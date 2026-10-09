package collect

import (
	"context"
	"errors"
	"fmt"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The dhcp_lease kind, and the two neighbour tables.
//
// What is true of any lease source, and is therefore here: the identity cascade, resolving
// the interface the backend named, reading one page of the standard envelope, normalising the
// field-name differences away, and writing one row per lease generation. Which backend, and
// what each one's own facts are, is in dhcplease_kea.go, dhcplease_dnsmasq.go and
// dhcplease_isc.go — one file each, which is what makes a fourth backend one more file.
//
// THIS IS THE KIND THAT MAKES THE SEAM NECESSARY RATHER THAN NOTIONAL. All three are
// registered in the schema today; on the firewall the survey probed, Dnsmasq serves DHCP and
// Kea is disabled, and somebody else's firewall is the other way round.
//
// THE VALIDITY START IS REPORTED BY KEA AND BY NO OTHER BACKEND, and the model now says so
// instead of substituting. Kea reports `valid_lifetime`, so the start is `expire` minus it — a
// real figure, stored. Dnsmasq reports only `expire` and a reserved lease reports neither, so
// both store a NULL start: a backend that cannot know says so.
//
// What makes a re-poll idempotent is a column of its own, dhcp_lease.generation_key, composed by
// store.GenerationKeyOf from the most specific instant the backend supplied and carrying the
// name of what it rests on — `start:`, `expiry:` or `observed_day:`. The expiry still
// discriminates a Dnsmasq generation, and a renewal is still a new one, but the column that says
// so no longer pretends to be a validity start. A reservation's key is the day it was observed,
// because a reservation is standing configuration rather than a generation, and it therefore
// yields one row per day rather than one per poll.
//
// THE NEIGHBOUR TABLES ARE READ WHETHER OR NOT ANY LEASE BACKEND IS. get_arp and get_ndp carry
// a MAC, an address, the interface and a vendor string, and the survey's inferred text named
// neither. They matter because on a firewall whose DHCP is unreadable — Kea with its control
// agent off, a Dnsmasq with no ranges, an ISC plugin that is gone — they are the only client
// identity there is, and without them every machine on such a network would fall to the
// weakest level of the cascade.
//
// They belong to no provider kind: they are the firewall's own view of its neighbours rather
// than an implementation of one of the eight contracts, so no availability row moves for them.
// What a failure costs is client identity, which the cascade already degrades for explicitly,
// and what it must not do is stop the lease pass.

// CollectDHCPLease runs one neighbour pass and one lease pass.
//
// The neighbour pass runs first and unconditionally: a machine it identifies by MAC is a
// machine the filter-log collector will find by address instead of minting the weakest
// identity for.
func (c *Collector) CollectDHCPLease(ctx context.Context) error {
	// No retention purge runs between this pass storing a lease or a neighbour and the
	// end of its derivation (purge.go).
	defer c.storing()()
	var failures []error

	var seen []string
	if err := c.collectNeighbours(ctx, &seen); err != nil {
		failures = append(failures, err)
	}
	// The instant the lease read begins. A lookup ingested at or after it may postdate the
	// leases this pass reads, so it is left for the next lease pass.
	leasesReadAt := c.now()
	complete, err := c.collectLeases(ctx, &seen)
	if err != nil {
		failures = append(failures, err)
	}

	// A lease or a neighbour entry is on-link evidence, and one that names an address
	// seen only in flows is a better identity for it: every address read is placed
	// again, which re-points the rows naming it and decides their attribution again.
	// A lease can also name the host a lookup logged as its client before the lease
	// was read, so the lookups whose host name resolved to no single address are
	// resolved again -- ONLY WHEN THE PASS WAS COMPLETE, as collectLeases decides it.
	// A lookup is examined once by a lease pass and marked; a pass that read no lease,
	// or missed a backend that may answer at the next pass, would use that one
	// examination up against an incomplete table and leave the lookup unresolved for
	// good (step-5A live corrections, H6).
	request := derivation{addresses: seen}
	if complete {
		request.hostnames, request.hostnamesBefore = true, leasesReadAt
	}
	if err := c.derive(ctx, request); err != nil {
		failures = append(failures, err)
	}

	if len(failures) > 0 {
		return fmt.Errorf("collect: the lease pass was incomplete: %w", joinErrors(failures))
	}
	return nil
}

// collectLeases reads every active backend's lease table, and reports whether the pass
// is complete: whether the unresolved host-name lookups may be examined against what it
// read. It is complete when it read at least one backend and every backend it did not
// read is one of these two (defect L1 of specs/SPEC-test-budget-and-two-live-defects.md):
//
//   - a backend that cannot be read by design, which answers ErrUnsupportedRead. It
//     contributes no lease, at this pass or any later one, so waiting for it would wait
//     for ever: before this rule an ISC plugin active beside Dnsmasq kept every
//     unresolved lookup unexamined;
//   - a backend that has failed on every one of the last lease_backend_failure_pass_limit
//     passes, this one included. A failure is reported in its availability row, as
//     before, and the detail says how many passes in a row it has failed and the limit.
//     Until the limit the pass is incomplete, so one or two failed passes -- a restart --
//     do not spend each lookup's one examination on a partial table; from the limit on,
//     the other backends' leases are what there is, and the lookups wait no further.
//
// The count of failed passes is kept in memory: a restart begins it again, which delays
// the examination by at most the limit and never forgoes it.
//
// SEVERAL ARE NORMAL, and the deployment is the ordinary one: one server issuing on
// one VLAN and another on a second, two scopes with no overlap. A machine leased by
// both is ONE client holding TWO leases, because the identity cascade keys on the
// DHCP client identifier and then on the MAC, neither of them scoped to an
// interface. One failing backend does not stop the others: they are separate
// servers behind separate endpoints, and letting the first failure end the pass
// would turn one unreadable server into two.
func (c *Collector) collectLeases(ctx context.Context, seen *[]string) (bool, error) {
	sources, err := c.activeSources(ctx, KindDHCPLease)
	if err != nil {
		return false, err
	}
	var failures []error
	read, waiting := 0, 0
	for _, active := range sources {
		outcome, err := c.collectLeasesFrom(ctx, active, seen)
		if err != nil {
			failures = append(failures, err)
		}
		switch outcome {
		case leaseBackendRead:
			read++
		case leaseBackendFailed:
			waiting++
		}
	}
	complete := read > 0 && waiting == 0
	if len(failures) > 0 {
		return complete, fmt.Errorf("collect: the lease read was incomplete: %w", joinErrors(failures))
	}
	return complete, nil
}

// leaseBackendOutcome is what one lease pass made of one backend.
type leaseBackendOutcome int

const (
	// leaseBackendRead: its lease table was read.
	leaseBackendRead leaseBackendOutcome = iota
	// leaseBackendUnsupported: it cannot be read by design, and contributes no lease.
	leaseBackendUnsupported
	// leaseBackendFailed: it failed, on fewer passes in a row than the limit, so the
	// pass waits for it.
	leaseBackendFailed
	// leaseBackendGivenUp: it failed, on every pass up to the limit, so the pass no
	// longer waits for it.
	leaseBackendGivenUp
)

// noteLeaseFailure counts one more failed pass for a backend, and returns the count and
// the limit in force.
func (c *Collector) noteLeaseFailure(providerID int64) (int64, int64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.leaseFailures == nil {
		c.leaseFailures = map[int64]int64{}
	}
	c.leaseFailures[providerID]++
	return c.leaseFailures[providerID], c.leaseFailureLimit
}

// clearLeaseFailures ends a backend's run of failed passes.
func (c *Collector) clearLeaseFailures(providerID int64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.leaseFailures, providerID)
}

// failedOutcome is a failed backend's outcome: the pass waits for it until it has
// failed on as many passes in a row as the limit.
func failedOutcome(failed, limit int64) leaseBackendOutcome {
	if failed >= limit {
		return leaseBackendGivenUp
	}
	return leaseBackendFailed
}

// leaseFailureDetail is what a failing backend's availability detail adds about the
// host-name retry: how many passes in a row it has failed, and the limit.
func leaseFailureDetail(failed, limit int64) string {
	if failed >= limit {
		return fmt.Sprintf("failed on %d lease passes in a row, the limit of %d (setting %s): "+
			"unresolved host names are re-examined without this backend", failed, limit,
			config.KeyLeaseBackendFailurePassLimit)
	}
	return fmt.Sprintf("failed on %d lease passes in a row; unresolved host names wait for this "+
		"backend until %d (setting %s)", failed, limit, config.KeyLeaseBackendFailurePassLimit)
}

// collectLeasesFrom reads one backend's lease table, and reports what the pass made of
// the backend.
func (c *Collector) collectLeasesFrom(ctx context.Context, active activeSource, seen *[]string) (
	leaseBackendOutcome, error) {
	providerKey, providerID := active.providerKey, active.providerID
	source, registered := leaseSources[providerKey]
	if !registered {
		failed, limit := c.noteLeaseFailure(providerID)
		return failedOutcome(failed, limit), fmt.Errorf(
			"collect: the active dhcp_lease provider %q has no implementation", providerKey)
	}

	observations, result, readErr := source.leases(ctx, c, c.pages().DHCPLease)
	if errors.Is(readErr, ErrUnsupportedRead) {
		// The implementation is registered and its material cannot be read. That is a state,
		// recorded with its reason, and not a failed pass: nothing was attempted.
		c.clearLeaseFailures(providerID)
		return leaseBackendUnsupported, c.writeAvailability(ctx, providerID, result.state,
			result.probe, readErr.Error())
	}
	if readErr != nil {
		failed, limit := c.noteLeaseFailure(providerID)
		detail := leaseFailureDetail(failed, limit)
		if result.detail != "" {
			detail = result.detail + "; " + detail
		}
		if writeErr := c.writeAvailability(ctx, providerID, result.state,
			result.probe, detail); writeErr != nil {
			return failedOutcome(failed, limit), writeErr
		}
		return failedOutcome(failed, limit), readErr
	}
	c.clearLeaseFailures(providerID)
	if writeErr := c.writeAvailability(ctx, providerID, result.state,
		result.probe, result.detail); writeErr != nil {
		return leaseBackendFailed, writeErr
	}

	snapshot := c.Discovery()
	now := c.now()
	for _, observation := range observations {
		if err := c.ingestLease(ctx, observation, snapshot, providerID, providerKey, now); err != nil {
			return leaseBackendFailed, err
		}
		*seen = append(*seen, observation.Address)
	}
	return leaseBackendRead, nil
}

// ingestLease turns one lease observation into a machine and a lease generation.
//
// The provider and the backend are both passed, and they are not the same thing: the
// provider is WHICH SERVER issued the lease, which the row records so a screen can
// name it, and the backend is WHICH RESPONSE SHAPE it was read from. Today one
// implementation has one backend; the row keeps both so that a second
// implementation reading the same shape somewhere else does not collapse into the
// first.
func (c *Collector) ingestLease(ctx context.Context, observation leaseObservation,
	snapshot Discovery, providerID int64, backend string, now int64) error {
	// The lease endpoints report the interface under up to three names. Only two of them are
	// join keys opnview already holds — the configuration key and the device — so both are
	// tried against the maps discovery built, and never the description, which is a name.
	var interfaceID *int64
	if id, found := snapshot.InterfaceIDByIdentifier[observation.InterfaceIdentifier]; found {
		interfaceID = &id
	}
	if interfaceID == nil {
		if id, found := snapshot.InterfaceIDByDevice[observation.InterfaceDevice]; found {
			interfaceID = &id
		}
	}

	// The generation key, and the start, which is stored only when the backend reported one.
	// Composed by the store so that every backend composes it the same way: the idempotence of
	// the lease table rests on that and on nothing else.
	generationKey := store.GenerationKeyOf(observation.StartsAt, observation.ExpiresAt, DayStart(now))

	identity := leaseIdentity(observation, interfaceID, now)
	clientID, err := c.store.UpsertClient(ctx, store.Client{
		Identity:    identity,
		InterfaceID: interfaceID,
		MAC:         observation.MAC,
		Hostname:    observation.Hostname,
		VendorHint:  observation.VendorHint,
		LastAddress: &observation.Address,
	}, now)
	if err != nil {
		return err
	}

	return c.store.InsertDHCPLease(ctx, providerID, store.DHCPLease{
		ClientID:      &clientID,
		Backend:       backend,
		Address:       observation.Address,
		MAC:           observation.MAC,
		Hostname:      observation.Hostname,
		DHCPClientID:  observation.DHCPClientID,
		DUID:          observation.DUID,
		IAID:          observation.IAID,
		VendorHint:    observation.VendorHint,
		LeaseState:    observation.LeaseState,
		InterfaceID:   interfaceID,
		StartsAt:      observation.StartsAt,
		GenerationKey: generationKey,
		ExpiresAt:     observation.ExpiresAt,
		ObservedAt:    now,
	})
}

// leaseIdentity picks the most stable level of the cascade the observation supports.
//
// TWO SERVERS REPORTING ONE MACHINE RESOLVE TO ONE CLIENT, and that is what makes the
// dhcp_lease kind safe to run several providers of. Neither of the top two levels is scoped to
// an interface: a DHCP client identifier is a property of the CLIENT — it is what the machine
// itself sent in option 61, so both servers report the same one — and a MAC is a property of
// its network adapter. So a machine leased on one VLAN by one server and on another by a second
// is one client holding two leases. The survey establishes that both current backends report
// `client_id`, so neither level is available to only one of them.
//
// THE ONE CASE THAT STILL SPLITS A MACHINE IN TWO is an ASYMMETRIC report: if one server
// supplies a DHCP client identifier for a machine and the other supplies none, the first lease
// resolves at the `dhcp_client_id` level and the second falls to `mac`, and the two identities
// are different rows. It is worth being precise about what that is and is not. It is NOT
// introduced by running two servers: the same split already happens in sequence when a firewall
// switches backends, because the level a lease resolves at is a property of what the source
// reported. And it is not reachable through a client behaving consistently, because option 61 is
// the machine's own to send. It is reachable if a backend stops reporting a field the other
// reports. Closing it would mean merging identities across cascade levels on a shared MAC, which
// is a change to the cascade rather than to this kind, and it is not attempted here.
func leaseIdentity(observation leaseObservation, interfaceID *int64,
	now int64) store.ClientIdentity {
	for _, candidate := range []*string{
		observation.DHCPClientID, observation.DUID, observation.IAID,
	} {
		if candidate != nil && *candidate != "" {
			return store.ClientIdentity{Kind: store.IdentityDHCPClientID, Key: *candidate}
		}
	}
	if observation.MAC != nil {
		return store.ClientIdentity{Kind: store.IdentityMAC, Key: *observation.MAC}
	}
	return store.ClientIdentity{
		Kind: store.IdentityAddressInInterface,
		Key:  addressIdentityKey(interfaceID, observation.Address, now),
	}
}

// collectNeighbours reads the ARP and NDP tables.
func (c *Collector) collectNeighbours(ctx context.Context, seen *[]string) error {
	snapshot := c.Discovery()
	now := c.now()
	var failures []error

	for _, endpoint := range []opnsense.Endpoint{opnsense.ARPTable, opnsense.NDPTable} {
		response, err := c.client.Call(ctx, endpoint, opnsense.RequestOptions{})
		if err != nil && response.Outcome == "" {
			failures = append(failures, err)
			continue
		}
		if !response.OK() {
			failures = append(failures,
				fmt.Errorf("collect: %s answered %s", endpoint.Path, response.Outcome))
			continue
		}
		rows, err := decode.Rows(response.Body)
		if err != nil {
			failures = append(failures, fmt.Errorf("collect: reading %s: %w", endpoint.Path, err))
			continue
		}
		for _, row := range rows {
			if err := c.ingestNeighbour(ctx, row, snapshot, now); err != nil {
				failures = append(failures, err)
				break
			}
			*seen = append(*seen, decode.RawString(row, "ip"))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("collect: the neighbour tables were incomplete: %w", joinErrors(failures))
	}
	return nil
}

// ingestNeighbour turns one ARP or NDP row into a machine at the MAC level.
func (c *Collector) ingestNeighbour(ctx context.Context, row decode.Object,
	snapshot Discovery, now int64) error {
	mac := macPointer(decode.RawString(row, "mac"))
	address, hasAddress := decode.String(row, "ip")
	if mac == nil || !hasAddress {
		// Without both there is nothing here the cascade can use: a MAC with no address names a
		// machine opnview cannot match to a flow, and an address with no MAC is what the
		// filter-log collector already handles.
		return nil
	}

	var interfaceID *int64
	if id, found := snapshot.InterfaceIDByDevice[decode.RawString(row, "intf")]; found {
		interfaceID = &id
	}
	if interfaceID != nil {
		if _, upstream := snapshot.Upstream[*interfaceID]; upstream {
			// A neighbour of an upstream interface -- the provider's router, typically --
			// is outside, and an outside address never becomes a client row.
			return nil
		}
	}
	// The tables list this firewall's own interface entries as well as its neighbours.
	// An entry at an address the firewall holds at this instant is this firewall, which
	// is never a client (step-5A live corrections, item 1.2; defect L2 of
	// specs/SPEC-test-budget-and-two-live-defects.md, where each such entry had become a
	// MAC-level client), so it creates and updates nothing.
	if firewall, err := c.store.IsThisFirewallAt(ctx, address, now); err != nil {
		return err
	} else if firewall {
		return nil
	}

	_, err := c.store.UpsertClient(ctx, store.Client{
		Identity:    store.ClientIdentity{Kind: store.IdentityMAC, Key: *mac},
		InterfaceID: interfaceID,
		MAC:         mac,
		// `manufacturer` is the neighbour table's spelling of the vendor string the leases call
		// `mac_info`. It is context and is never a basis for classifying a machine.
		VendorHint:  decode.StringPointer(row, "manufacturer"),
		LastAddress: &address,
	}, now)
	return err
}

// readLeaseRows is the part of a lease read that is the same for every backend: one page, the
// standard envelope, and the availability state the answer implies.
//
// The page holds pageSize rows, the operator's page size for the kind. The table is small —
// one row per active lease — so one page is the whole of it in practice.
//
// IT IS A FREE FUNCTION OVER THE PORT RATHER THAN A METHOD ON IT. Only this kind's
// implementations call it, and a port that gains a method for one kind's convenience stops
// being a boundary: the six methods `session` grants are ones any kind could plausibly need,
// and that is what keeps the list short enough to become a protocol. Written this way it reads
// identically at the call site and costs a connector nothing, and when connectors speak a
// protocol it travels with the lease reader instead of becoming a verb the far side has to
// implement.
func readLeaseRows(ctx context.Context, host session, endpoint opnsense.Endpoint, pageSize int) (
	[]decode.Object, probeResult, error) {
	result := probeResult{probe: endpoint, state: store.StateUnavailable}

	response, err := host.call(ctx, endpoint, opnsense.RequestOptions{
		Form: opnsense.Pagination(1, pageSize),
	})
	if err != nil && response.Outcome == "" {
		return nil, result, err
	}
	if !response.OK() {
		result.detail = response.Detail
		return nil, result, fmt.Errorf("collect: %s answered %s", endpoint.Path, response.Outcome)
	}

	rows, err := decode.Rows(response.Body)
	if err != nil {
		result.detail = "the lease table could not be read"
		return nil, result, fmt.Errorf("collect: reading %s: %w", endpoint.Path, err)
	}
	result.state = store.StateReachable
	result.separable = true
	if len(rows) == 0 {
		// Returned as an empty, non-nil slice so a caller can tell "read nothing" from "did
		// not read".
		return []decode.Object{}, result, nil
	}
	return rows, result, nil
}

// decodeLease reads the fields the two current backends and the legacy plugin share, with the
// field-name differences normalised — `hwaddr` against `mac`, `state` against `lease_type` and
// `is_reserved`.
//
// IT DOES NOT INVENT A VALIDITY START. What the shared envelope carries is an expiry; the start
// is left nil here, and only a backend that genuinely reports one — Kea, through
// `valid_lifetime` — fills it in its own file.
func decodeLease(row decode.Object) (leaseObservation, bool) {
	address, present := decode.String(row, "address")
	if !present {
		// A lease with no address names no machine and keys nothing.
		return leaseObservation{}, false
	}

	mac := macPointer(decode.RawString(row, "hwaddr"))
	if mac == nil {
		// `mac` is the legacy plugin's spelling of the same field, and normalising the two
		// names is the only normalisation the survey says leases need.
		mac = macPointer(decode.RawString(row, "mac"))
	}

	expiresAt := decode.EpochPointer(row, "expire")
	if expiresAt == nil {
		expiresAt = decode.EpochPointer(row, "ends")
	}

	observation := leaseObservation{
		Address:             address,
		MAC:                 mac,
		Hostname:            decode.StringPointer(row, "hostname"),
		DHCPClientID:        decode.StringPointer(row, "client_id"),
		DUID:                decode.StringPointer(row, "duid"),
		IAID:                decode.StringPointer(row, "iaid"),
		VendorHint:          decode.StringPointer(row, "mac_info"),
		LeaseState:          normaliseLeaseState(row),
		InterfaceIdentifier: decode.RawString(row, "if_name"),
		InterfaceDevice:     decode.RawString(row, "device"),
		ExpiresAt:           expiresAt,
	}
	return observation, true
}

// normaliseLeaseState maps the three backends' several state fields onto the one vocabulary
// the schema constrains: `state` on Kea, `lease_type` plus `is_reserved` on Dnsmasq, and
// `state` plus `status` on the legacy plugin.
//
// A value none of them matches is `unknown`, never folded into `active`: a screen that showed
// an expired lease as active would name a machine that has gone.
func normaliseLeaseState(row decode.Object) string {
	if reserved := decode.Flag(row, "is_reserved"); reserved != nil && *reserved {
		return "reserved"
	}
	for _, key := range []string{"state", "lease_type", "status"} {
		value, present := decode.String(row, key)
		if !present {
			continue
		}
		switch decode.LowerASCII(value) {
		case "active", "assigned", "1":
			return "active"
		case "expired", "released", "free", "backup", "0":
			return "expired"
		case "reserved", "static":
			return "reserved"
		}
	}
	return "unknown"
}

// leaseFailurePasses is how many lease passes in a row one backend has failed.
func (c *Collector) leaseFailurePasses(providerID int64) int64 {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.leaseFailures[providerID]
}
