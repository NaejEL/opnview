package collecttest

import (
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The fixtures of NJ12 (the amendment "the NetFlow join with every interface captured" to
// specs/SPEC-backend-completion-plugins.md): one world -- interfaces_info's answer and the capture
// configuration -- and one file per shape, each holding the legs the filter log logs, the records the
// exporter sends, and what the join and the measured volume must make of them. Every file carries
// __provenance__, naming what of the capture analysis it follows and what was rewritten or synthesised.

// JoinFixtureDirectory is the fixtures' directory, relative to internal/collect.
const JoinFixtureDirectory = "testdata/netflowjoin"

// WorldCapture is one interface of a fixture's capture configuration.
type WorldCapture struct {
	Identifier string `json:"identifier"`
	EgressOnly bool   `json:"egress_only"`
}

// JoinWorld is the world every fixture runs in.
type JoinWorld struct {
	Provenance map[string]any `json:"__provenance__"`
	Capture    []WorldCapture `json:"capture"`
	// Body is interfaces_info's answer: discovery's, the index map's and the netflow plugin's.
	Body map[string]any `json:"body"`
}

// Offered are the identifiers getconfig offers: every assigned interface of the world.
func (w JoinWorld) Offered() []string {
	var offered []string
	rows, _ := w.Body["rows"].([]any)
	for _, row := range rows {
		fields, _ := row.(map[string]any)
		if identifier, _ := fields["identifier"].(string); identifier != "" {
			offered = append(offered, identifier)
		}
	}
	sort.Strings(offered)
	return offered
}

// Captured is the world's capture configuration, or a fixture's own.
func (w JoinWorld) Captured(override []WorldCapture) []Captured {
	chosen := w.Capture
	if len(override) > 0 {
		chosen = override
	}
	captured := make([]Captured, 0, len(chosen))
	for _, one := range chosen {
		captured = append(captured, Captured{Identifier: one.Identifier, EgressOnly: one.EgressOnly})
	}
	return captured
}

// FixtureLeg is one filter-log line of a fixture; at is seconds after LegInstant.
type FixtureLeg struct {
	Digest         string `json:"digest"`
	At             int64  `json:"at"`
	Device         string `json:"device"`
	Dir            string `json:"dir"`
	Action         string `json:"action"`
	Src            string `json:"src"`
	Dst            string `json:"dst"`
	SrcPort        int    `json:"src_port"`
	DstPort        int    `json:"dst_port"`
	Protocol       string `json:"protocol"`
	ProtocolNumber int    `json:"protocol_number"`
	IPID           int    `json:"ip_id"`
	TCPSeq         int    `json:"tcp_seq"`
}

// Row is the leg as the filter log answers it.
func (l FixtureLeg) Row() LegRow {
	return LegRow{Digest: l.Digest, At: LegInstant + l.At, Device: l.Device, Dir: l.Dir, Action: l.Action, Src: l.Src,
		Dst: l.Dst, SrcPort: l.SrcPort, DstPort: l.DstPort, Protocol: l.Protocol, ProtocolNumber: l.ProtocolNumber,
		IPID: l.IPID, Sequence: l.TCPSeq}
}

// FixtureExpect is what the join must decide of one record.
type FixtureExpect struct {
	JoinState   string `json:"join_state"`
	Leg         string `json:"leg"`
	Orientation string `json:"orientation"`
	CopyKind    string `json:"copy_kind"`
	CopyLeg     string `json:"copy_leg"`
	Reason      string `json:"reason"`
	CountedCopy bool   `json:"counted_copy"`
	// PendingBeforeBound says the record stays pending while the filter log has not been read past its
	// leg's export bound (NJ4 rule 4).
	PendingBeforeBound bool `json:"pending_before_bound"`
}

// FixtureCount names the connection direction a record's figure counts in: what NJ-AC9's independent
// computation sums.
type FixtureCount struct {
	Connection string `json:"connection"`
	Direction  string `json:"direction"`
}

// FixtureRecord is one record the exporter sends; start and end are seconds after LegInstant.
type FixtureRecord struct {
	Src      string `json:"src"`
	Dst      string `json:"dst"`
	SrcPort  uint16 `json:"src_port"`
	DstPort  uint16 `json:"dst_port"`
	Protocol uint8  `json:"protocol"`
	Flags    uint8  `json:"flags"`
	In       uint16 `json:"in"`
	Out      uint16 `json:"out"`
	Packets  uint32 `json:"packets"`
	Octets   uint32 `json:"octets"`
	Start    int64  `json:"start"`
	End      int64  `json:"end"`
	// Side is the packets' direction in their connection, forward or reverse: what the orders of
	// NJ-AC6 deliver first.
	Side     string        `json:"side"`
	Datagram int           `json:"datagram"`
	Expect   FixtureExpect `json:"expect"`
	Counts   *FixtureCount `json:"counts"`
}

// FlowRecord is the record as the datagram builder takes it.
func (r FixtureRecord) FlowRecord() FlowRecord {
	return FlowRecord{Src: netip.MustParseAddr(r.Src), Dst: netip.MustParseAddr(r.Dst), SrcPort: r.SrcPort,
		DstPort: r.DstPort, Protocol: r.Protocol, Flags: r.Flags, In: r.In, Out: r.Out, Packets: r.Packets,
		Octets: r.Octets, Start: LegInstant + r.Start, End: LegInstant + r.End}
}

// Key identifies a record among the stored rows: records alike in every field share it, and their
// expectations are alike too.
func (r FixtureRecord) Key() string {
	return RecordKey(netip.MustParseAddr(r.Src).String(), netip.MustParseAddr(r.Dst).String(), int64(r.SrcPort),
		int64(r.DstPort), int64(r.In), int64(r.Out), int64(r.Octets), LegInstant+r.Start)
}

// RecordKey is a stored record's key: its two ends, ports, raw indexes, octets and start.
func RecordKey(src, dst string, srcPort, dstPort, in, out, octets, start int64) string {
	return strings.Join([]string{src, dst, itoa(srcPort), itoa(dstPort), itoa(in), itoa(out), itoa(octets), itoa(start)},
		"|")
}

// FixtureVolume is one direction of a connection's expected measured volume.
type FixtureVolume struct {
	Connection  string `json:"connection"`
	Direction   string `json:"direction"`
	State       string `json:"state"`
	CountedCopy bool   `json:"counted_copy"`
}

// JoinFixture is one shape of NJ12.
type JoinFixture struct {
	Provenance map[string]any  `json:"__provenance__"`
	Name       string          `json:"name"`
	Capture    []WorldCapture  `json:"capture"`
	Legs       []FixtureLeg    `json:"legs"`
	ExportAt   int64           `json:"export_at"`
	Records    []FixtureRecord `json:"records"`
	Volumes    []FixtureVolume `json:"volumes"`
	// File is the file it was read from.
	File string `json:"-"`
}

// LoadJoinWorld reads the world from a fixture directory.
func LoadJoinWorld(t *testing.T, directory string) JoinWorld {
	t.Helper()
	var world JoinWorld
	readJSON(t, filepath.Join(directory, "world.json"), &world)
	return world
}

// LoadJoinFixtures reads every shape of a fixture directory that holds records and legs: every file
// but the world and the counters' (F16, which TestLossIsDetectedUnderSeveralCounters reads).
func LoadJoinFixtures(t *testing.T, directory string) []JoinFixture {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []JoinFixture
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || name == "world.json" || strings.HasPrefix(name, "f16_") {
			continue
		}
		var fixture JoinFixture
		readJSON(t, filepath.Join(directory, name), &fixture)
		fixture.File = name
		fixtures = append(fixtures, fixture)
	}
	return fixtures
}

// readJSON decodes one file, refusing a field the type does not know.
func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// DeliverFixtureRecords sends the records of fixtures a filter keeps, each fixture's records grouped in
// its datagrams, exported at LegInstant plus the fixture's export_at plus the datagram's ordinal.
func (h *NetflowHarness) DeliverFixtureRecords(t *testing.T, fixtures []JoinFixture, keep func(FixtureRecord) bool) {
	t.Helper()
	for _, fixture := range fixtures {
		datagrams := map[int][]FlowRecord{}
		for _, record := range fixture.Records {
			if keep(record) {
				datagrams[record.Datagram] = append(datagrams[record.Datagram], record.FlowRecord())
			}
		}
		ordinals := make([]int, 0, len(datagrams))
		for ordinal := range datagrams {
			ordinals = append(ordinals, ordinal)
		}
		sort.Ints(ordinals)
		for _, ordinal := range ordinals {
			h.Deliver(t, LegInstant+fixture.ExportAt+int64(ordinal), datagrams[ordinal]...)
		}
	}
}

// FixtureLegs are the legs of fixtures, in their order.
func FixtureLegs(fixtures []JoinFixture) []LegRow {
	var legs []LegRow
	for _, fixture := range fixtures {
		for _, leg := range fixture.Legs {
			legs = append(legs, leg.Row())
		}
	}
	return legs
}

func itoa(value int64) string { return strconv.FormatInt(value, 10) }
