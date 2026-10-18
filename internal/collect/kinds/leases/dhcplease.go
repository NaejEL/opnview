package leases

import (
	"context"
	"errors"
	"fmt"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/collect/identity"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The dhcp_lease kind.
//
// What is true of any lease source, and is therefore here: the identity cascade, resolving
// the interface the backend named, reading one page of the standard envelope, normalising the
// field-name differences away, and writing one row per lease generation. Which backend, and
// what each one's own facts are, is in its plugin directory, plugins/<product>/ — one directory each,
// which is what makes a fourth backend one more directory.
//
// THIS IS THE KIND THAT MAKES THE SEAM NECESSARY RATHER THAN NOTIONAL. All three are
// implementations today; on the firewall the survey probed, Dnsmasq serves DHCP and
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
// The neighbour tables, read beside the leases until the plugin-API amendment, are the neighbour
// kind's (internal/collect/kinds/neighbours, plugins/neighbours).

// CollectDHCPLease runs one lease pass.
func (c *LeaseCoordinator) CollectDHCPLease(ctx context.Context) error {
	// No retention purge runs between this pass storing a lease and the end of its
	// derivation (purge.go).
	defer c.Storing()()
	var failures []error

	var seen []string
	// The instant the lease read begins. A lookup ingested at or after it may postdate the
	// leases this pass reads, so it is left for the next lease pass.
	leasesReadAt := c.Now()
	complete, err := c.collectLeases(ctx, &seen)
	if err != nil {
		failures = append(failures, err)
	}

	// A lease is on-link evidence, and one that names an address
	// seen only in flows is a better identity for it: every address read is placed
	// again, which re-points the rows naming it and decides their attribution again.
	// A lease can also name the host a lookup logged as its client before the lease
	// was read, so the lookups whose host name resolved to no single address are
	// resolved again -- ONLY WHEN THE PASS WAS COMPLETE, as collectLeases decides it.
	// A lookup is examined once by a lease pass and marked; a pass that read no lease,
	// or missed a backend that may answer at the next pass, would use that one
	// examination up against an incomplete table and leave the lookup unresolved for
	// good (step-5A live corrections, H6).
	//
	// The resolver's local data names hosts too, so when a local-data source is in use
	// the examination also waits until it has been read after the lookups were ingested
	// (internal/collect/core/hostnamesources.go).
	request := derive.Derivation{Addresses: seen}
	if complete {
		before, examine, err := c.NoteHostNameSourceRead(ctx, Kind, leasesReadAt)
		if err != nil {
			failures = append(failures, err)
		} else if examine {
			request.Hostnames, request.HostnamesBefore = true, before
		}
	}
	if err := derive.On(c.Collector).Derive(ctx, request); err != nil {
		failures = append(failures, err)
	}

	if len(failures) > 0 {
		return fmt.Errorf("collect: the lease pass was incomplete: %w", core.JoinErrors(failures))
	}
	return nil
}

// collectLeases reads every active backend's lease table, and reports whether the pass
// is complete: whether the unresolved host-name lookups may be examined against what it
// read. It is complete when it read at least one backend and every backend it did not
// read is one of these two (defect L1 of specs/SPEC-test-budget-and-two-live-defects.md):
//
//   - a backend that cannot be read by design, which answers provider.ErrUnsupportedRead. It
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
func (c *LeaseCoordinator) collectLeases(ctx context.Context, seen *[]string) (bool, error) {
	sources, err := c.ActiveSources(ctx, Kind)
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
		return complete, fmt.Errorf("collect: the lease read was incomplete: %w", core.JoinErrors(failures))
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
func (c *LeaseCoordinator) noteLeaseFailure(providerID int64) (int64, int64) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	if c.LeaseFailures == nil {
		c.LeaseFailures = map[int64]int64{}
	}
	c.LeaseFailures[providerID]++
	return c.LeaseFailures[providerID], c.LeaseFailureLimit
}

// clearLeaseFailures ends a backend's run of failed passes.
func (c *LeaseCoordinator) clearLeaseFailures(providerID int64) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	delete(c.LeaseFailures, providerID)
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
func (c *LeaseCoordinator) collectLeasesFrom(ctx context.Context, active core.ActiveSource, seen *[]string) (
	leaseBackendOutcome, error) {
	providerKey, providerID := active.ProviderKey, active.ProviderID
	source, registered := c.SourceSet.Lookup(Kind, providerKey)
	if !registered {
		failed, limit := c.noteLeaseFailure(providerID)
		return failedOutcome(failed, limit), fmt.Errorf(
			"collect: the active dhcp_lease provider %q has no implementation", providerKey)
	}

	answer, result, readErr := source.Implementation.Call(ctx, c, "leases", c.PageSize(Kind))
	observations := provider.Records(answer, "leases")
	if errors.Is(readErr, provider.ErrUnsupportedRead) {
		// The implementation is registered and its material cannot be read. That is a state,
		// recorded with its reason, and not a failed pass: nothing was attempted.
		c.clearLeaseFailures(providerID)
		return leaseBackendUnsupported, c.WriteAvailability(ctx, providerID, result.State,
			result.Outcome, result.Probe, readErr.Error())
	}
	if readErr != nil {
		failed, limit := c.noteLeaseFailure(providerID)
		detail := leaseFailureDetail(failed, limit)
		if result.Detail != "" {
			detail = result.Detail + "; " + detail
		}
		if writeErr := c.WriteAvailability(ctx, providerID, result.State,
			result.Outcome, result.Probe, detail); writeErr != nil {
			return failedOutcome(failed, limit), writeErr
		}
		return failedOutcome(failed, limit), readErr
	}

	// THE AVAILABILITY IS WRITTEN AFTER THE LEASES ARE STORED, not before (SPEC-step-5b-
	// widget-api.md, B8g). Written first, a store failure in storeLeases left the backend
	// reading `reachable` with the read's detail, which said the opposite of what had
	// happened: the firewall answered, and opnview kept nothing of it.
	snapshot := c.Discovery()
	now := c.Now()
	if err := c.storeLeases(ctx, observations, snapshot, providerID, providerKey, now); err != nil {
		// A failed pass for the host-name retry, exactly as a read failure is: the
		// leases this pass would have contributed are not in the store.
		failed, limit := c.noteLeaseFailure(providerID)
		detail := fmt.Sprintf("the backend answered and its leases could not be stored: %v; %s",
			err, leaseFailureDetail(failed, limit))
		// Unavailable, because nothing of this backend's answer reached the store a
		// widget reads; the outcome says the fault is opnview's, not the firewall's.
		if writeErr := c.WriteAvailability(ctx, providerID, vocab.StateUnavailable,
			vocab.OutcomeStoreFailure, result.Probe, detail); writeErr != nil {
			return failedOutcome(failed, limit), core.JoinErrors([]error{err, writeErr})
		}
		return failedOutcome(failed, limit), err
	}
	for _, observation := range observations {
		*seen = append(*seen, provider.Text(observation, "address"))
	}
	c.clearLeaseFailures(providerID)
	if writeErr := c.WriteAvailability(ctx, providerID, result.State,
		result.Outcome, result.Probe, result.Detail); writeErr != nil {
		return leaseBackendFailed, writeErr
	}
	return leaseBackendRead, nil
}

// storeLeases stores the machines and the lease generations of a pass: two statements whatever the
// number of observations (PA17 of specs/SPEC-backend-completion-plugins.md).
//
// The provider and the backend are both passed, and they are not the same thing: the
// provider is WHICH SERVER issued the lease, which the row records so a screen can
// name it, and the backend is WHICH RESPONSE SHAPE it was read from. Today one
// implementation has one backend; the row keeps both so that a second
// implementation reading the same shape somewhere else does not collapse into the
// first.
func (c *LeaseCoordinator) storeLeases(ctx context.Context, observations []provider.Answer,
	snapshot core.Discovery, providerID int64, backend string, now int64) error {
	clients := make([]vocab.Client, len(observations))
	leases := make([]vocab.DHCPLease, len(observations))
	for index, observation := range observations {
		clients[index], leases[index] = leaseRows(observation, snapshot, backend, now)
	}
	ids, err := c.Store.UpsertClients(ctx, clients, now)
	if err != nil {
		return err
	}
	for index := range leases {
		leases[index].ClientID = &ids[index]
	}
	return c.Store.InsertDHCPLeases(ctx, providerID, leases)
}

// leaseRows is the machine and the lease generation of one observation, the lease's client still to be
// written.
func leaseRows(observation provider.Answer, snapshot core.Discovery, backend string,
	now int64) (vocab.Client, vocab.DHCPLease) {
	lease := vocab.DHCPLease{
		Backend: backend, Address: provider.Text(observation, "address"),
		MAC: decode.MACPointer(provider.Text(observation, "mac")), Hostname: provider.TextPointer(observation, "hostname"),
		DHCPClientID: provider.TextPointer(observation, "dhcp_client_id"), DUID: provider.TextPointer(observation, "duid"),
		IAID: provider.TextPointer(observation, "iaid"), VendorHint: provider.TextPointer(observation, "vendor_hint"),
		LeaseState: provider.Text(observation, "lease_state"), StartsAt: provider.IntegerPointer(observation, "starts_at"),
		ExpiresAt: provider.IntegerPointer(observation, "expires_at"), ObservedAt: now,
	}
	// The lease endpoints report the interface under up to three names. Only two of them are
	// join keys opnview already holds — the configuration key and the device — so both are
	// tried against the maps discovery built, and never the description, which is a name.
	var interfaceID *int64
	if id, found := snapshot.InterfaceIDByIdentifier[provider.Text(observation, "interface_identifier")]; found {
		interfaceID = &id
	}
	if interfaceID == nil {
		if id, found := snapshot.InterfaceIDByDevice[provider.Text(observation, "interface_device")]; found {
			interfaceID = &id
		}
	}

	// The generation key, and the start, which is stored only when the backend reported one.
	// Composed by the store so that every backend composes it the same way: the idempotence of
	// the lease table rests on that and on nothing else.
	lease.GenerationKey = vocab.GenerationKeyOf(lease.StartsAt, lease.ExpiresAt, identity.DayStart(now))
	lease.InterfaceID = interfaceID

	address := lease.Address
	client := vocab.Client{
		Identity:    leaseIdentity(lease, interfaceID, now),
		InterfaceID: interfaceID,
		MAC:         lease.MAC,
		Hostname:    lease.Hostname,
		VendorHint:  lease.VendorHint,
		LastAddress: &address,
	}
	return client, lease
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
func leaseIdentity(observation vocab.DHCPLease, interfaceID *int64,
	now int64) vocab.ClientIdentity {
	for _, candidate := range []*string{
		observation.DHCPClientID, observation.DUID, observation.IAID,
	} {
		if candidate != nil && *candidate != "" {
			return vocab.ClientIdentity{Kind: vocab.IdentityDHCPClientID, Key: *candidate}
		}
	}
	if observation.MAC != nil {
		return vocab.ClientIdentity{Kind: vocab.IdentityMAC, Key: *observation.MAC}
	}
	return vocab.ClientIdentity{
		Kind: vocab.IdentityAddressInInterface,
		Key:  identity.AddressIdentityKey(interfaceID, observation.Address, now),
	}
}

// Kind is the kind this coordinator collects.
const Kind = "dhcp_lease"
