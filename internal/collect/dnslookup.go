package collect

import (
	"context"
	"errors"
	"fmt"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/store"
)

// The dns_lookup kind.
//
// What is true of any source of resolver lookups, and is therefore here: resolving the
// querying machine, resolving the list that refused a lookup, walking the pages, and detecting
// a gap. Which resolver, how its window behaves, and what can serve as a de-duplication key
// are in dnslookup_unbound.go and dnslookup_dnsmasq.go, because all three are facts about a
// product.
//
// THE KIND NEVER PRESENTS WHAT IT READ AS COVERAGE OF A WINDOW. The one implementation that
// can be read is a ring buffer of the most recent lookups whose time bounds are ignored
// outright, measured on a live firewall, so there is nothing here that returns, stores or
// reports a covered interval — and the availability detail says in words what was read and
// what it is not.

// CollectDNSLookup runs one pass of the active implementation of the dns_lookup kind.
func (c *Collector) CollectDNSLookup(ctx context.Context) error {
	providerKey, providerID, active, err := c.activeSourceKey(ctx, KindDNSLookup)
	if err != nil {
		return err
	}
	if !active {
		// Either the resolver's query reporting is off, or the only other implementation is one
		// whose log grammar is not recorded. Both are states the probe round wrote down, and
		// neither is an absence of lookups.
		return nil
	}
	source, registered := lookupSources[providerKey]
	if !registered {
		return fmt.Errorf("collect: the active dns_lookup provider %q has no implementation",
			providerKey)
	}

	newestStored, hasStored, err := c.store.NewestDNSResolutionAt(ctx)
	if err != nil {
		return err
	}

	now := c.now()
	span := source.requestedSpanSeconds()
	windowStart := now - span

	var (
		lastResult             probeResult
		oldestSeen             int64
		haveOldest             bool
		outsideRequestedWindow int
		totalRows              int
	)

	for page := 1; page <= source.pageBound(); page++ {
		records, result, readErr := source.lookups(ctx, c, page)
		lastResult = result
		if errors.Is(readErr, ErrUnsupportedRead) {
			// Registered, probed, and not readable. A state with its reason, not a failed pass.
			return c.writeAvailability(ctx, providerID, result.state, result.probe, readErr.Error())
		}
		if readErr != nil {
			if writeErr := c.writeAvailability(ctx, providerID, result.state,
				result.probe, result.detail); writeErr != nil {
				return writeErr
			}
			return readErr
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
			if hasStored && record.LookedUpAt <= newestStored {
				// Already inside the stored history. The row may still be a different lookup at
				// the same instant, so the insert is what decides; what this does not do is stop
				// the page, because the buffer is not ordered by anything opnview controls.
				continue
			}
			newOnThisPage++

			lookup, err := c.buildDNSResolution(ctx, record, providerKey, now)
			if err != nil {
				return err
			}
			if err := c.store.InsertDNSResolution(ctx, lookup); err != nil {
				return err
			}
		}
		if newOnThisPage == 0 {
			// Every row on this page was already inside the stored history, so the pages behind
			// it are older still.
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
	if err := c.writeAvailability(ctx, providerID, store.StateReachable,
		lastResult.probe, detail); err != nil {
		return err
	}

	// The gap test, and the only one this kind's material permits: the buffer holds the most
	// recent lookups, so if its OLDEST row is newer than the newest row stored, the lookups
	// between the two rotated out before opnview read them.
	if hasStored && haveOldest && oldestSeen > newestStored {
		gapDetail := fmt.Sprintf(
			"the oldest of the %d lookups read is newer than the newest lookup stored, so the "+
				"interval between them rotated out of the endpoint's ring buffer before it was "+
				"read; the endpoint honours no window, so nothing can recover it", totalRows)
		if err := c.store.RecordCollectionGap(ctx, store.CollectionGap{
			ProviderID:      providerID,
			IntervalStartAt: newestStored,
			IntervalEndAt:   oldestSeen,
			Reason:          store.GapResolverWindowNotHonoured,
			Detail:          &gapDetail,
			DetectedAt:      now,
		}); err != nil {
			return err
		}
	}
	return nil
}

// buildDNSResolution turns one record into a row, resolving the querying machine.
func (c *Collector) buildDNSResolution(ctx context.Context, record lookupRecord,
	resolver string, now int64) (store.DNSResolution, error) {
	var clientID *int64
	if record.ClientAddress != "" {
		id, _, found, err := c.store.ClientRefByAddressSince(ctx, record.ClientAddress,
			now-AddressIdleWindow)
		if err != nil {
			return store.DNSResolution{}, err
		}
		if found {
			clientID = &id
		}
		// A lookup from an address no machine is known at is NOT given a machine of its own here.
		// Minting a level-3 identity from a resolver row would create a machine on no interface,
		// and the filter-log collector is the place that knows which interface an address was
		// seen behind.
	}

	return store.DNSResolution{
		LookupKey:     record.DeduplicationKey,
		ClientAddress: record.ClientAddress,
		ClientID:      clientID,
		Domain:        record.Domain,
		Resolver:      resolver,
		Action:        record.Action,
		AnswerSource:  record.AnswerSource,
		Rcode:         record.Rcode,
		DNSSECStatus:  record.DNSSECStatus,
		BlocklistName: record.BlocklistName,
		LookedUpAt:    record.LookedUpAt,
		IngestedAt:    now,
	}, nil
}

// normaliseLookupAction maps a resolver's action onto the closed vocabulary the schema
// constrains. Anything else is `unknown`: a lookup whose verdict opnview cannot name must not
// be counted as allowed.
func normaliseLookupAction(raw string) string {
	switch decode.LowerASCII(raw) {
	case "pass", "passed", "allowed":
		return "pass"
	case "block", "blocked":
		return "block"
	case "drop", "dropped":
		return "drop"
	default:
		return "unknown"
	}
}
