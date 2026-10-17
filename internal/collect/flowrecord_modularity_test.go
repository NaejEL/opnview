package collect_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
	"github.com/NaejEL/opnview/internal/collect/providertest"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/push/ipfixregistry"
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

	insideForward, insideReverse, _, _ := collecttest.NatRecords()
	run := func(h *collecttest.NetflowHarness, readCapture func()) string {
		readCapture()
		h.At(collecttest.NetflowReadSecond)
		h.Deliver(t, collecttest.LegInstant+30, insideForward, insideReverse)
		h.Logged(t, collecttest.NatLegs("nat", collecttest.LegInstant, "pass")...)
		return collecttest.JoinFingerprint(t, h, "nat1", "nat2")
	}
	builtin := collecttest.NewNetflowHarness(t)
	want := run(builtin, func() {
		builtin.Capture(t, collecttest.CaptureStartAt, collecttest.Captured{Identifier: collecttest.InsideIdentifier})
	})

	state := map[string]any{"running": true, "active_timeout": 1800,
		"points": []any{map[string]any{"name": collecttest.InsideIdentifier, "receive": true, "send": true,
			"exported": true, "also_received_for": []any{"example_if_b"}}}}
	second := collecttest.NewNetflowHarnessWith(t, loaded, "example_exporter", func(fake *providertest.Firewall) {
		fake.AnswerJSON(exampleExporterEndpoint, state)
	})
	got := run(second, func() {
		second.At(collecttest.CaptureStartAt)
		if err := second.Collector.CollectFlowRecordCapture(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	// NJ-AC17: the second exporter reports exported and parent_of in its own words, and the core stores them.
	if rows := collecttest.ScalarCount(t, second.Store, `SELECT count(*) FROM capture_interface AS i
		JOIN capture_interface_parent_of AS p ON p.capture_configuration_id = i.capture_configuration_id
		  AND p.identifier = i.identifier
		WHERE i.identifier = $1 AND i.exported = 'yes' AND p.of_identifier = 'example_if_b'`,
		collecttest.InsideIdentifier); rows != 1 {
		t.Errorf("the second exporter's exported and parent_of were stored %d times, want once", rows)
	}
	if len(second.Records(t)) != 2 {
		t.Fatalf("the second exporter fed %d records, want 2 (the built-in's, not active, stores none)",
			len(second.Records(t)))
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

// TestTheCoreJoinReadsContractFieldsOnly is PS-AC47, extended to the core's join, coverage and state
// code, and by NJ-AC17 to the code of NJ4 and NJ5: it names no OPNsense endpoint, no option or field of an
// OPNsense model, no netgraph term, no Information Element, no device name and no address literal, and
// reads the contract's fields only.
func TestTheCoreJoinReadsContractFieldsOnly(t *testing.T) {
	t.Parallel()
	// The core's flow_record code is the join and the measured volume (derive), the kind's coordinator
	// (kinds/flowrecords) and the state they share (core).
	files := []string{filepath.Join("derive", "flowrecord.go"), filepath.Join("derive", "measuredvolume.go"),
		filepath.Join("derive", "join.go"),
		filepath.Join("kinds", "flowrecords", "flowrecordkind.go"), filepath.Join("core", "state.go"),
		filepath.Join("provider", "pushed_flow_record.go"),
		filepath.Join("..", "store", "netflow", "flowrecord.go"), filepath.Join("..", "store", "netflow", "capture.go"),
		filepath.Join("..", "store", "netflow", "measuredvolume.go"),
		filepath.Join("..", "store", "netflow", "flowrecord.statements.sql")}
	forbidden := []*regexp.Regexp{regexp.MustCompile(`getconfig`), regexp.MustCompile(`is_enabled`),
		regexp.MustCompile(`capture\.interfaces`), regexp.MustCompile(`egress_only`), regexp.MustCompile(`\bconf\b`),
		regexp.MustCompile(`/api/`), regexp.MustCompile(`macaddr`), regexp.MustCompile(`vlan_tag|vlan\.parent`),
		regexp.MustCompile(`(?i)netgraph|ng_netflow|ng_ether|ksocket|samplicate`),
		// A device name: a driver's letters and a unit number, as FreeBSD names an interface.
		regexp.MustCompile(`\b(?:re|igc|igb|em|ix|ixl|vtnet|bge|vlan|wg|lo|lagg|bridge|ovpn[cs]?|tun|tap|gif|gre|pppoe)\d+\b`),
		// An address literal, of either family.
		regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`),
		regexp.MustCompile(`(?i)\b[0-9a-f]{1,4}::[0-9a-f]{0,4}\b|\b[0-9a-f]{1,4}(?::[0-9a-f]{1,4}){3,7}\b`)}
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
