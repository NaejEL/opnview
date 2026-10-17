package lookups

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/collect/identity"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The dns_lookup kind.
//
// What is true of any source of resolver lookups, and is therefore here: resolving the
// querying machine, resolving the list that refused a lookup, walking the pages, and detecting
// a gap. Which resolver, how its window behaves, and what can serve as a de-duplication key
// are in each resolver's plugin directory, plugins/<product>/, because all three are facts about a
// product.
//
// THE KIND NEVER PRESENTS WHAT IT READ AS COVERAGE OF A WINDOW. The one implementation that
// can be read is a ring buffer of the most recent lookups whose time bounds are ignored
// outright, measured on a live firewall, so there is nothing here that returns, stores or
// reports a covered interval — and the availability detail says in words what was read and
// what it is not.

// CollectDNSLookup runs one pass of the active implementation of the dns_lookup kind.
func (c *LookupCoordinator) CollectDNSLookup(ctx context.Context) error {
	providerKey, providerID, active, err := c.ActiveSourceKey(ctx, Kind)
	if err != nil {
		return err
	}
	if !active {
		// Either the resolver's query reporting is off, or the only other implementation is one
		// whose log grammar is not recorded. Both are states the probe round wrote down, and
		// neither is an absence of lookups.
		return nil
	}
	source, registered := c.SourceSet.Lookup(Kind, providerKey)
	if !registered {
		return fmt.Errorf("collect: the active dns_lookup provider %q has no implementation",
			providerKey)
	}

	newestStored, hasStored, err := c.Store.NewestDNSResolutionAt(ctx)
	if err != nil {
		return err
	}

	// No retention purge runs between this pass storing its lookups and the end of its
	// derivation (purge.go).
	defer c.Storing()()
	// The instant every lookup of this pass is stamped with stays in flight until the
	// pass has written them, so a lease pass running meanwhile does not move the point it
	// next resolves host names from past lookups not yet committed.
	now, ingest := derive.On(c.Collector).BeginIngest()
	defer derive.On(c.Collector).EndIngest(ingest)
	// The two values the implementation states (its contract's `values`): how many pages one pass may
	// walk, and the window it asks for, which is never presented as coverage.
	span := provider.Integer(source.Values, "requested_span_seconds")
	pageBound := int(provider.Integer(source.Values, "page_bound"))
	windowStart := now - span

	var (
		lastResult             provider.Result
		oldestSeen             int64
		haveOldest             bool
		outsideRequestedWindow int
		stored                 derive.Derivation
		totalRows              int
	)

	for page := 1; page <= pageBound; page++ {
		answer, result, readErr := source.Implementation.Call(ctx, c, "lookups", page)
		records := make([]vocab.DNSResolution, 0, len(provider.Records(answer, "lookups")))
		for _, record := range provider.Records(answer, "lookups") {
			records = append(records, reportedLookup(record))
		}
		lastResult = result
		if errors.Is(readErr, provider.ErrUnsupportedRead) {
			// Registered, probed, and not readable. A state with its reason, not a failed pass.
			return c.WriteAvailability(ctx, providerID, result.State, result.Outcome, result.Probe,
				readErr.Error())
		}
		if readErr != nil {
			// The pages read before this one are stored, and are derived all the same.
			if writeErr := c.WriteAvailability(ctx, providerID, result.State,
				result.Outcome, result.Probe, result.Detail); writeErr != nil {
				return derive.On(c.Collector).DeriveAfter(ctx, stored, core.JoinErrors([]error{readErr, writeErr}))
			}
			return derive.On(c.Collector).DeriveAfter(ctx, stored, readErr)
		}
		if len(records) == 0 {
			break
		}
		totalRows += len(records)

		newOnThisPage := 0
		for _, record := range records {
			if !haveOldest || record.LookedUpAt < oldestSeen {
				oldestSeen, haveOldest = record.LookedUpAt, true
			}
			if span > 0 && (record.LookedUpAt < windowStart || record.LookedUpAt > now) {
				outsideRequestedWindow++
			}

			// THE INSERT DECIDES whether a row is new, whatever its instant: the identity is
			// lookup_key, and a different lookup can share the instant of the newest one
			// stored -- or of any older one, since the buffer is not ordered by anything
			// opnview controls. 5A skipped every row at or before the newest stored instant
			// without inserting it, and lost such a lookup (step-5A live corrections, F6).
			lookup, err := c.buildDNSResolution(ctx, record, providerKey, now)
			if err != nil {
				return derive.On(c.Collector).DeriveAfter(ctx, stored, c.lookupStoreFailure(ctx, providerID, result, err))
			}
			inserted, err := c.Store.StoreDNSResolution(ctx, lookup)
			if err != nil {
				return derive.On(c.Collector).DeriveAfter(ctx, stored, c.lookupStoreFailure(ctx, providerID, result, err))
			}
			if !inserted {
				continue
			}
			newOnThisPage++
			stored.Addresses = append(stored.Addresses, lookup.ClientAddress)
			// A lookup can name a flow up to the timing delay after it, and through the
			// resolver's cache up to the cache-answer cap after it.
			stored.Widen(record.LookedUpAt, record.LookedUpAt+derive.On(c.Collector).AttributionWindows().Reach())
		}
		if newOnThisPage == 0 {
			// Every row on this page was already stored, so the pages behind it, which are
			// older still, were stored by an earlier pass: the page stop is unchanged, only
			// what counts as new now comes from the insert.
			break
		}
	}

	// The availability detail is where the honesty lives. It says what was read and what it is
	// not: the most recent lookups, and not coverage of the window that was asked for.
	detail := fmt.Sprintf(
		"read %d of the most recent lookups; the endpoint ignores timeStart and timeEnd, so this "+
			"is not coverage of the %d-second window that was requested", totalRows, span)
	if outsideRequestedWindow > 0 {
		detail += fmt.Sprintf(" (%d of the rows fell outside it, which is that behaviour showing)",
			outsideRequestedWindow)
	}
	if totalRows == 0 {
		detail = "reachable and the query report returned no lookup, which is not an absence of " +
			"lookups"
	}
	outcome := vocab.OutcomeHealthy
	if totalRows == 0 {
		outcome = vocab.OutcomeHealthyEmpty
	}
	if err := c.WriteAvailability(ctx, providerID, vocab.StateReachable, outcome,
		lastResult.Probe, detail); err != nil {
		return derive.On(c.Collector).DeriveAfter(ctx, stored, err)
	}

	// The gap test, and the only one this kind's material permits: the buffer holds the most
	// recent lookups, so if its OLDEST row is newer than the newest row stored, the lookups
	// between the two rotated out before opnview read them.
	if hasStored && haveOldest && oldestSeen > newestStored {
		gapDetail := fmt.Sprintf(
			"the oldest of the %d lookups read is newer than the newest lookup stored, so the "+
				"interval between them rotated out of the endpoint's ring buffer before it was "+
				"read; the endpoint honours no window, so nothing can recover it", totalRows)
		if err := c.Store.RecordCollectionGap(ctx, vocab.CollectionGap{
			ProviderID:      providerID,
			IntervalStartAt: newestStored,
			IntervalEndAt:   oldestSeen,
			Reason:          vocab.GapResolverWindowNotHonoured,
			Detail:          &gapDetail,
			DetectedAt:      now,
		}); err != nil {
			return derive.On(c.Collector).DeriveAfter(ctx, stored, err)
		}
	}

	// The lookups are stored, so their queriers can be placed, and every flow they could
	// name -- up to the attribution delay after each -- has its attribution decided again.
	return derive.On(c.Collector).Derive(ctx, stored)
}

// lookupStoreFailure records that the resolver's query report answered and its lookups
// could not be stored, and returns the store's error with any failure to record it. The
// provider is unavailable, because what a widget reads did not receive the answer, and the
// outcome names the fault as opnview's rather than the firewall's, as the lease pass does
// (SPEC-step-5b-widget-api.md, B8g). The pages stored before the failure stay stored.
func (c *LookupCoordinator) lookupStoreFailure(ctx context.Context, providerID int64, result provider.Result,
	storeErr error) error {
	detail := fmt.Sprintf("the resolver's query report answered and its lookups could not be stored: %v",
		storeErr)
	if writeErr := c.WriteAvailability(ctx, providerID, vocab.StateUnavailable, vocab.OutcomeStoreFailure,
		result.Probe, detail); writeErr != nil {
		return core.JoinErrors([]error{storeErr, writeErr})
	}
	return storeErr
}

// buildDNSResolution turns one record into a row, resolving the querying machine.
func (c *LookupCoordinator) buildDNSResolution(ctx context.Context, record vocab.DNSResolution,
	resolver string, now int64) (vocab.DNSResolution, error) {
	// The resolver's query report gives, in place of the querying address, the name a
	// reverse lookup of it returned whenever there was one (opnsense/core 26.7.3,
	// scripts/unbound/stats.py, the `details` query, over the `client` table
	// scripts/unbound/logger.py fills through socket.gethostbyaddr). Such a name is
	// resolved back to an address through the DHCP leases valid at the lookup's
	// instant, or, when no lease names it, through the resolver's local data held then,
	// and only when exactly one address answers; otherwise the lookup names no machine,
	// and the resolution is recorded either way.
	clientAddress := record.ClientAddress
	resolution := vocab.ClientResolutionLoggedAddress
	var clientHostname *string
	if clientAddress != "" {
		if _, err := netip.ParseAddr(clientAddress); err != nil {
			logged := clientAddress
			clientHostname = &logged
			clientAddress, resolution, err = c.Store.ResolveLoggedHostname(ctx, logged, record.ClientFamily,
				record.LookedUpAt)
			if err != nil {
				return vocab.DNSResolution{}, err
			}
		}
	}

	var clientID *int64
	if clientAddress != "" && (resolution == vocab.ClientResolutionLoggedAddress ||
		resolution == vocab.ClientResolutionLeased || resolution == vocab.ClientResolutionLocalData) {
		id, _, found, err := c.Store.ClientRefByAddressSince(ctx, clientAddress,
			now-identity.AddressIdleWindow)
		if err != nil {
			return vocab.DNSResolution{}, err
		}
		if found {
			clientID = &id
		}
		// A lookup from an address no machine is known at is NOT given a machine of its own here.
		// Minting a level-3 identity from a resolver row would create a machine on no interface,
		// and the filter-log collector is the place that knows which interface an address was
		// seen behind.
	}

	lookup := record
	lookup.ClientAddress, lookup.ClientHostname, lookup.ClientResolution = clientAddress, clientHostname, resolution
	lookup.ClientID, lookup.Resolver, lookup.IngestedAt = clientID, resolver, now
	return lookup, nil
}

// reportedLookup is a checked dns_lookup record as a row holding what the resolver reported;
// buildDNSResolution adds what the core derives.
func reportedLookup(record provider.Answer) vocab.DNSResolution {
	return vocab.DNSResolution{
		LookupKey: provider.Text(record, "deduplication_key"), ClientAddress: provider.Text(record, "client_address"),
		ClientFamily: provider.IntegerPointer(record, "client_family"), Domain: provider.Text(record, "domain"),
		Action: provider.Text(record, "action"), AnswerSource: provider.TextPointer(record, "answer_source"),
		Rcode: provider.TextPointer(record, "rcode"), DNSSECStatus: provider.TextPointer(record, "dnssec_status"),
		BlocklistName: provider.Text(record, "blocklist_name"), LookedUpAt: provider.Integer(record, "looked_up_at"),
	}
}

// Kind is the kind this coordinator collects.
const Kind = "dns_lookup"
