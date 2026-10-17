package collect_test

import (
	"encoding/json"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/collecttest"
)

// What the amendment "the NetFlow join with every interface captured" removed is gone, and its fixtures
// hold nothing of the analysed firewall (NJ-AC3, NJ-AC14, NJ-AC16 and NJ-AC18).

// thisFile is the file holding the patterns below, which every walk skips.
const thisFile = "flowrecord_vocabulary_test.go"

// TestTheSingleObservationPointIsGone is NJ-AC3's, NJ-AC14's and NJ-AC16's greps: no Observation Point
// column, state or field, no captured-interface count, no stream-condition table, no direction at a
// point, no unmapped-window statement and no not-joinable join state is left in the contract, the
// schema, the code, the plugins or the documents. RFC 7011's term itself stays, in prose, in
// *Vocabulary*'s row saying it is neither exported nor assigned.
func TestTheSingleObservationPointIsGone(t *testing.T) {
	t.Parallel()
	removed := []string{
		"observation" + "_point", "captured" + "_interfaces", "CapturedInter" + "faces", "flow_record" + "_condition",
		"FlowRecord" + "Condition" + "s(", "Direction" + "At(", "widget_flow_record" + "_unmapped", "not" + "_joinable",
		"NotJoin" + "able", "CaptureObservation" + "Point", "measured" + "_at_leg", "netflow_sequence" + "_interleaved",
	}
	root := filepath.Join("..", "..")
	for _, tree := range []string{"cmd", "internal", "plugins", "docs", "sql"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || entry.Name() == thisFile {
				return err
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(raw)
			for _, name := range removed {
				if strings.Contains(text, name) {
					t.Errorf("%s still names %s", path, name)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{filepath.Join(root, "README.md"), filepath.Join(root, "ROADMAP.md")} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range removed {
			if strings.Contains(string(raw), name) {
				t.Errorf("%s still names %s", file, name)
			}
		}
	}
	vocabulary, err := os.ReadFile(filepath.Join(root, "docs", "data-model.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(vocabulary), "| RFC 7011's **Observation Point** |") ||
		!strings.Contains(string(vocabulary), "not exported by OPNsense and not assigned by `opnview`") {
		t.Error("docs/data-model.md, *Vocabulary*, no longer says the Observation Point is neither exported nor assigned")
	}
}

// The analysed firewall's values (scratchpad/capture-analysis/REPORT.md, its configuration table and Q2),
// none of which a fixture may hold (X1). The networks are written as prefixes, the identifiers and
// devices as the strings a fixture would quote, the kernel indexes as numbers.
var (
	analysedNetworks = []string{"192.168.50.0/24", "172.16.0.0/24", "10.30.0.0/24", "192.168.1.0/24", "10.20.0.0/24",
		"10.40.0.0/24", "10.99.99.0/24"}
	analysedNames = []string{"lan", "wan", "opt1", "opt2", "opt3", "opt4", "opt5", "re0", "igc0", "ix0", "vlan01",
		"vlan02", "vlan03", "wg0", "lo0"}
	analysedIndexes = map[int64]bool{1: true, 3: true, 4: true, 5: true, 9: true, 10: true, 11: true, 12: true}
	// documentationNetworks are RFC 5737's and RFC 3849's: the only addresses a fixture holds.
	documentationNetworks = []string{"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32"}
)

// joinFixtureFiles are the fixtures NJ12 covers: the join's and the netflow plugin's.
func joinFixtureFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, pattern := range []string{filepath.Join(collecttest.JoinFixtureDirectory, "*.json"),
		filepath.Join("..", "..", "plugins", "netflow", "testdata", "*.json")} {
		found, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, found...)
	}
	if len(files) < 20 {
		t.Fatalf("only %d fixtures were found, so this check is not reaching them", len(files))
	}
	return files
}

// TestNoFixtureHoldsTheAnalysedFirewallsValues is NJ-AC18: every NJ12 fixture carries __provenance__,
// holds documentation addresses only, and none of the analysed firewall's addresses, identifiers,
// devices or kernel indexes.
func TestNoFixtureHoldsTheAnalysedFirewallsValues(t *testing.T) {
	t.Parallel()
	address := regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b|\b[0-9a-fA-F]{1,4}:[0-9a-fA-F:]*:[0-9a-fA-F:]*\b`)
	inside := func(value netip.Addr, networks []string) bool {
		for _, network := range networks {
			if netip.MustParsePrefix(network).Contains(value.Unmap()) {
				return true
			}
		}
		return false
	}
	for _, file := range joinFixtureFiles(t) {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var tree map[string]any
		if err := json.Unmarshal(raw, &tree); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if provenance, _ := tree["__provenance__"].(map[string]any); len(provenance) == 0 {
			t.Errorf("%s carries no __provenance__", file)
		}
		for _, text := range strings.Split(string(raw), "\"") {
			for _, name := range analysedNames {
				if text == name {
					t.Errorf("%s holds the analysed firewall's %q", file, name)
				}
			}
			for _, match := range address.FindAllString(text, -1) {
				value, err := netip.ParseAddr(match)
				if err != nil {
					continue
				}
				if inside(value, analysedNetworks) {
					t.Errorf("%s holds the analysed firewall's address %s", file, match)
				}
				if !inside(value, documentationNetworks) {
					t.Errorf("%s holds %s, which is not a documentation address (RFC 5737, RFC 3849)", file, match)
				}
			}
		}
		walkIndexes(t, file, tree)
	}
}

// walkIndexes fails on an interface index of the analysed firewall: a record's `in` or `out`, or a row's
// statistics.index.
func walkIndexes(t *testing.T, file string, node any) {
	t.Helper()
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			if key == "in" || key == "out" || key == "index" {
				number := int64(-1)
				switch v := value.(type) {
				case float64:
					number = int64(v)
				case string:
					if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
						number = parsed
					}
				}
				if analysedIndexes[number] {
					t.Errorf("%s holds the analysed firewall's kernel index %d as %s", file, number, key)
				}
			}
			walkIndexes(t, file, value)
		}
	case []any:
		for _, value := range typed {
			walkIndexes(t, file, value)
		}
	}
}
