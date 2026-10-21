package resolverrecords

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/store/attribution"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The resolver's records, read for two kinds: resolver_cache and resolver_local_data.
//
// THIS FILE SPANS THE TWO KINDS, which is why it is named for neither. Both read a list of
// DNS resource records -- an owner name, a type, a value, and for the cache the seconds
// each has left -- and what is true of a resource record whoever returned it is DNS's,
// not a product's: names compare without regard to case and an absolute name ends in a
// dot (RFC 4343; RFC 1034, section 3.1), an A record holds an IPv4 address and an AAAA
// record an IPv6 one (RFC 1035, section 3.4.1; RFC 3596, section 2.2), and a PTR record's
// owner encodes the address it names (RFC 1035, section 3.5; RFC 3596, section 2.5). So
// the normalisation, the counting, the storing and the derivation that follows are written
// here once; which endpoint a resolver answers on, and what its envelope looks like, is in
// that resolver's own file.
//
// AN ABSENCE IS NEVER STORED AS AN EMPTY READ. A read that was refused, not found, could
// not be decoded, or answered a failed status stores no observation and does not move the
// provider's last successful poll: the next successful poll's records are then covered
// from the last poll that did succeed, which is what the coverage interval promises.

// recordTally is what one read returned, counted.
type recordTally struct {
	// total is every record the endpoint returned. Rows its implementation recognised as
	// not being records are not among them.
	total int
	// notRecords are those rows, counted and worded by the implementation that read them.
	notRecords []provider.Answer
	// byType counts every record by its `rrtype`, verbatim, stored or not.
	byType map[string]int
	// stored is how many records passed normalisation and were handed to the store.
	stored int
	// badTTL counts the cache records whose `ttl` is not a string of digits: a string
	// holding anything else, a number, an absent field.
	badTTL int
	// badValues counts the records whose `value` is not valid for their type, per named
	// category (SPEC-step-5b-widget-api.md, B6): a single undifferentiated count told
	// nobody whether it was a defect of the reading or something the resolver holds.
	badValues map[badValue]int
	// badName counts the records with no owner name.
	badName int
}

// badValue is one named way a record's value is not valid for its type. The categories
// are DNS's rules restated per type (RFC 1035, sections 3.3.1, 3.4.1 and 3.5; RFC 3596,
// sections 2.2 and 2.5), not a product's.
type badValue string

// The categories, in the order the detail lists them.
const (
	badValueANotAddress    badValue = "A whose value is not an address"
	badValueAIsIPv6        badValue = "A whose value is an IPv6 address"
	badValueAAAANotAddress badValue = "AAAA whose value is not an address"
	badValueAAAAIsIPv4     badValue = "AAAA whose value is an IPv4 address"
	badValueCNAMEEmpty     badValue = "CNAME with an empty target"
	badValuePTREmpty       badValue = "PTR with an empty target"
	badValuePTRNoAddress   badValue = "PTR whose owner name encodes no address"
	badValueNoHostLabel    badValue = "local data whose host name has an empty first label"
)

// badValueOrder is the order the categories are listed in.
var badValueOrder = []badValue{badValueANotAddress, badValueAIsIPv6, badValueAAAANotAddress,
	badValueAAAAIsIPv4, badValueCNAMEEmpty, badValuePTREmpty, badValuePTRNoAddress,
	badValueNoHostLabel}

// badValueTotal is every record skipped for its value.
func (t recordTally) badValueTotal() int {
	total := 0
	for _, n := range t.badValues {
		total += n
	}
	return total
}

// addressValue classifies an A or AAAA record's value: the address, or the category it
// fails under.
func addressValue(rrtype, value string) (netip.Addr, badValue) {
	address, ok := attribution.CanonicalAddress(value)
	switch {
	case !ok && rrtype == attribution.RRTypeA:
		return netip.Addr{}, badValueANotAddress
	case !ok:
		return netip.Addr{}, badValueAAAANotAddress
	case rrtype == attribution.RRTypeA && !address.Is4():
		return netip.Addr{}, badValueAIsIPv6
	case rrtype == attribution.RRTypeAAAA && address.Is4():
		return netip.Addr{}, badValueAAAAIsIPv4
	}
	return address, ""
}

// storedTypes are the record types each part of the resolver contributes: the cache's
// addresses and the CNAMEs that lead to them, and the local data's addresses with the
// PTR records that name hosts.
func storedTypes(heldIn string) []string {
	if heldIn == attribution.HeldInCache {
		return []string{attribution.RRTypeA, attribution.RRTypeAAAA, attribution.RRTypeCNAME}
	}
	return []string{attribution.RRTypeA, attribution.RRTypeAAAA, attribution.RRTypePTR}
}

// normaliseRecords turns what an endpoint returned into the records the store keeps, and
// counts what it did not keep and why.
func normaliseRecords(raw []provider.Answer, heldIn string) ([]attribution.ResourceRecord, recordTally) {
	tally := recordTally{byType: map[string]int{}, badValues: map[badValue]int{}}
	kept := map[string]bool{}
	for _, rrtype := range storedTypes(heldIn) {
		kept[rrtype] = true
	}
	records := make([]attribution.ResourceRecord, 0, len(raw))
	for _, record := range raw {
		tally.total++
		rrtype := strings.TrimSpace(provider.Text(record, "rrtype"))
		tally.byType[rrtype]++
		if !kept[rrtype] {
			continue
		}
		owner := attribution.DNSName(provider.Text(record, "owner_name"))
		if owner == "" {
			tally.badName++
			continue
		}
		normalised := attribution.ResourceRecord{OwnerName: owner, RRType: rrtype}
		if heldIn == attribution.HeldInCache {
			// The cache's ttl is the seconds the record has left at the dump's instant, a
			// string of digits; 0 is valid. Anything else -- a string holding something
			// other than digits, a negative value, a number rather than a string, no ttl
			// at all -- bounds nothing, so the record is not stored.
			seconds, ok := ttlSeconds(record)
			if !ok {
				tally.badTTL++
				continue
			}
			normalised.TTLSeconds = seconds
		}
		switch rrtype {
		case attribution.RRTypeA, attribution.RRTypeAAAA:
			address, bad := addressValue(rrtype, provider.Text(record, "value"))
			if bad != "" {
				tally.badValues[bad]++
				continue
			}
			normalised.Value = address.String()
			normalised.Address = address.String()
			if heldIn == attribution.HeldInLocalData {
				normalised.HostLabel = attribution.HostnameLabel(owner)
			}
		case attribution.RRTypeCNAME:
			target := attribution.DNSName(provider.Text(record, "value"))
			if target == "" {
				tally.badValues[badValueCNAMEEmpty]++
				continue
			}
			normalised.Value = target
		case attribution.RRTypePTR:
			target := attribution.DNSName(provider.Text(record, "value"))
			address, ok := reverseNameAddress(owner)
			if !ok {
				tally.badValues[badValuePTRNoAddress]++
				continue
			}
			if target == "" {
				tally.badValues[badValuePTREmpty]++
				continue
			}
			normalised.Value = target
			normalised.Address = address.String()
			normalised.HostLabel = attribution.HostnameLabel(target)
		}
		if normalised.HostLabel == "" && heldIn == attribution.HeldInLocalData {
			tally.badValues[badValueNoHostLabel]++
			continue
		}
		records = append(records, normalised)
		tally.stored++
	}
	return records, tally
}

// ttlSeconds reads a cache record's ttl: a JSON string of ASCII digits, surrounding white
// space aside.
func ttlSeconds(record provider.Answer) (int64, bool) {
	// The JSON shape the ttl arrived in, as the implementation named it: only a string carries seconds.
	if provider.Text(record, "ttl_shape") != "string" {
		return 0, false
	}
	text := strings.TrimSpace(provider.Text(record, "ttl"))
	if text == "" {
		return 0, false
	}
	for _, digit := range text {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	seconds, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, false
	}
	return seconds, true
}

// reverseNameAddress decodes the address a reverse-mapping owner name encodes: four
// decimal labels under in-addr.arpa, least significant first (RFC 1035, section 3.5), or
// thirty-two hexadecimal nibble labels under ip6.arpa, least significant first (RFC 3596,
// section 2.5). Any other name encodes no address.
func reverseNameAddress(name string) (netip.Addr, bool) {
	switch {
	case strings.HasSuffix(name, ".in-addr.arpa"):
		labels := strings.Split(strings.TrimSuffix(name, ".in-addr.arpa"), ".")
		if len(labels) != 4 {
			return netip.Addr{}, false
		}
		var octets [4]byte
		for index, label := range labels {
			value, err := strconv.ParseUint(label, 10, 8)
			if err != nil || (len(label) > 1 && label[0] == '0') {
				return netip.Addr{}, false
			}
			octets[3-index] = byte(value)
		}
		return netip.AddrFrom4(octets), true
	case strings.HasSuffix(name, ".ip6.arpa"):
		labels := strings.Split(strings.TrimSuffix(name, ".ip6.arpa"), ".")
		if len(labels) != 32 {
			return netip.Addr{}, false
		}
		var raw [16]byte
		for index, label := range labels {
			if len(label) != 1 {
				return netip.Addr{}, false
			}
			nibble, err := strconv.ParseUint(label, 16, 8)
			if err != nil {
				return netip.Addr{}, false
			}
			position := 31 - index
			if position%2 == 0 {
				raw[position/2] |= byte(nibble) << 4
			} else {
				raw[position/2] |= byte(nibble)
			}
		}
		return netip.AddrFrom16(raw), true
	}
	return netip.Addr{}, false
}

// detail is the availability detail of one successful read: what it returned, by type, its
// size and its time, what was stored, and what was skipped and why.
func (t recordTally) detail(heldIn string, responseBytes int, elapsed time.Duration,
	storage attribution.RecordStorage) string {
	type count struct {
		rrtype string
		n      int
	}
	counts := make([]count, 0, len(t.byType))
	for rrtype, n := range t.byType {
		counts = append(counts, count{rrtype, n})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].n != counts[j].n {
			return counts[i].n > counts[j].n
		}
		return counts[i].rrtype < counts[j].rrtype
	})
	parts := make([]string, 0, len(counts))
	for _, c := range counts {
		label := c.rrtype
		if label == "" {
			label = "no type"
		}
		parts = append(parts, fmt.Sprintf("%s %d", label, c.n))
	}
	source := "the cache"
	if heldIn == attribution.HeldInLocalData {
		source = "the local data"
	}
	detail := fmt.Sprintf("read %d records of %s (%d bytes) in %d ms", t.total, source,
		responseBytes, elapsed.Milliseconds())
	if len(parts) > 0 {
		detail += ": " + strings.Join(parts, ", ")
	}
	detail += fmt.Sprintf("; kept %d of the %s records (%d observations started, %d extended); "+
		"every other type is counted and not stored", t.stored,
		strings.Join(storedTypes(heldIn), ", "), storage.Inserted, storage.Extended)
	for _, rows := range t.notRecords {
		if count := provider.Integer(rows, "count"); count > 0 {
			detail += fmt.Sprintf("; skipped %d %s", count, provider.Text(rows, "wording"))
		}
	}
	if t.badTTL > 0 {
		detail += fmt.Sprintf("; skipped %d whose ttl is not a whole number of seconds", t.badTTL)
	}
	if total := t.badValueTotal(); total > 0 {
		categories := make([]string, 0, len(t.badValues))
		for _, category := range badValueOrder {
			if n := t.badValues[category]; n > 0 {
				categories = append(categories, fmt.Sprintf("%s: %d", category, n))
			}
		}
		detail += fmt.Sprintf("; skipped %d whose value is not valid for their type (%s)", total,
			strings.Join(categories, ", "))
	}
	if t.badName > 0 {
		detail += fmt.Sprintf("; skipped %d with no owner name", t.badName)
	}
	return detail
}

// recordPass is what one pass over a kind's active sources did.
type recordPass struct {
	// read says whether at least one source was read successfully, and readAt the
	// instant its read began.
	read   bool
	readAt int64
	// followsAPoll says whether a source read successfully had been read successfully
	// before. A provider's FIRST poll ever can vouch for no instant before itself -- its
	// records are covered from its own instant -- so it cannot yet speak for a lookup
	// made earlier.
	followsAPoll bool
	// failed says whether a source failed or could not be read by design.
	failed bool
}

// readResourceRecords runs the reading half of one pass of a resolver-record kind: every
// active source is read, its records counted and stored, and its availability written. It
// returns what the pass did and the derivation the stored records call for -- the flows
// whose attribution they can change -- which the caller runs, holding the purge back
// until it has (purge.go).
func (c *ResolverRecordCoordinator) readResourceRecords(ctx context.Context, kind, function, heldIn string) (
	recordPass, derive.Derivation, error) {
	var (
		pass    recordPass
		request derive.Derivation
	)
	sources, err := c.ActiveSources(ctx, kind)
	if err != nil {
		return pass, request, err
	}
	var failures []error
	for _, active := range sources {
		source, registered := c.SourceSet.Lookup(kind, active.ProviderKey)
		if !registered {
			pass.failed = true
			failures = append(failures, fmt.Errorf("collect: the active %s provider %q has no "+
				"implementation", kind, active.ProviderKey))
			continue
		}
		polledAt := c.Now()
		started := c.Clock.Now()
		dump, result, readErr := source.Implementation.Call(ctx, c, function)
		elapsed := c.Clock.Now().Sub(started)
		if errors.Is(readErr, provider.ErrUnsupportedRead) {
			// Registered, probed, and not readable through the API: a state with its
			// reason, not a failed pass. Nothing is stored.
			pass.failed = true
			c.SetReadDetail(active.ProviderID, result.Outcome, readErr.Error())
			if err := c.WriteAvailability(ctx, active.ProviderID, result.State, result.Outcome,
				result.Probe, readErr.Error()); err != nil {
				failures = append(failures, err)
			}
			continue
		}
		if readErr != nil {
			// The implementation's own wording of the absence: refused, not found,
			// undecodable, a failed status. Nothing is stored, and the provider's last
			// successful poll does not move.
			pass.failed = true
			state, outcome := result.State, result.Outcome
			if state == "" {
				state = vocab.StateUnavailable
			}
			c.SetReadDetail(active.ProviderID, outcome, result.Detail)
			if err := c.WriteAvailability(ctx, active.ProviderID, state, outcome, result.Probe,
				result.Detail); err != nil {
				failures = append(failures, err)
			}
			failures = append(failures, readErr)
			continue
		}

		records, tally := normaliseRecords(provider.Records(dump, "records"), heldIn)
		tally.notRecords = provider.Records(dump, "not_records")
		storage, err := c.Store.StoreResourceRecords(ctx, attribution.ResourceRecordRead{
			ProviderID: active.ProviderID, HeldIn: heldIn, PolledAt: polledAt, Records: records,
		})
		if err != nil {
			// The resolver answered and opnview kept nothing of it: unavailable, because
			// nothing of this read reached the store a widget reads, with the outcome saying
			// the fault is opnview's rather than the firewall's -- as the lease pass records
			// it (SPEC-step-5b-widget-api.md, B8g). The provider's last successful poll does
			// not move, so no lookup is vouched for by a read that was not kept.
			pass.failed = true
			detail := fmt.Sprintf("the resolver answered and its %d records could not be stored: %v",
				len(records), err)
			c.SetReadDetail(active.ProviderID, vocab.OutcomeStoreFailure, detail)
			if writeErr := c.WriteAvailability(ctx, active.ProviderID, vocab.StateUnavailable,
				vocab.OutcomeStoreFailure, result.Probe, detail); writeErr != nil {
				failures = append(failures, writeErr)
			}
			failures = append(failures, err)
			continue
		}
		if !pass.read || polledAt < pass.readAt {
			pass.readAt = polledAt
		}
		pass.read = true
		pass.followsAPoll = pass.followsAPoll || storage.HadPrevious
		detail := tally.detail(heldIn, int(provider.Integer(dump, "response_bytes")), elapsed, storage)
		outcome := vocab.OutcomeHealthy
		if tally.total == 0 {
			outcome = vocab.OutcomeHealthyEmpty
		}
		c.SetReadDetail(active.ProviderID, outcome, detail)
		if err := c.WriteAvailability(ctx, active.ProviderID, vocab.StateReachable, outcome,
			result.Probe, detail); err != nil {
			failures = append(failures, err)
		}
		if storage.Changed {
			// LATE ARRIVAL. What the resolver held changed over this interval, so every
			// flow observed inside it -- and, through a timing candidate's contradicting
			// evidence, up to the timing delay after it -- has its attribution decided
			// again. Flows observed later are decided when they are stored.
			request.Widen(storage.ChangedFrom, storage.ChangedTo+derive.On(c.Collector).AttributionMaxDelay())
		}
	}

	if len(failures) > 0 {
		return pass, request, fmt.Errorf("collect: the %s pass was incomplete: %w", kind,
			core.JoinErrors(failures))
	}
	return pass, request, nil
}

// deriveRecordPass runs the derivation a resolver-record pass asked for, if it asked for
// any, and returns the pass's own error joined with the derivation's.
func (c *ResolverRecordCoordinator) deriveRecordPass(ctx context.Context, request derive.Derivation, passErr error) error {
	if !request.Attribute && !request.Hostnames {
		return passErr
	}
	deriveErr := derive.On(c.Collector).Derive(ctx, request)
	switch {
	case passErr == nil:
		return deriveErr
	case deriveErr == nil:
		return passErr
	}
	return core.JoinErrors([]error{passErr, deriveErr})
}
