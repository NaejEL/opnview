package collect

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/push/ipfixregistry"
	"github.com/NaejEL/opnview/internal/store/netflow"
)

// The flow_record kind is a seam: a second exporter is a plugin directory, and the core's join,
// coverage and states read contract fields only (PS-AC47, PS-AC48 and PS-AC50 of the NetFlow join
// amendment; CLAUDE.md, "Modular at every layer").

// exampleExporterEndpoint is the test-only exporter's own configuration read.
var exampleExporterEndpoint = opnsense.Endpoint{Path: "/api/example/exporter/state", Method: "GET"}

// copyDirectory copies a directory tree, file by file: the only way the test adds the exporter.
func copyDirectory(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestASecondExporterIsAddedByItsDirectoryAlone is PS-AC48: a test-only provider of flow_record, a
// plugin directory holding a manifest, an Information Element mapping and a capture-configuration read
// against the fake firewall, is found by the loader from its directory alone; it feeds flow_record, and
// on the NAT fixture its records join and give exactly the figures the built-in's give. No Go file, no
// file of plugins/ and no core registry names it.
func TestASecondExporterIsAddedByItsDirectoryAlone(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	copyDirectory(t, filepath.Join("testdata", "example_exporter"),
		filepath.Join(dataDir, plugin.PluginsDirectory, "example_exporter"))
	loaded, err := plugin.Load(context.Background(), plugin.Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	for _, refusal := range loaded.Refusals() {
		t.Errorf("refused: %+v", refusal)
	}
	found, ok := loaded.Plugin("example_exporter")
	if !ok || found.Origin != plugin.OriginDataDirectory {
		t.Fatalf("the loader did not find the exporter in the data directory")
	}

	insideForward, insideReverse, _, _ := natRecords()
	run := func(h *netflowHarness, readCapture func()) string {
		readCapture()
		h.at(netflowReadSecond)
		h.deliver(t, legInstant+30, insideForward, insideReverse)
		h.logged(t, natLegs("nat", legInstant, "pass")...)
		return joinFingerprint(t, h, "nat1", "nat2")
	}
	builtin := newNetflowHarness(t)
	want := run(builtin, func() { builtin.capture(t, captureStartAt, captured{identifier: insideIdentifier}) })

	state := map[string]any{"running": true, "active_timeout": 1800,
		"points": []any{map[string]any{"name": insideIdentifier, "receive": true, "send": true}}}
	second := newNetflowHarnessWith(t, loaded, "example_exporter", func(fake *providertest.Firewall) {
		fake.AnswerJSON(exampleExporterEndpoint, state)
	})
	got := run(second, func() {
		second.at(captureStartAt)
		if err := second.collector.CollectFlowRecordCapture(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	if len(second.records(t)) != 2 {
		t.Fatalf("the second exporter fed %d records, want 2 (the built-in's, not active, stores none)",
			len(second.records(t)))
	}
	if got != want {
		t.Errorf("the second exporter gives\n%s\nthe built-in gives\n%s", got, want)
	}

	// Nothing shipped names it: it was added by writing its directory, and nothing else.
	root := filepath.Join("..", "..")
	for _, tree := range []string{"cmd", "internal", "plugins"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(raw), "example_exporter") {
				t.Errorf("%s names the test-only exporter", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestTheDirectionAtTheObservationPointComesFromTheConfigurationAndTheIngressIndex is PS-AC50, rule 1:
// every case, egress-only included, and an egress index that never changes the result.
func TestTheDirectionAtTheObservationPointComesFromTheConfigurationAndTheIngressIndex(t *testing.T) {
	t.Parallel()
	index := func(value int64) *int64 { return &value }
	both := netflow.CaptureInterface{Identifier: insideIdentifier, Ingress: true, Egress: true}
	egressOnly := netflow.CaptureInterface{Identifier: insideIdentifier, Egress: true}
	for _, scenario := range []struct {
		name    string
		point   netflow.CaptureInterface
		ingress *int64
		want    string
	}{
		{"received on the point", both, index(insideIndex), netflow.DirectionIngress},
		{"received elsewhere", both, index(upstreamIndex), netflow.DirectionEgress},
		{"originated by the firewall (index 0, S6a)", both, index(0), netflow.DirectionEgress},
		{"no ingress index", both, nil, netflow.DirectionEgress},
		{"egress-only point, received on it", egressOnly, index(insideIndex), netflow.DirectionEgress},
		{"egress-only point, received elsewhere", egressOnly, index(upstreamIndex), netflow.DirectionEgress},
	} {
		if got := derive.DirectionAt(scenario.point, insideIndex, scenario.ingress); got != scenario.want {
			t.Errorf("%s: %s, want %s", scenario.name, got, scenario.want)
		}
	}
}

// TestAPolicyRoutedRecordJoinsByItsIngressIndexAndAContradictionJoinsNothing is PS-AC50's other two
// clauses: a record whose egress index is not the interface its leg left by -- policy routing -- joins
// as its ingress index says, the egress index changing nothing; and a record whose derived direction
// contradicts the only candidate's pf direction and orientation joins nothing.
func TestAPolicyRoutedRecordJoinsByItsIngressIndexAndAContradictionJoinsNothing(t *testing.T) {
	t.Parallel()
	at := legInstant
	h := newNetflowHarness(t)
	h.capture(t, captureStartAt, captured{identifier: insideIdentifier})
	h.at(netflowReadSecond)
	routed := forward(17, 46000, 53, 0, 7, at, at+1)
	routed.out = 4 // the route lookup's interface, not the upstream one the leg left by
	contradicting := forward(17, 46001, 53, 0, 8, at, at+1)
	contradicting.in = upstreamIndex // received elsewhere: egress at the point, forward: needs an `out` leg
	h.deliver(t, at+30, routed, contradicting)
	h.logged(t, singleLeg("routed", at, "udp", 17, 46000, 53, "pass"),
		singleLeg("contradicted", at, "udp", 17, 46001, 53, "pass"))
	records := byOctets(t, h)
	if record := records[7]; record.joinedFlowID == nil || *record.joinedFlowID != h.flowID(t, "routed") ||
		orNull(record.direction) != netflow.DirectionIngress {
		t.Errorf("the policy-routed record is %s %s, want ingress and joined", orNull(record.direction),
			record.joinState)
	}
	if record := records[8]; record.joinState != netflow.JoinUnmatched ||
		orNull(record.reason) != netflow.UnmatchedNoLoggedLeg {
		t.Errorf("the contradicting record is %s (%s), want unmatched", record.joinState, orNull(record.reason))
	}
}

// TestTheCoreJoinReadsContractFieldsOnly is PS-AC47, extended to the core's join, coverage and state
// code: it names no OPNsense endpoint, no option of OPNsense's NetFlow model and no Information Element,
// and reads the contract's fields only.
func TestTheCoreJoinReadsContractFieldsOnly(t *testing.T) {
	t.Parallel()
	// The core's flow_record code is the join and the measured volume (derive), the kind's coordinator
	// (kinds/flowrecords) and the state they share (core).
	files := []string{filepath.Join("derive", "flowrecord.go"), filepath.Join("derive", "measuredvolume.go"),
		filepath.Join("kinds", "flowrecords", "flowrecordkind.go"), filepath.Join("core", "state.go"),
		filepath.Join("flowrecord", "flowrecord.go"),
		filepath.Join("..", "store", "netflow", "flowrecord.go"),
		filepath.Join("..", "store", "netflow", "flowrecord.statements.sql")}
	forbidden := []*regexp.Regexp{regexp.MustCompile(`getconfig`), regexp.MustCompile(`is_enabled`),
		regexp.MustCompile(`capture\.interfaces`), regexp.MustCompile(`egress_only`), regexp.MustCompile(`\bconf\b`),
		regexp.MustCompile(`/api/`)}
	for id := 0; id <= 0xFFFF; id++ {
		if element, known := ipfixregistry.Lookup(uint16(id)); known {
			forbidden = append(forbidden, regexp.MustCompile(`\b`+regexp.QuoteMeta(element.Name)+`\b`))
		}
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, pattern := range forbidden {
			if location := pattern.FindIndex(raw); location != nil {
				t.Errorf("%s names %q, which is a provider's, not the core's", file, raw[location[0]:location[1]])
			}
		}
	}
}
