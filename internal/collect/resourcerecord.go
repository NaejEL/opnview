package collect

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/store"
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

// recordReader is one implementation's read of its records.
type recordReader func(ctx context.Context, host session) (recordDump, probeResult, error)

// recordTally is what one read returned, counted.
type recordTally struct {
	// total is every record the endpoint returned, the message-cache references aside.
	total int
	// messageCacheRefs counts the cache rows whose `ttl` is JSON null: references from
	// the dump's message-cache section, which are not records (resolvercache_unbound.go).
	messageCacheRefs int
	// byType counts every record by its `rrtype`, verbatim, stored or not.
	byType map[string]int
	// stored is how many records passed normalisation and were handed to the store.
	stored int
	// badTTL counts the cache records whose `ttl` is not a string of digits: a string
	// holding anything else, a number, an absent field. A null ttl is not counted here.
	badTTL int
	// badValue counts the records whose `value` is not valid for their type -- an A
	// record that is not an IPv4 address, a CNAME with no target -- and the PTR records
	// whose owner name encodes no address.
	badValue int
	// badName counts the records with no owner name.
	badName int
}

// storedTypes are the record types each part of the resolver contributes: the cache's
// addresses and the CNAMEs that lead to them, and the local data's addresses with the
// PTR records that name hosts.
func storedTypes(heldIn string) []string {
	if heldIn == store.HeldInCache {
		return []string{store.RRTypeA, store.RRTypeAAAA, store.RRTypeCNAME}
	}
	return []string{store.RRTypeA, store.RRTypeAAAA, store.RRTypePTR}
}

// normaliseRecords turns what an endpoint returned into the records the store keeps, and
// counts what it did not keep and why.
func normaliseRecords(raw []rawRecord, heldIn string) ([]store.ResourceRecord, recordTally) {
	tally := recordTally{byType: map[string]int{}}
	kept := map[string]bool{}
	for _, rrtype := range storedTypes(heldIn) {
		kept[rrtype] = true
	}
	records := make([]store.ResourceRecord, 0, len(raw))
	for _, record := range raw {
		if heldIn == store.HeldInCache && record.TTLShape == decode.ShapeNull {
			// A message-cache reference: it names an rrset the dump's rrset section
			// already holds, and carries no TTL and no record data. Not a record, so not
			// counted among them, and not an anomaly.
			tally.messageCacheRefs++
			continue
		}
		tally.total++
		rrtype := strings.TrimSpace(record.RRType)
		tally.byType[rrtype]++
		if !kept[rrtype] {
			continue
		}
		owner := store.DNSName(record.OwnerName)
		if owner == "" {
			tally.badName++
			continue
		}
		normalised := store.ResourceRecord{OwnerName: owner, RRType: rrtype}
		if heldIn == store.HeldInCache {
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
		case store.RRTypeA, store.RRTypeAAAA:
			address, ok := store.CanonicalAddress(record.Value)
			if !ok || (rrtype == store.RRTypeA) != address.Is4() {
				tally.badValue++
				continue
			}
			normalised.Value = address.String()
			normalised.Address = address.String()
			if heldIn == store.HeldInLocalData {
				normalised.HostLabel = store.HostnameLabel(owner)
			}
		case store.RRTypeCNAME:
			target := store.DNSName(record.Value)
			if target == "" {
				tally.badValue++
				continue
			}
			normalised.Value = target
		case store.RRTypePTR:
			target := store.DNSName(record.Value)
			address, ok := reverseNameAddress(owner)
			if target == "" || !ok {
				tally.badValue++
				continue
			}
			normalised.Value = target
			normalised.Address = address.String()
			normalised.HostLabel = store.HostnameLabel(target)
		}
		if normalised.HostLabel == "" && heldIn == store.HeldInLocalData {
			tally.badValue++
			continue
		}
		records = append(records, normalised)
		tally.stored++
	}
	return records, tally
}

// ttlSeconds reads a cache record's ttl: a JSON string of ASCII digits, surrounding white
// space aside.
func ttlSeconds(record rawRecord) (int64, bool) {
	if record.TTLShape != decode.ShapeString {
		return 0, false
	}
	text := strings.TrimSpace(record.TTL)
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
	storage store.RecordStorage) string {
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
	if heldIn == store.HeldInLocalData {
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
	if t.messageCacheRefs > 0 {
		detail += fmt.Sprintf("; skipped %d message-cache references: lines of the dump's message-cache "+
			"section naming an rrset of a cached answer, with no ttl and no record data, so not records "+
			"and not counted among them", t.messageCacheRefs)
	}
	if t.badTTL > 0 {
		detail += fmt.Sprintf("; skipped %d whose ttl is not a whole number of seconds", t.badTTL)
	}
	if t.badValue > 0 {
		detail += fmt.Sprintf("; skipped %d whose value is not valid for their type", t.badValue)
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
func (c *Collector) readResourceRecords(ctx context.Context, kind, heldIn string,
	readerOf func(providerKey string) (recordReader, bool)) (recordPass, derivation, error) {
	var (
		pass    recordPass
		request derivation
	)
	sources, err := c.activeSources(ctx, kind)
	if err != nil {
		return pass, request, err
	}
	var failures []error
	for _, active := range sources {
		reader, registered := readerOf(active.providerKey)
		if !registered {
			pass.failed = true
			failures = append(failures, fmt.Errorf("collect: the active %s provider %q has no "+
				"implementation", kind, active.providerKey))
			continue
		}
		polledAt := c.now()
		started := c.clock.Now()
		dump, result, readErr := reader(ctx, c)
		elapsed := c.clock.Now().Sub(started)
		if errors.Is(readErr, ErrUnsupportedRead) {
			// Registered, probed, and not readable through the API: a state with its
			// reason, not a failed pass. Nothing is stored.
			pass.failed = true
			c.setReadDetail(active.providerID, readErr.Error())
			if err := c.writeAvailability(ctx, active.providerID, result.state, result.probe,
				readErr.Error()); err != nil {
				failures = append(failures, err)
			}
			continue
		}
		if readErr != nil {
			// The implementation's own wording of the absence: refused, not found,
			// undecodable, a failed status. Nothing is stored, and the provider's last
			// successful poll does not move.
			pass.failed = true
			state := result.state
			if state == "" {
				state = store.StateUnavailable
			}
			c.setReadDetail(active.providerID, result.detail)
			if err := c.writeAvailability(ctx, active.providerID, state, result.probe,
				result.detail); err != nil {
				failures = append(failures, err)
			}
			failures = append(failures, readErr)
			continue
		}

		records, tally := normaliseRecords(dump.Records, heldIn)
		storage, err := c.store.StoreResourceRecords(ctx, store.ResourceRecordRead{
			ProviderID: active.providerID, HeldIn: heldIn, PolledAt: polledAt, Records: records,
		})
		if err != nil {
			pass.failed = true
			failures = append(failures, err)
			continue
		}
		if !pass.read || polledAt < pass.readAt {
			pass.readAt = polledAt
		}
		pass.read = true
		pass.followsAPoll = pass.followsAPoll || storage.HadPrevious
		detail := tally.detail(heldIn, dump.ResponseBytes, elapsed, storage)
		c.setReadDetail(active.providerID, detail)
		if err := c.writeAvailability(ctx, active.providerID, store.StateReachable, result.probe,
			detail); err != nil {
			failures = append(failures, err)
		}
		if storage.Changed {
			// LATE ARRIVAL. What the resolver held changed over this interval, so every
			// flow observed inside it -- and, through a timing candidate's contradicting
			// evidence, up to the timing delay after it -- has its attribution decided
			// again. Flows observed later are decided when they are stored.
			request.widen(storage.ChangedFrom, storage.ChangedTo+c.attributionMaxDelay())
		}
	}

	if len(failures) > 0 {
		return pass, request, fmt.Errorf("collect: the %s pass was incomplete: %w", kind,
			joinErrors(failures))
	}
	return pass, request, nil
}

// deriveRecordPass runs the derivation a resolver-record pass asked for, if it asked for
// any, and returns the pass's own error joined with the derivation's.
func (c *Collector) deriveRecordPass(ctx context.Context, request derivation, passErr error) error {
	if !request.attribute && len(request.addresses) == 0 && !request.hostnames {
		return passErr
	}
	deriveErr := c.derive(ctx, request)
	switch {
	case passErr == nil:
		return deriveErr
	case deriveErr == nil:
		return passErr
	}
	return joinErrors([]error{passErr, deriveErr})
}
