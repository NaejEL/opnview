package netflow

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/push/ipfixregistry"
)

// Packets are built here field by field, from RFC 3954 and RFC 7011, with documentation addresses
// (RFC 5737, RFC 3849). The OPNsense-shaped template is ng_netflow's (survey, Push sources, S10).

var (
	exporter = netip.MustParseAddr("192.0.2.1")
	received = time.Date(2026, 9, 27, 0, 0, 10, 0, time.UTC)
)

func meta() push.Meta {
	return push.Meta{Sender: exporter, Transport: push.TransportUDP, ReceivedAt: received}
}

func limits() push.Limits {
	return push.Limits{HeldRecords: 4, HeldSeconds: 60, TemplatesPerExporter: 16}
}

type writer struct{ bytes []byte }

func (w *writer) u8(v uint8)   { w.bytes = append(w.bytes, v) }
func (w *writer) u16(v uint16) { w.bytes = binary.BigEndian.AppendUint16(w.bytes, v) }
func (w *writer) u32(v uint32) { w.bytes = binary.BigEndian.AppendUint32(w.bytes, v) }
func (w *writer) u64(v uint64) { w.bytes = binary.BigEndian.AppendUint64(w.bytes, v) }
func (w *writer) raw(v []byte) { w.bytes = append(w.bytes, v...) }

// set wraps a body in a set header, padded to four octets.
func set(id uint16, body []byte) []byte {
	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	out := &writer{}
	out.u16(id)
	out.u16(uint16(len(body) + 4))
	out.raw(body)
	return out.bytes
}

// v9 builds a NetFlow v9 packet.
func v9(sysUptime, unixSecs, sequence, sourceID uint32, sets ...[]byte) []byte {
	out := &writer{}
	out.u16(9)
	out.u16(uint16(len(sets)))
	out.u32(sysUptime)
	out.u32(unixSecs)
	out.u32(sequence)
	out.u32(sourceID)
	for _, s := range sets {
		out.raw(s)
	}
	return out.bytes
}

// ipfix builds an IPFIX message.
func ipfix(exportTime, sequence, domain uint32, sets ...[]byte) []byte {
	body := &writer{}
	for _, s := range sets {
		body.raw(s)
	}
	out := &writer{}
	out.u16(10)
	out.u16(uint16(16 + len(body.bytes)))
	out.u32(exportTime)
	out.u32(sequence)
	out.u32(domain)
	out.raw(body.bytes)
	return out.bytes
}

// ngTemplate is the IPv4 template ng_netflow emits: (type, length) pairs, RFC 3954 numbering.
var ngTemplate = [][2]uint16{{8, 4}, {12, 4}, {15, 4}, {10, 2}, {14, 2}, {2, 4}, {1, 4}, {24, 4}, {23, 4},
	{22, 4}, {21, 4}, {7, 2}, {11, 2}, {6, 1}, {4, 1}, {5, 1}, {16, 4}, {17, 4}, {9, 1}, {13, 1}}

func templateSet(id uint16, fields [][2]uint16) []byte {
	body := &writer{}
	body.u16(id)
	body.u16(uint16(len(fields)))
	for _, f := range fields {
		body.u16(f[0])
		body.u16(f[1])
	}
	return body.bytes
}

// ngRecord is one data record of ngTemplate.
func ngRecord(src, dst string, inIndex, outIndex uint16, packets, octets, first, last uint32, srcPort,
	dstPort uint16, flags, protocol uint8) []byte {
	out := &writer{}
	s, d := netip.MustParseAddr(src).As4(), netip.MustParseAddr(dst).As4()
	out.raw(s[:])
	out.raw(d[:])
	out.u32(0)
	out.u16(inIndex)
	out.u16(outIndex)
	out.u32(packets)
	out.u32(octets)
	out.u32(0)
	out.u32(0)
	out.u32(first)
	out.u32(last)
	out.u16(srcPort)
	out.u16(dstPort)
	out.u8(flags)
	out.u8(protocol)
	out.u8(0)
	out.u32(0)
	out.u32(0)
	out.u8(24)
	out.u8(24)
	return out.bytes
}

func decodeAll(t *testing.T, d *decoder, packets ...[]byte) push.Decoded {
	t.Helper()
	var all push.Decoded
	for _, packet := range packets {
		decoded := d.Decode(packet, meta())
		all.Records = append(all.Records, decoded.Records...)
		all.Problems = append(all.Problems, decoded.Problems...)
	}
	return all
}

// TestV9RecordsAreNamedTypedAndTimed is PS-AC6 and PS-AC4: a v9 template and its data, every field
// under its IANA name and the type of its abstract data type, start and end from sysUptime arithmetic.
func TestV9RecordsAreNamedTypedAndTimed(t *testing.T) {
	t.Parallel()
	// Exported at 1790467200 with an uptime of 100 s: a record first seen at uptime 40 s started
	// 60 s before the export.
	packet := v9(100_000, 1_790_467_200, 7, 0,
		set(0, templateSet(257, ngTemplate)),
		set(257, append(ngRecord("198.51.100.10", "203.0.113.20", 3, 1, 10, 1420, 40_000, 95_000, 44321, 443, 0x1b, 6),
			ngRecord("198.51.100.11", "203.0.113.21", 3, 1, 1, 84, 99_000, 99_000, 0, 0, 0, 1)...)))
	decoded := decodeAll(t, newDecoder(limits()), packet)
	if len(decoded.Problems) != 0 || len(decoded.Records) != 2 {
		t.Fatalf("decoded %d records, problems %+v", len(decoded.Records), decoded.Problems)
	}
	fields := decoded.Records[0].Fields
	want := map[string]any{
		"sourceIPv4Address": netip.MustParseAddr("198.51.100.10"), "destinationIPv4Address": netip.MustParseAddr("203.0.113.20"),
		"ingressInterface": uint64(3), "egressInterface": uint64(1), "packetDeltaCount": uint64(10),
		"octetDeltaCount": uint64(1420), "sourceTransportPort": uint64(44321), "destinationTransportPort": uint64(443),
		"tcpControlBits": uint64(0x1b), "protocolIdentifier": uint64(6), "exporterIPv4Address": exporter,
		"observationDomainId": uint64(0), "exportProtocolVersion": uint64(9),
		"flowStartMilliseconds": time.Unix(1_790_467_140, 0).UTC(), "flowEndMilliseconds": time.Unix(1_790_467_195, 0).UTC(),
	}
	for name, value := range want {
		if got := fields[name]; got != value {
			if gotTime, isTime := got.(time.Time); !isTime || !gotTime.Equal(value.(time.Time)) {
				t.Errorf("%s = %v (%T), want %v", name, got, got, value)
			}
		}
	}
	// Every decoded field has a registry name and a plugin type.
	for name := range fields {
		if _, known := (protocol{}).FieldType(name); !known {
			t.Errorf("the field %s has no registry type", name)
		}
	}
	if columnType, _ := (protocol{}).FieldType("sourceIPv4Address"); columnType != push.TypeIP {
		t.Errorf("an ipv4Address is %s", columnType)
	}
	if columnType, _ := (protocol{}).FieldType("octetDeltaCount"); columnType != push.TypeInteger {
		t.Errorf("an unsigned64 is %s", columnType)
	}
	if columnType, _ := (protocol{}).FieldType("flowStartMilliseconds"); columnType != push.TypeInstant {
		t.Errorf("a dateTimeMilliseconds is %s", columnType)
	}
	if !decoded.Records[0].Instant.Equal(time.Unix(1_790_467_140, 0)) {
		t.Errorf("the record's instant is %v", decoded.Records[0].Instant)
	}
}

// TestDataBeforeItsTemplateIsHeldThenDecoded is PS-AC6: held within the bound, decoded when the
// template arrives; past the bound counted as netflow_template_missing with a gap.
func TestDataBeforeItsTemplateIsHeldThenDecoded(t *testing.T) {
	t.Parallel()
	d := newDecoder(limits())
	data := set(257, ngRecord("198.51.100.10", "203.0.113.20", 3, 1, 1, 60, 1000, 1000, 1, 2, 0, 17))
	first := d.Decode(v9(2000, 1_790_467_200, 1, 0, data), meta())
	if len(first.Records) != 0 || len(first.Problems) != 0 {
		t.Fatalf("data before its template: %+v", first)
	}
	second := d.Decode(v9(3000, 1_790_467_201, 2, 0, set(0, templateSet(257, ngTemplate))), meta())
	if len(second.Records) != 1 {
		t.Fatalf("the held data was not decoded when its template arrived: %+v", second)
	}
	// Past the bound: five sets of an unknown template with a bound of four drop the oldest.
	d = newDecoder(limits())
	var problems []push.Problem
	for i := range 5 {
		problems = append(problems, d.Decode(v9(2000, 1_790_467_200, uint32(i+1), 0, set(300, []byte{1, 2, 3, 4})), meta()).Problems...)
	}
	if len(problems) != 1 || problems[0].Reason != ReasonTemplateMissing || !problems[0].Gap {
		t.Errorf("the overflowing hold reported %+v", problems)
	}
	expired := d.Expire(received.Add(2 * time.Minute))
	if len(expired.Problems) != 4 {
		t.Errorf("expiry dropped %d held sets, want 4", len(expired.Problems))
	}
}

// TestIPFIXWithdrawalRedefinitionVariableLengthEnterprise is PS-AC6: a template withdrawn and
// redefined, a variable-length field, padding and an enterprise field, with absolute and delta times
// giving the same instants (PS-AC4).
func TestIPFIXWithdrawalRedefinitionVariableLengthEnterprise(t *testing.T) {
	t.Parallel()
	d := newDecoder(limits())
	template := &writer{}
	template.u16(256)
	template.u16(4)
	template.u16(8) // sourceIPv4Address
	template.u16(4)
	template.u16(82) // interfaceName, a string, variable length
	template.u16(65535)
	template.u16(152) // flowStartMilliseconds
	template.u16(8)
	template.u16(0x8000 | 5) // an enterprise field
	template.u16(2)
	template.u32(32473)
	record := &writer{}
	address := netip.MustParseAddr("198.51.100.10").As4()
	record.raw(address[:])
	record.u8(6)
	record.raw([]byte("exdev0"))
	record.u64(1_790_467_190_500)
	record.u16(0xabcd)
	decoded := decodeAll(t, d, ipfix(1_790_467_200, 0, 1, set(2, template.bytes), set(256, record.bytes)))
	if len(decoded.Records) != 1 {
		t.Fatalf("decoded %+v", decoded)
	}
	fields := decoded.Records[0].Fields
	if fields["interfaceName"] != "exdev0" {
		t.Errorf("the variable-length field reads %v", fields["interfaceName"])
	}
	if got := fields[ipfixregistry.EnterpriseName(32473, 5)]; string(got.([]byte)) != "\xab\xcd" {
		t.Errorf("the enterprise field reads %v", got)
	}
	if start := fields["flowStartMilliseconds"].(time.Time); !start.Equal(time.UnixMilli(1_790_467_190_500)) {
		t.Errorf("the absolute start reads %v", start)
	}
	// Withdrawn (field count 0), then data of it is held, not decoded.
	withdrawal := &writer{}
	withdrawal.u16(256)
	withdrawal.u16(0)
	after := decodeAll(t, d, ipfix(1_790_467_201, 1, 1, set(2, withdrawal.bytes), set(256, record.bytes)))
	if len(after.Records) != 0 {
		t.Errorf("data of a withdrawn template was decoded")
	}
	// Redefined with a delta start: flowStartDeltaMicroseconds counts back from the export time.
	redefined := &writer{}
	redefined.u16(256)
	redefined.u16(2)
	redefined.u16(8)
	redefined.u16(4)
	redefined.u16(158) // flowStartDeltaMicroseconds
	redefined.u16(4)
	data := &writer{}
	data.raw(address[:])
	data.u32(9_500_000)
	again := decodeAll(t, d, ipfix(1_790_467_200, 2, 1, set(2, redefined.bytes), set(256, data.bytes)))
	var found bool
	for _, record := range again.Records {
		if start, ok := record.Fields["flowStartMilliseconds"].(time.Time); ok && start.Equal(time.UnixMilli(1_790_467_190_500)) {
			found = true
		}
	}
	if !found {
		t.Errorf("the delta time did not give the same instant as the absolute one: %+v", again.Records)
	}
}

// TestSequenceJumpsAreGapsWithTheirInterval is PS-AC6: a v9 sequence jump, confirmed by the next
// packet, is a netflow_sequence_gap between the export times on either side of it.
func TestSequenceJumpsAreGapsWithTheirInterval(t *testing.T) {
	t.Parallel()
	d := newDecoder(limits())
	template := set(0, templateSet(257, ngTemplate))
	decoded := decodeAll(t, d,
		v9(1000, 1_790_467_200, 10, 0, template),
		v9(2000, 1_790_467_201, 11, 0),
		v9(3000, 1_790_467_210, 15, 0),
		v9(4000, 1_790_467_211, 16, 0))
	var gaps []push.Problem
	for _, problem := range decoded.Problems {
		if problem.Reason == ReasonSequenceGap {
			gaps = append(gaps, problem)
		}
	}
	if len(gaps) != 1 || gaps[0].Count != 3 || !gaps[0].From.Equal(time.Unix(1_790_467_201, 0)) ||
		!gaps[0].To.Equal(time.Unix(1_790_467_210, 0)) {
		t.Errorf("gaps %+v", gaps)
	}
}

// TestInterleavedCountersAssertNoGap: several counters sharing one domain -- OPNsense's per-interface
// nodes behind one forwarder (survey S11) -- go backwards, and no gap is asserted from them.
func TestInterleavedCountersAssertNoGap(t *testing.T) {
	t.Parallel()
	d := newDecoder(limits())
	decoded := decodeAll(t, d,
		v9(1000, 1_790_467_200, 100, 0),
		v9(1000, 1_790_467_200, 5, 0),
		v9(1000, 1_790_467_201, 101, 0),
		v9(1000, 1_790_467_201, 6, 0))
	for _, problem := range decoded.Problems {
		if problem.Reason == ReasonSequenceGap {
			t.Errorf("an interleaved stream asserted a gap: %+v", problem)
		}
	}
	if flags := d.State().Flags; len(flags) != 1 || flags[0] != FlagSequenceInterleaved {
		t.Errorf("flags %v", flags)
	}
}

// TestV5IsCountedAndNotDecoded is PS-AC6: a v5 packet is netflow_version_unsupported.
func TestV5IsCountedAndNotDecoded(t *testing.T) {
	t.Parallel()
	packet := make([]byte, 24+48)
	binary.BigEndian.PutUint16(packet, 5)
	decoded := newDecoder(limits()).Decode(packet, meta())
	if len(decoded.Records) != 0 || len(decoded.Problems) != 1 || decoded.Problems[0].Reason != ReasonVersionUnsupported {
		t.Errorf("a v5 packet decoded into %+v", decoded)
	}
}

// TestOptionsTemplatesAreScopedPerExporterAndDomain: an options template's data decodes, and a
// template of one domain does not decode another's data.
func TestOptionsTemplatesAreScopedPerExporterAndDomain(t *testing.T) {
	t.Parallel()
	d := newDecoder(limits())
	options := &writer{}
	options.u16(300)
	options.u16(4) // scope length: one field
	options.u16(4) // option length: one field
	options.u16(1) // scope: System
	options.u16(4)
	options.u16(34) // samplingInterval
	options.u16(4)
	data := &writer{}
	data.u32(1)
	data.u32(1)
	decoded := decodeAll(t, d, v9(1000, 1_790_467_200, 1, 7, set(1, options.bytes), set(300, data.bytes)))
	if len(decoded.Records) != 1 || decoded.Records[0].Fields["samplingInterval"] != uint64(1) {
		t.Fatalf("the options data decoded into %+v", decoded.Records)
	}
	other := d.Decode(v9(1000, 1_790_467_200, 1, 8, set(300, data.bytes)), meta())
	if len(other.Records) != 0 {
		t.Errorf("a template of domain 7 decoded data of domain 8")
	}
}

// TestValuesDecodeByAbstractType covers the abstract types the registry uses.
func TestValuesDecodeByAbstractType(t *testing.T) {
	t.Parallel()
	mac, _ := decodeValue(ipfixregistry.TypeMACAddress, []byte{0x00, 0x00, 0x5e, 0x00, 0x53, 0x01})
	if mac.(net.HardwareAddr).String() != "00:00:5e:00:53:01" {
		t.Errorf("mac %v", mac)
	}
	if v, _ := decodeValue(ipfixregistry.TypeSigned16, []byte{0xff, 0xfe}); v != int64(-2) {
		t.Errorf("signed16 %v", v)
	}
	if v, _ := decodeValue(ipfixregistry.TypeBoolean, []byte{2}); v != false {
		t.Errorf("boolean 2 is %v", v)
	}
	if _, err := decodeValue(ipfixregistry.TypeIPv4Address, []byte{1, 2, 3}); err == nil {
		t.Error("a three-octet IPv4 address decoded")
	}
	ntp := make([]byte, 8)
	binary.BigEndian.PutUint32(ntp, uint32(1_790_467_200+ntpEpochOffset))
	binary.BigEndian.PutUint32(ntp[4:], 1<<31)
	if v, _ := decodeValue(ipfixregistry.TypeDateTimeNanoseconds, ntp); !v.(time.Time).Equal(time.Unix(1_790_467_200, 500_000_000)) {
		t.Errorf("an NTP timestamp reads %v", v)
	}
}

// TestFilterRefusesUnknownElements is part of PS-AC10: an element outside the registry is refused.
func TestFilterRefusesUnknownElements(t *testing.T) {
	t.Parallel()
	for _, filter := range []string{`{"require": ["notAnElement"]}`, `{"require": []}`,
		`{"require": ["octetDeltaCount"], "where": [{"element": "nope", "operator": "eq", "value": 1}]}`,
		`{"require": ["octetDeltaCount"], "where": [{"element": "protocolIdentifier", "operator": "like", "value": 1}]}`,
		`{"require": ["octetDeltaCount"], "unknown": 1}`} {
		if _, err := (protocol{}).Compile(json.RawMessage(filter)); err == nil {
			t.Errorf("%s compiled", filter)
		}
	}
	selector, err := (protocol{}).Compile(json.RawMessage(
		`{"require": ["octetDeltaCount"], "where": [{"element": "protocolIdentifier", "operator": "in", "values": [6, 17]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	tcp := &push.Record{Fields: map[string]any{"octetDeltaCount": uint64(1), "protocolIdentifier": uint64(6)}}
	icmp := &push.Record{Fields: map[string]any{"octetDeltaCount": uint64(1), "protocolIdentifier": uint64(1)}}
	missing := &push.Record{Fields: map[string]any{"protocolIdentifier": uint64(6)}}
	if ok, _ := selector.Admits(tcp); !ok {
		t.Error("a TCP record was not admitted")
	}
	if ok, _ := selector.Admits(icmp); ok {
		t.Error("an ICMP record was admitted")
	}
	if ok, reason := selector.Admits(missing); ok || reason != ReasonRequiredMissing {
		t.Errorf("a record lacking a required element: %v %q", ok, reason)
	}
}

// FuzzDecoder is the NetFlow / IPFIX decoder's fuzz target (PS15): no input panics, and the state
// stays within its bounds.
func FuzzDecoder(f *testing.F) {
	f.Add(v9(100_000, 1_790_467_200, 7, 0, set(0, templateSet(257, ngTemplate)),
		set(257, ngRecord("198.51.100.10", "203.0.113.20", 3, 1, 10, 1420, 40_000, 95_000, 44321, 443, 0x1b, 6))))
	f.Add(ipfix(1_790_467_200, 0, 1, set(2, []byte{1, 0, 0, 1, 0x80, 1, 0xff, 0xff, 0, 0, 0, 1}), set(256, []byte{3, 1, 2, 3})))
	f.Add([]byte{0, 5})
	f.Add([]byte{0, 10, 0, 16, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	d := newDecoder(limits())
	f.Fuzz(func(t *testing.T, packet []byte) {
		d.Decode(packet, meta())
		d.Expire(received.Add(time.Hour))
		for key, state := range d.scopes {
			if len(state.held) > limits().HeldRecords || len(state.templates) > limits().TemplatesPerExporter {
				t.Fatalf("the scope %v holds %d sets and %d templates", key, len(state.held), len(state.templates))
			}
		}
	})
}
