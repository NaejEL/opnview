package netflow

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/push/ipfixregistry"
)

// The decoder: packet headers, template and options template sets, data sets, the records that
// arrive before their template, and the sequence numbers. Templates are scoped by exporter address
// and observation domain (the v9 source ID), as RFC 3954 section 5.1 and RFC 7011 section 8 scope
// them; one listener's decoder never shares one with another's.

// The protocol versions in a packet header's first two octets.
const (
	versionV5    = 5
	versionV9    = 9
	versionIPFIX = 10
)

// The set ids that carry templates.
const (
	v9TemplateSet         = 0
	v9OptionsTemplateSet  = 1
	ipfixTemplateSet      = 2
	ipfixOptionsTemplate  = 3
	firstDataSetID        = 256
	variableLength        = 65535
	enterpriseBit         = 0x8000
	v9HeaderLength        = 20
	ipfixHeaderLength     = 16
	setHeaderLength       = 4
	sequenceForwardWindow = 1 << 31
)

// The counters and gap reasons of the NetFlow decoder (docs/plugins.md, "Push sources").
const (
	ReasonVersionUnsupported = "netflow_version_unsupported"
	ReasonTemplateMissing    = "netflow_template_missing"
	ReasonSequenceGap        = "netflow_sequence_gap"
	ReasonPacketMalformed    = "netflow_packet_malformed"
	ReasonTemplateLimit      = "netflow_template_limit"
	// FlagSequenceInterleaved names an exporter and domain whose sequence numbers went backwards:
	// several counters share one observation domain (OPNsense's per-interface nodes behind one
	// forwarder do, survey S11), so no gap is asserted for it from then on.
	FlagSequenceInterleaved = "netflow_sequence_interleaved"
)

// field is one field of a template.
type field struct {
	id         uint16
	enterprise uint32
	length     uint16
	name       string
	abstract   string
}

// template is one template or options template.
type template struct {
	id         uint16
	version    int
	fields     []field
	scopeCount int
	options    bool
	// generation changes when the template is redefined, so a route key never outlives it.
	generation int
	minLength  int
}

// scope is an exporter address and an observation domain.
type scope struct {
	exporter netip.Addr
	domain   uint32
}

// heldSet is a data set waiting for its template.
type heldSet struct {
	templateID uint16
	// set is the set's ordinal in its datagram, which the records released from it keep.
	set     int
	payload []byte
	header  packetHeader
	meta    push.Meta
}

// sequenceState follows one scope's sequence numbers.
type sequenceState struct {
	known       bool
	expected    uint32
	lastExport  time.Time
	interleaved bool
	// pending is a forward jump not yet confirmed by a following packet.
	pending *pendingGap
}

type pendingGap struct {
	from, to   time.Time
	missing    uint32
	nextExpect uint32
	detectedAt time.Time
}

// scopeState is everything held for one scope.
type scopeState struct {
	// name is the scope as a Problem names it: "<exporter>/<domain>".
	name      string
	templates map[uint16]*template
	held      []heldSet
	sequence  sequenceState
}

// packetHeader is what a packet header says that records need.
type packetHeader struct {
	version    int
	exportTime time.Time
	sysUptime  uint32
	sequence   uint32
	domain     uint32
	// delivery is the SHA-256 of the whole datagram, in hexadecimal: every record of it carries it.
	delivery string
}

type decoder struct {
	mutex      sync.Mutex
	limits     push.Limits
	scopes     map[scope]*scopeState
	seen       map[string]int64
	generation int
	flags      map[string]bool
}

func newDecoder(limits push.Limits) *decoder {
	return &decoder{limits: limits, scopes: map[scope]*scopeState{}, seen: map[string]int64{},
		flags: map[string]bool{}}
}

func (d *decoder) scopeOf(key scope) *scopeState {
	state, found := d.scopes[key]
	if !found {
		state = &scopeState{name: fmt.Sprintf("%s/%d", key.exporter, key.domain), templates: map[uint16]*template{}}
		d.scopes[key] = state
	}
	return state
}

func (d *decoder) Decode(payload []byte, meta push.Meta) push.Decoded {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if len(payload) < 2 {
		return malformed("a datagram shorter than a version number")
	}
	switch version := int(binary.BigEndian.Uint16(payload)); version {
	case versionV9:
		return d.decodeV9(payload, meta)
	case versionIPFIX:
		return d.decodeIPFIX(payload, meta)
	default:
		return push.Decoded{Problems: []push.Problem{{Reason: ReasonVersionUnsupported, Count: 1,
			Detail: fmt.Sprintf("a NetFlow version %d datagram was counted and not decoded: only version 9 and "+
				"IPFIX (10) are", version)}}}
	}
}

func malformed(detail string) push.Decoded {
	return push.Decoded{Problems: []push.Problem{{Reason: ReasonPacketMalformed, Count: 1, Detail: detail}}}
}

// decodeV9 reads a NetFlow v9 packet (RFC 3954, section 5.1): a 20-octet header, then FlowSets.
func (d *decoder) decodeV9(payload []byte, meta push.Meta) push.Decoded {
	if len(payload) < v9HeaderLength {
		return malformed("a v9 datagram shorter than its header")
	}
	header := packetHeader{
		version:    versionV9,
		sysUptime:  binary.BigEndian.Uint32(payload[4:8]),
		exportTime: time.Unix(int64(binary.BigEndian.Uint32(payload[8:12])), 0).UTC(),
		sequence:   binary.BigEndian.Uint32(payload[12:16]),
		domain:     binary.BigEndian.Uint32(payload[16:20]),
		delivery:   digestOf(payload),
	}
	key := scope{exporter: meta.Sender, domain: header.domain}
	state := d.scopeOf(key)
	var decoded push.Decoded
	_, _, problem := d.walkSets(payload[v9HeaderLength:], header, meta, state, &decoded)
	if problem != "" {
		decoded.Problems = append(decoded.Problems, push.Problem{Reason: ReasonPacketMalformed, Count: 1,
			Detail: problem})
	}
	// A v9 sequence number counts export packets (RFC 3954, section 5.1), so a held set changes nothing.
	d.followSequence(state, header, 1, false, meta, &decoded)
	return decoded
}

// decodeIPFIX reads an IPFIX message (RFC 7011, section 3.1): a 16-octet header, then Sets.
func (d *decoder) decodeIPFIX(payload []byte, meta push.Meta) push.Decoded {
	if len(payload) < ipfixHeaderLength {
		return malformed("an IPFIX message shorter than its header")
	}
	length := int(binary.BigEndian.Uint16(payload[2:4]))
	if length < ipfixHeaderLength || length > len(payload) {
		return malformed("an IPFIX message whose length field disagrees with the datagram")
	}
	payload = payload[:length]
	header := packetHeader{
		version:    versionIPFIX,
		exportTime: time.Unix(int64(binary.BigEndian.Uint32(payload[4:8])), 0).UTC(),
		sequence:   binary.BigEndian.Uint32(payload[8:12]),
		domain:     binary.BigEndian.Uint32(payload[12:16]),
		delivery:   digestOf(payload),
	}
	state := d.scopeOf(scope{exporter: meta.Sender, domain: header.domain})
	var decoded push.Decoded
	dataRecords, held, problem := d.walkSets(payload[ipfixHeaderLength:], header, meta, state, &decoded)
	if problem != "" {
		decoded.Problems = append(decoded.Problems, push.Problem{Reason: ReasonPacketMalformed, Count: 1,
			Detail: problem})
	}
	// An IPFIX sequence number counts data records (RFC 7011, section 3.1). A set held for its
	// template holds an unknown number of them, so the next packet is a new starting point rather
	// than a loss measured against a count that was not known.
	d.followSequence(state, header, uint32(dataRecords), held > 0 || problem != "", meta, &decoded)
	return decoded
}

// walkSets reads every set of a packet, returning how many data records of the packet it decoded
// (for IPFIX's sequence numbers), how many sets it held for their template, and a malformation, if
// any. Records of earlier packets released by a template are not this packet's.
func (d *decoder) walkSets(sets []byte, header packetHeader, meta push.Meta, state *scopeState,
	decoded *push.Decoded) (int, int, string) {
	dataRecords, held, ordinal := 0, 0, -1
	for len(sets) > 0 {
		ordinal++
		if len(sets) < setHeaderLength {
			// Trailing padding shorter than a set header.
			return dataRecords, held, ""
		}
		id := binary.BigEndian.Uint16(sets[:2])
		length := int(binary.BigEndian.Uint16(sets[2:4]))
		if length < setHeaderLength || length > len(sets) {
			return dataRecords, held, "a set whose length field overruns the packet"
		}
		body := sets[setHeaderLength:length]
		sets = sets[length:]
		switch {
		case header.version == versionV9 && id == v9TemplateSet,
			header.version == versionIPFIX && id == ipfixTemplateSet:
			if problem := d.readTemplates(body, header.version, false, state, decoded); problem != "" {
				return dataRecords, held, problem
			}
			d.releaseHeld(state, decoded)
		case header.version == versionV9 && id == v9OptionsTemplateSet,
			header.version == versionIPFIX && id == ipfixOptionsTemplate:
			if problem := d.readTemplates(body, header.version, true, state, decoded); problem != "" {
				return dataRecords, held, problem
			}
			d.releaseHeld(state, decoded)
		case id >= firstDataSetID:
			tmpl, known := state.templates[id]
			if !known {
				d.hold(state, heldSet{templateID: id, set: ordinal, payload: append([]byte(nil), body...),
					header: header, meta: meta}, decoded)
				held++
				continue
			}
			dataRecords += d.readData(body, tmpl, header, ordinal, meta, decoded)
		default:
			// Set ids 4 to 255 are reserved (RFC 7011, section 3.3.2): skipped.
		}
	}
	return dataRecords, held, ""
}

// readTemplates reads a template or options template set; a template record with no field is an
// IPFIX withdrawal (RFC 7011, section 8.1).
func (d *decoder) readTemplates(body []byte, version int, options bool, state *scopeState,
	decoded *push.Decoded) string {
	for len(body) >= 4 {
		id := binary.BigEndian.Uint16(body[:2])
		var fieldCount, scopeCount int
		position := 4
		switch {
		case version == versionV9 && options:
			if len(body) < 6 {
				return "a v9 options template shorter than its header"
			}
			scopeLength := int(binary.BigEndian.Uint16(body[2:4]))
			optionLength := int(binary.BigEndian.Uint16(body[4:6]))
			if scopeLength%4 != 0 || optionLength%4 != 0 {
				return "a v9 options template whose lengths are not multiples of four"
			}
			scopeCount = scopeLength / 4
			fieldCount = scopeCount + optionLength/4
			position = 6
		case version == versionIPFIX && options:
			if len(body) < 6 {
				return "an IPFIX options template shorter than its header"
			}
			fieldCount = int(binary.BigEndian.Uint16(body[2:4]))
			scopeCount = int(binary.BigEndian.Uint16(body[4:6]))
			position = 6
		default:
			fieldCount = int(binary.BigEndian.Uint16(body[2:4]))
		}
		if version == versionIPFIX && fieldCount == 0 && (id == ipfixTemplateSet || id == ipfixOptionsTemplate) {
			// An all-templates withdrawal (RFC 7011, section 8.1): template id 2 withdraws every
			// template of the set, 3 every options template.
			for held, tmpl := range state.templates {
				if tmpl.options == options {
					delete(state.templates, held)
				}
			}
			body = body[position:]
			continue
		}
		if id < firstDataSetID {
			if id == 0 && fieldCount == 0 {
				// Padding (RFC 7011, section 3.3.1): the rest of the set is zeros.
				return ""
			}
			return "a template whose id is below 256"
		}
		if fieldCount == 0 && version == versionIPFIX {
			delete(state.templates, id)
			body = body[position:]
			continue
		}
		fields := make([]field, 0, fieldCount)
		minimum := 0
		for range fieldCount {
			if len(body) < position+4 {
				return "a template whose fields overrun its set"
			}
			id := binary.BigEndian.Uint16(body[position : position+2])
			length := binary.BigEndian.Uint16(body[position+2 : position+4])
			position += 4
			var enterprise uint32
			if version == versionIPFIX && id&enterpriseBit != 0 {
				if len(body) < position+4 {
					return "an enterprise field whose number overruns its set"
				}
				enterprise = binary.BigEndian.Uint32(body[position : position+4])
				position += 4
				id &^= enterpriseBit
			}
			if version == versionV9 && length == variableLength {
				return "a v9 template declaring a variable-length field, which v9 does not have"
			}
			fields = append(fields, describe(id, enterprise, length))
			if length != variableLength {
				minimum += int(length)
			} else {
				minimum++
			}
		}
		if previous, found := state.templates[id]; !found && len(state.templates) >= d.limits.TemplatesPerExporter &&
			d.limits.TemplatesPerExporter > 0 {
			decoded.Problems = append(decoded.Problems, push.Problem{Reason: ReasonTemplateLimit, Count: 1,
				Detail: "a template was refused: the exporter already holds the most templates kept"})
			body = body[position:]
			continue
		} else if found && sameFields(previous.fields, fields) {
			body = body[position:]
			continue
		}
		d.generation++
		state.templates[id] = &template{id: id, version: version, fields: fields, scopeCount: scopeCount,
			options: options, generation: d.generation, minLength: minimum}
		body = body[position:]
	}
	return ""
}

func sameFields(a, b []field) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index].id != b[index].id || a[index].enterprise != b[index].enterprise || a[index].length != b[index].length {
			return false
		}
	}
	return true
}

// describe names and types a field from the registry; an enterprise field, or an IANA id the
// registry does not assign, is an octet array under a name built from its numbers.
func describe(id uint16, enterprise uint32, length uint16) field {
	if enterprise == 0 {
		if element, found := ipfixregistry.Lookup(id); found {
			return field{id: id, length: length, name: element.Name, abstract: element.AbstractType}
		}
		return field{id: id, length: length, name: fmt.Sprintf("unassigned_%d", id),
			abstract: ipfixregistry.TypeOctetArray}
	}
	return field{id: id, enterprise: enterprise, length: length, name: ipfixregistry.EnterpriseName(enterprise, id),
		abstract: ipfixregistry.TypeOctetArray}
}

// readData reads the records of a data set; what remains shorter than the template's shortest
// record is padding (RFC 3954, section 5.3; RFC 7011, section 3.3.1).
func (d *decoder) readData(body []byte, tmpl *template, header packetHeader, set int, meta push.Meta,
	decoded *push.Decoded) int {
	count := 0
	for len(body) >= tmpl.minLength && tmpl.minLength > 0 {
		fields := make(map[string]any, len(tmpl.fields)+6)
		position := 0
		ok := true
		for _, f := range tmpl.fields {
			length := int(f.length)
			if f.length == variableLength {
				if position >= len(body) {
					ok = false
					break
				}
				length = int(body[position])
				position++
				if length == 255 {
					if position+2 > len(body) {
						ok = false
						break
					}
					length = int(binary.BigEndian.Uint16(body[position : position+2]))
					position += 2
				}
			}
			if position+length > len(body) {
				ok = false
				break
			}
			value, err := decodeValue(f.abstract, body[position:position+length])
			position += length
			if err != nil {
				// A field the length of which its type cannot have is left out: a subscriber
				// requiring it skips the record and counts it.
				continue
			}
			fields[f.name] = value
		}
		if !ok || position == 0 {
			break
		}
		body = body[position:]
		d.finish(fields, tmpl, header, set<<16|count, meta, decoded)
		count++
	}
	return count
}

// finish adds what the packet header says to a record -- the exporter, the domain, the version and
// the reception instant, under their registry names -- and the absolute start and end instants
// computed from the relative time elements (PS5), then appends it.
func (d *decoder) finish(fields map[string]any, tmpl *template, header packetHeader, position int, meta push.Meta,
	decoded *push.Decoded) {
	if meta.Sender.Is4() {
		fields["exporterIPv4Address"] = meta.Sender
	} else if meta.Sender.IsValid() {
		fields["exporterIPv6Address"] = meta.Sender
	}
	fields["observationDomainId"] = uint64(header.domain)
	fields["exportProtocolVersion"] = uint64(header.version)
	fields["collectionTimeMilliseconds"] = meta.ReceivedAt.UTC()
	addAbsoluteTimes(fields, header)
	kind := "template"
	if tmpl.options {
		kind = "options template"
	}
	d.seen[fmt.Sprintf("v%d %s %d (%d fields)", header.version, kind, tmpl.id, len(tmpl.fields))]++
	routeKey := fmt.Sprintf("%s|%d|%d|%d", meta.Sender, header.domain, tmpl.id, tmpl.generation)
	record := push.Record{RouteKey: routeKey, Fields: fields, Sender: meta.Sender, Transport: meta.Transport,
		ReceivedAt: meta.ReceivedAt, InstantField: "flowStartMilliseconds", Delivery: header.delivery, Position: position}
	if start, ok := fields["flowStartMilliseconds"].(time.Time); ok {
		record.Instant = start
	}
	decoded.Records = append(decoded.Records, record)
}

// addAbsoluteTimes computes flowStartMilliseconds and flowEndMilliseconds when the record carries
// them otherwise:
//
//   - v9's FIRST_SWITCHED and LAST_SWITCHED (flowStartSysUpTime, flowEndSysUpTime) are milliseconds
//     of the exporter's uptime; the header's unix_secs is the export instant and its sysUptime the
//     uptime then, so an instant is unix_secs - (sysUptime - value) milliseconds (RFC 3954, 5.1, 8);
//   - IPFIX's flowStartSysUpTime and flowEndSysUpTime are relative to systemInitTimeMilliseconds;
//   - IPFIX's flowStartDeltaMicroseconds and flowEndDeltaMicroseconds count back from the export
//     time (RFC 7011, section 4.4 and the registry);
//   - flowStart/EndSeconds, Microseconds and Nanoseconds are the same instant at another precision.
//
// A record carrying none of these has no start instant, and a subscriber whose time column needs one
// refuses the row.
func addAbsoluteTimes(fields map[string]any, header packetHeader) {
	for _, pair := range [][2]string{{"flowStartMilliseconds", "Start"}, {"flowEndMilliseconds", "End"}} {
		target, which := pair[0], pair[1]
		if _, present := fields[target]; present {
			continue
		}
		for _, other := range []string{"flow" + which + "Seconds", "flow" + which + "Microseconds",
			"flow" + which + "Nanoseconds"} {
			if instant, ok := fields[other].(time.Time); ok {
				fields[target] = instant
				break
			}
		}
		if _, present := fields[target]; present {
			continue
		}
		if uptime, ok := fields["flow"+which+"SysUpTime"].(uint64); ok {
			switch header.version {
			case versionV9:
				exportMillis := header.exportTime.UnixMilli()
				fields[target] = time.UnixMilli(exportMillis - int64(header.sysUptime) + int64(uptime)).UTC()
			case versionIPFIX:
				if init, ok := fields["systemInitTimeMilliseconds"].(time.Time); ok {
					fields[target] = init.Add(time.Duration(uptime) * time.Millisecond).UTC()
				}
			}
			continue
		}
		if delta, ok := fields["flow"+which+"DeltaMicroseconds"].(uint64); ok {
			fields[target] = header.exportTime.Add(-time.Duration(delta) * time.Microsecond).UTC()
		}
	}
	if _, present := fields["flowEndMilliseconds"]; !present {
		if start, ok := fields["flowStartMilliseconds"].(time.Time); ok {
			if duration, ok := fields["flowDurationMilliseconds"].(uint64); ok {
				fields["flowEndMilliseconds"] = start.Add(time.Duration(duration) * time.Millisecond).UTC()
			} else if duration, ok := fields["flowDurationMicroseconds"].(uint64); ok {
				fields["flowEndMilliseconds"] = start.Add(time.Duration(duration) * time.Microsecond).UTC()
			}
		}
	}
}

// hold keeps a data set whose template has not arrived, within the bound; past it the oldest is
// dropped and counted as netflow_template_missing, with a gap over its reception instant.
func (d *decoder) hold(state *scopeState, set heldSet, decoded *push.Decoded) {
	limit := d.limits.HeldRecords
	if limit <= 0 {
		decoded.Problems = append(decoded.Problems, templateMissing(1, set.meta.ReceivedAt, set.meta.ReceivedAt))
		return
	}
	if len(state.held) >= limit {
		dropped := state.held[0]
		state.held = state.held[1:]
		decoded.Problems = append(decoded.Problems,
			templateMissing(1, dropped.meta.ReceivedAt, dropped.meta.ReceivedAt))
	}
	state.held = append(state.held, set)
}

func templateMissing(count int, from, to time.Time) push.Problem {
	return push.Problem{Reason: ReasonTemplateMissing, Count: count, Gap: true, From: from, To: to,
		Detail: "data sets whose template never arrived within the bound were dropped"}
}

// releaseHeld decodes every held set whose template is now known.
func (d *decoder) releaseHeld(state *scopeState, decoded *push.Decoded) {
	kept := state.held[:0]
	for _, set := range state.held {
		tmpl, known := state.templates[set.templateID]
		if !known {
			kept = append(kept, set)
			continue
		}
		d.readData(set.payload, tmpl, set.header, set.set, set.meta, decoded)
	}
	state.held = kept
}

// followSequence checks a packet's sequence number against the one expected. A forward jump is a
// loss, but it is recorded only once the next packet confirms the new position: an exporter whose
// several counters share one domain (survey S11) jumps forward and back, and a jump back marks the
// stream interleaved, after which nothing is asserted about its loss.
func (d *decoder) followSequence(state *scopeState, header packetHeader, advance uint32, uncounted bool,
	meta push.Meta, decoded *push.Decoded) {
	sequence := &state.sequence
	wasKnown := sequence.known
	defer func() {
		sequence.expected = header.sequence + advance
		sequence.lastExport = header.exportTime
		sequence.known = !uncounted
	}()
	if !wasKnown || sequence.interleaved {
		return
	}
	if sequence.pending != nil {
		if header.sequence == sequence.pending.nextExpect {
			gap := sequence.pending
			sequence.pending = nil
			decoded.Problems = append(decoded.Problems, sequenceGap(state.name, gap))
		} else {
			sequence.pending = nil
			d.interleave(state, header, decoded)
			return
		}
	}
	difference := header.sequence - sequence.expected
	switch {
	case difference == 0:
	case difference < sequenceForwardWindow:
		sequence.pending = &pendingGap{from: sequence.lastExport, to: header.exportTime, missing: difference,
			nextExpect: header.sequence + advance, detectedAt: meta.ReceivedAt}
	default:
		d.interleave(state, header, decoded)
	}
}

// interleave marks a scope's stream interleaved and tells the subscribers so, once: a condition at the
// export time of the packet that showed it, which a sink reading the exporter's configuration may
// find contradicts what that configuration says (PS18 of the NetFlow join amendment).
func (d *decoder) interleave(state *scopeState, header packetHeader, decoded *push.Decoded) {
	state.sequence.interleaved = true
	d.flags[FlagSequenceInterleaved] = true
	decoded.Problems = append(decoded.Problems, push.Problem{Reason: FlagSequenceInterleaved, Count: 1,
		Condition: true, Sequence: true, From: header.exportTime, To: header.exportTime, Scope: state.name,
		Detail: "the exporter's sequence numbers went backwards: several counters share this observation domain, " +
			"so no loss is asserted from them until the stream is followed afresh"})
}

// ResetSequences forgets what was concluded of every scope's sequence numbers: a stream found
// interleaved, and a forward jump not yet confirmed, are followed afresh from the next datagram.
// Whoever reads the exporter's configuration calls it when that configuration changes.
func (d *decoder) ResetSequences() {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	for _, state := range d.scopes {
		state.sequence = sequenceState{}
	}
	delete(d.flags, FlagSequenceInterleaved)
}

func sequenceGap(scope string, gap *pendingGap) push.Problem {
	return push.Problem{Reason: ReasonSequenceGap, Count: int(gap.missing), Gap: true, From: gap.from, To: gap.to,
		Sequence: true, Scope: scope,
		Detail: fmt.Sprintf("the exporter's sequence number jumped by %d", gap.missing)}
}

// Expire drops held sets past their age and records a forward jump nothing contradicted within it.
func (d *decoder) Expire(now time.Time) push.Decoded {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	var decoded push.Decoded
	bound := time.Duration(d.limits.HeldSeconds) * time.Second
	keys := make([]scope, 0, len(d.scopes))
	for key := range d.scopes {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].exporter != keys[j].exporter {
			return keys[i].exporter.Less(keys[j].exporter)
		}
		return keys[i].domain < keys[j].domain
	})
	for _, key := range keys {
		state := d.scopes[key]
		kept := state.held[:0]
		for _, set := range state.held {
			if now.Sub(set.meta.ReceivedAt) > bound {
				decoded.Problems = append(decoded.Problems,
					templateMissing(1, set.meta.ReceivedAt, set.meta.ReceivedAt))
				continue
			}
			kept = append(kept, set)
		}
		state.held = kept
		if pending := state.sequence.pending; pending != nil && now.Sub(pending.detectedAt) > bound {
			state.sequence.pending = nil
			decoded.Problems = append(decoded.Problems, sequenceGap(state.name, pending))
		}
	}
	return decoded
}

func (d *decoder) State() push.DecoderState {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	seen := make(map[string]int64, len(d.seen))
	for key, count := range d.seen {
		seen[key] = count
	}
	var flags []string
	for flag := range d.flags {
		flags = append(flags, flag)
	}
	sort.Strings(flags)
	return push.DecoderState{Seen: seen, Flags: flags}
}

// digestOf is a datagram's identity: the SHA-256 of its octets, in hexadecimal.
func digestOf(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
