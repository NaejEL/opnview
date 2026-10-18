//go:build njreplay

package collect_test

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// TestReplayACapture is NJ-AC19's replay, run by hand and never by the project commands: the built join
// over a capture of a firewall's NetFlow export and filterlog lines, with that firewall's interfaces
// supplied as a test-only index map. NOTHING OF THE CAPTURE IS IN THE REPOSITORY: every input is named by
// an environment variable, read from outside the tree, and the outcome table is written outside it.
//
//	OPNVIEW_NJ_REPLAY_NETFLOW  the NetFlow frames: 8 octets of receive time (ns), 16 of source address,
//	                           2 of length, then the datagram
//	OPNVIEW_NJ_REPLAY_SYSLOG   the syslog lines, "<ns>\t<RFC 5424 line>"
//	OPNVIEW_NJ_REPLAY_MAPPING  the index map and capture: {"loopback_index": n, "interfaces": [{identifier,
//	                           device, index, network, ether, vlan_parent, egress_only, gateway}]}
//	OPNVIEW_NJ_REPLAY_REPORT   where the outcome table is written
//	OPNVIEW_NJ_REPLAY_GO_OUTCOMES the Go join's outcome file, recorded before the join left Go (PA10), which
//	                           the plugin's must equal record for record (PA-AC16)
//	OPNVIEW_NJ_REPLAY_OUTCOMES where the per-record outcome file is written, when set: PA10's recording
//	                           of every stored decision and figure, as njdump_test.go writes it
//
// go test -tags njreplay -run TestReplayACapture -timeout 590s ./internal/collect/
func TestReplayACapture(t *testing.T) {
	netflowPath, syslogPath := os.Getenv("OPNVIEW_NJ_REPLAY_NETFLOW"), os.Getenv("OPNVIEW_NJ_REPLAY_SYSLOG")
	mappingPath, reportPath := os.Getenv("OPNVIEW_NJ_REPLAY_MAPPING"), os.Getenv("OPNVIEW_NJ_REPLAY_REPORT")
	if netflowPath == "" || syslogPath == "" || mappingPath == "" || reportPath == "" {
		t.Fatal("the replay needs OPNVIEW_NJ_REPLAY_NETFLOW, _SYSLOG, _MAPPING and _REPORT")
	}
	var mapping struct {
		LoopbackIndex int `json:"loopback_index"`
		Interfaces    []struct {
			Identifier string `json:"identifier"`
			Device     string `json:"device"`
			Index      int    `json:"index"`
			Network    string `json:"network"`
			Ether      bool   `json:"ether"`
			VLANParent string `json:"vlan_parent"`
			EgressOnly bool   `json:"egress_only"`
			Gateway    string `json:"gateway"`
			Address    string `json:"address"`
		} `json:"interfaces"`
	}
	raw, err := os.ReadFile(mappingPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &mapping); err != nil {
		t.Fatal(err)
	}
	frames := readFrames(t, netflowPath)
	legs := readFilterlog(t, syslogPath)
	t.Logf("%d datagrams, %d filterlog legs", len(frames), len(legs))

	// The firewall's own addresses, which the capture shows as the destinations of local delivery.
	ownAddresses := map[string]netip.Addr{}
	for _, frame := range frames {
		for _, record := range v9Records(frame.payload) {
			if int(record.out) != mapping.LoopbackIndex {
				continue
			}
			for _, iface := range mapping.Interfaces {
				if iface.Network != "" && netip.MustParsePrefix(iface.Network).Contains(record.dst) {
					if _, known := ownAddresses[iface.Identifier]; !known {
						ownAddresses[iface.Identifier] = record.dst
					}
				}
			}
		}
	}
	var rows []any
	var offered []string
	var captured []Captured
	for _, iface := range mapping.Interfaces {
		row := map[string]any{"identifier": iface.Identifier, "device": iface.Device, "description": iface.Identifier,
			"status": "up", "enabled": "1", "link_type": "static", "gateways": []any{},
			"statistics": map[string]any{"index": strconv.Itoa(iface.Index)}, "flags": []any{"up", "running"},
			"macaddr": "00:00:00:00:00:00", "addr6": ""}
		if iface.Ether {
			row["macaddr"] = fmt.Sprintf("02:00:5e:00:00:%02x", iface.Index)
		}
		if iface.VLANParent != "" {
			row["vlan"] = map[string]any{"tag": "1", "parent": iface.VLANParent}
			row["vlan_tag"] = 1
		}
		if iface.Address != "" {
			ownAddresses[iface.Identifier] = netip.MustParseAddr(iface.Address)
		}
		if address, known := ownAddresses[iface.Identifier]; known {
			bits := netip.MustParsePrefix(iface.Network).Bits()
			row["addr4"] = fmt.Sprintf("%s/%d", address, bits)
		}
		if iface.Gateway != "" {
			row["gateways"] = []any{iface.Gateway}
		}
		rows = append(rows, row)
		offered = append(offered, iface.Identifier)
		captured = append(captured, Captured{Identifier: iface.Identifier, EgressOnly: iface.EgressOnly})
	}
	rows = append(rows, map[string]any{"identifier": "", "device": "replay-loopback", "flags": []any{"up", "loopback"},
		"statistics": map[string]any{"index": strconv.Itoa(mapping.LoopbackIndex)}})
	t.Logf("firewall addresses found: %v", ownAddresses)

	h := newNetflowWorld(t, map[string]any{"total": len(rows), "rowCount": len(rows),
		"current": 1, "rows": rows}, offered)
	first, last := frames[0].received, frames[len(frames)-1].received
	for _, leg := range legs {
		if leg.At < first {
			first = leg.At
		}
	}
	h.Capture(t, first-600, captured...)
	// Every datagram is received at its own instant, and the derivation runs once after the last: it maps each
	// record with the index map in force, which the capture never changes.
	h.Deferred = true
	for _, frame := range frames {
		h.At(frame.received)
		h.DeliverRaw(t, frame.payload)
	}
	h.RecordGaps()
	mapped := time.Now()
	h.Derive(t)
	t.Logf("mapping %d datagrams' records took %s", len(frames), time.Since(mapped).Round(time.Millisecond))
	// Every leg in one pass: the page holds the whole capture.
	settings := collecttest.ZeroMaxAges(config.Defaults()).WithPageSize("firewall_log", len(legs)+1)
	h.Collector.Configure(settings)
	started := time.Now()
	h.Legs = append(h.Legs, legs...)
	logged := make([]any, 0, len(h.Legs))
	for index := len(h.Legs) - 1; index >= 0; index-- {
		logged = append(logged, h.Legs[index].Row())
	}
	h.At(last + 4000)
	h.Fake.AnswerJSON(opnsense.FirewallLog, logged)
	if err := h.Collector.CollectFirewallLog(context.Background()); err != nil {
		t.Fatalf("the filter-log pass: %v", err)
	}
	t.Logf("the filter-log pass took %s", time.Since(started).Round(time.Millisecond))
	joined := time.Now()
	h.Derive(t)
	replayed := time.Since(joined)
	t.Logf("REPLAY: the netflow plugin's derivation -- the join of every record and the measured volume of every "+
		"connection -- took %s", replayed.Round(time.Millisecond))
	if replayed > 30*time.Second {
		t.Errorf("PA-AC16: the replay's derivation took %s, above its ceiling of 30 s", replayed.Round(time.Millisecond))
	}
	t.Logf("flows stored %d, of %d legs read; flow instants %d..%d; record starts %d..%d",
		collecttest.ScalarCount(t, h.Store, "SELECT count(*) FROM flow"), len(legs),
		collecttest.ScalarCount(t, h.Store, "SELECT coalesce(min(observed_at), 0) FROM flow"),
		collecttest.ScalarCount(t, h.Store, "SELECT coalesce(max(observed_at), 0) FROM flow"),
		collecttest.ScalarCount(t, h.Store, "SELECT coalesce(min(start_at), 0) FROM flow_record"),
		collecttest.ScalarCount(t, h.Store, "SELECT coalesce(max(start_at), 0) FROM plugin_netflow.flow_record"))

	type outcome struct{ records, octets int64 }
	table := map[string]*outcome{}
	add := func(name string, octets int64) {
		if table[name] == nil {
			table[name] = &outcome{}
		}
		table[name].records++
		table[name].octets += octets
	}
	var total outcome
	for _, record := range h.Records(t) {
		total.records++
		total.octets += record.Octets
		name := record.JoinState
		switch record.JoinState {
		case "copy":
			name = "copy: " + OrNull(record.CopyKind)
			if record.CountedCopy == 1 {
				name += " (counted)"
			}
		case "unmatched":
			name = "unmatched: " + OrNull(record.Reason)
		}
		add(name, record.Octets)
		add("ingress role: "+record.IngressRole, record.Octets)
		add("egress role: "+record.EgressRole, record.Octets)
	}
	states := map[string]int64{}
	for _, volume := range h.Volumes(t) {
		states[volume.State]++
	}
	var lines []string
	for name, value := range table {
		lines = append(lines, fmt.Sprintf("| %s | %d | %.1f %% | %d | %.1f %% |", name, value.records,
			100*float64(value.records)/float64(total.records), value.octets, 100*float64(value.octets)/float64(total.octets)))
	}
	sort.Strings(lines)
	copies := int64(0)
	copyOctets := int64(0)
	for name, value := range table {
		if strings.HasPrefix(name, "copy: ") {
			copies += value.records
			copyOctets += value.octets
		}
	}
	report := fmt.Sprintf("records %d, octets %d\n\n| outcome | records | share | octets | share |\n|---|---|---|---|---|\n%s\n",
		total.records, total.octets, strings.Join(lines, "\n"))
	if joined := table["joined"]; joined != nil {
		report += fmt.Sprintf("\nexcluding copies: joined %.1f %% of records, %.1f %% of octets\n",
			100*float64(joined.records)/float64(total.records-copies),
			100*float64(joined.octets)/float64(total.octets-copyOctets))
		for _, name := range []string{"unmatched: ambiguous", "pending"} {
			if value := table[name]; value != nil {
				report += fmt.Sprintf("excluding copies: %s %.1f %% of records, %.1f %% of octets\n", name,
					100*float64(value.records)/float64(total.records-copies),
					100*float64(value.octets)/float64(total.octets-copyOctets))
			}
		}
	}
	var stateLines []string
	for state, count := range states {
		stateLines = append(stateLines, fmt.Sprintf("%s %d", state, count))
	}
	sort.Strings(stateLines)
	report += "\ndirection states: " + strings.Join(stateLines, ", ") + "\n"
	// The unmatched records by the interfaces their indexes name and their protocol: what the analysis's
	// "no leg: other" is compared with.
	breakdown, err := h.Store.DB().QueryContext(context.Background(),
		`SELECT join_state || ':' || coalesce(unmatched_reason, coalesce(copy_kind, '')) || ' ' ||
		        coalesce(ingress_identifier, ingress_role) || '>' || coalesce(egress_identifier, egress_role) ||
		        ' p' || protocol || CASE WHEN protocol = 6 AND tcp_flags & 2 = 0 THEN ' nosyn' ELSE '' END,
		        count(*), sum(octets)
		 FROM plugin_netflow.flow_record WHERE join_state <> 'joined' GROUP BY 1 ORDER BY 2 DESC LIMIT 40`)
	if err != nil {
		t.Fatal(err)
	}
	report += "\n| not joined, by interfaces and protocol | records | octets |\n|---|---|---|\n"
	for breakdown.Next() {
		var name string
		var records, octets int64
		if err := breakdown.Scan(&name, &records, &octets); err != nil {
			t.Fatal(err)
		}
		report += fmt.Sprintf("| %s | %d | %d |\n", name, records, octets)
	}
	_ = breakdown.Close()
	report += explainUnmatched(t, h)
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, []byte(report), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + report)
	got := recordingRelations(t, h)
	if outcomes := os.Getenv("OPNVIEW_NJ_REPLAY_OUTCOMES"); outcomes != "" {
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(outcomes, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// PA-AC16: the per-record outcome file equals the Go join's, record for record, but for what N2, N3 and D5
	// change on purpose.
	if goOutcomes := os.Getenv("OPNVIEW_NJ_REPLAY_GO_OUTCOMES"); goOutcomes != "" {
		recorded := loadRecordingFile(t, goOutcomes)
		changed := compareRecording(t, goOutcomes, recorded, got, recordingExpectations())
		for class, count := range changed {
			t.Logf("expected change, %s: %d rows", class, count)
		}
	}
}

type replayFrame struct {
	received int64
	payload  []byte
}

// readFrames reads the capture's NetFlow frames.
func readFrames(t *testing.T, path string) []replayFrame {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var frames []replayFrame
	for len(raw) >= 26 {
		received := int64(binary.BigEndian.Uint64(raw[:8]))
		length := int(binary.BigEndian.Uint16(raw[24:26]))
		if len(raw) < 26+length {
			break
		}
		frames = append(frames, replayFrame{received: received / 1e9, payload: append([]byte(nil), raw[26:26+length]...)})
		raw = raw[26+length:]
	}
	return frames
}

type replayRecord struct {
	dst     netip.Addr
	in, out uint16
}

// v9Records reads, from one ng_netflow datagram, each data record's destination and indexes, by the
// template ids and field list the capture analysis found (Q1): enough to find the firewall's addresses.
func v9Records(payload []byte) []replayRecord {
	var records []replayRecord
	if len(payload) < 20 || binary.BigEndian.Uint16(payload) != 9 {
		return nil
	}
	sets := payload[20:]
	for len(sets) >= 4 {
		id, length := binary.BigEndian.Uint16(sets), int(binary.BigEndian.Uint16(sets[2:]))
		if length < 4 || length > len(sets) {
			break
		}
		body := sets[4:length]
		sets = sets[length:]
		size, addressLength := 0, 0
		switch id {
		case 256:
			size, addressLength = 57, 4
		case 259:
			size, addressLength = 93, 16
		default:
			continue
		}
		for len(body) >= size {
			dst, _ := netip.AddrFromSlice(body[addressLength : 2*addressLength])
			in := binary.BigEndian.Uint16(body[3*addressLength:])
			out := binary.BigEndian.Uint16(body[3*addressLength+2:])
			records = append(records, replayRecord{dst: dst.Unmap(), in: in, out: out})
			body = body[size:]
		}
	}
	return records
}

// readFilterlog reads the capture's filterlog lines into filter-log rows, at their RFC 5424 header
// time.
func readFilterlog(t *testing.T, path string) []LegRow {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	var legs []LegRow
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		tab := strings.IndexByte(text, '\t')
		if tab < 0 {
			continue
		}
		parts := strings.SplitN(text[tab+1:], " ", 8)
		if len(parts) < 8 || parts[3] != "filterlog" {
			continue
		}
		at, err := time.Parse(time.RFC3339, parts[1])
		if err != nil {
			continue
		}
		body := parts[7]
		if index := strings.Index(body, "] "); index >= 0 && strings.HasPrefix(body, "[") {
			body = body[index+2:]
		} else if strings.HasPrefix(body, "- ") {
			body = body[2:]
		}
		c := strings.Split(body, ",")
		if len(c) < 9 {
			continue
		}
		leg := LegRow{Digest: fmt.Sprintf("replay-%d", line), At: at.Unix(), Device: c[4], Action: c[6],
			Dir: c[7]}
		var rest []string
		switch c[8] {
		case "4":
			if len(c) < 20 {
				continue
			}
			leg.IPID, _ = strconv.Atoi(c[12])
			leg.ProtocolNumber, _ = strconv.Atoi(c[15])
			leg.Protocol, leg.Src, leg.Dst = c[16], c[18], c[19]
			rest = c[20:]
		case "6":
			if len(c) < 17 {
				continue
			}
			leg.Protocol = c[12]
			leg.ProtocolNumber, _ = strconv.Atoi(c[13])
			leg.Src, leg.Dst = c[15], c[16]
			rest = c[17:]
		default:
			continue
		}
		if (leg.ProtocolNumber == 6 || leg.ProtocolNumber == 17) && len(rest) >= 2 {
			leg.SrcPort, _ = strconv.Atoi(rest[0])
			leg.DstPort, _ = strconv.Atoi(rest[1])
			if leg.ProtocolNumber == 6 && len(rest) >= 5 {
				leg.Sequence, _ = strconv.Atoi(rest[4])
			}
		}
		if _, err := netip.ParseAddr(leg.Src); err != nil {
			continue
		}
		legs = append(legs, leg)
	}
	return legs
}
