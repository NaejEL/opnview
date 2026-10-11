package push_test

import (
	"encoding/json"
	"io/fs"
	"math"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/discover"
	"github.com/NaejEL/opnview/internal/push"
)

// TestThePushPackagesHoldNoProductKnowledge is PS-AC2's first half: no non-test Go file of the push
// packages names a program a built-in plugin subscribes to, a field of a program's message, or
// compiles a regular expression -- a program's format is its plugin's.
func TestThePushPackagesHoldNoProductKnowledge(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	// The programs the built-in plugins subscribe to, read from their manifests.
	forbidden := map[string]bool{}
	manifests, err := filepath.Glob(filepath.Join(root, "plugins", "*", "plugin.json"))
	if err != nil || len(manifests) == 0 {
		t.Fatalf("no manifest found: %v", err)
	}
	for _, path := range manifests {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct {
			Push []struct {
				Filter struct {
					Program []string `json:"program"`
				} `json:"filter"`
			} `json:"push"`
		}
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatal(err)
		}
		for _, entry := range manifest.Push {
			for _, program := range entry.Filter.Program {
				forbidden[program] = true
			}
		}
	}
	if !forbidden["filterlog"] {
		t.Fatal("the built-in programs were not read, so this assertion is vacuous")
	}
	// Fields of the programs' messages (survey, Push sources, S4, S7, S8) and the products'.
	for _, word := range []string{"rulenr", "subrulenr", "anchorname", "protoname", "protonum", "srcport", "dstport",
		"tcpflags", "rcode", "suricata", "sni", "eve", "unbound", "kea", "opnsense"} {
		forbidden[word] = true
	}
	var words []string
	for word := range forbidden {
		words = append(words, regexp.QuoteMeta(word))
	}
	pattern := regexp.MustCompile(`(?i)\b(` + strings.Join(words, "|") + `)\b`)
	err = filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for number, line := range strings.Split(string(raw), "\n") {
			code := line
			if comment := strings.Index(code, "//"); comment >= 0 {
				code = code[:comment]
			}
			if match := pattern.FindString(code); match != "" {
				t.Errorf("%s:%d names %q, which is a product's, not a protocol's", path, number+1, match)
			}
			if strings.Contains(code, "regexp.") {
				t.Errorf("%s:%d compiles a regular expression: a message's format is its plugin's", path, number+1)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestTheGeneratedLinkFileIsCurrent: the one file naming the protocols is generated from the tree.
func TestTheGeneratedLinkFileIsCurrent(t *testing.T) {
	t.Parallel()
	if err := discover.CheckDirective(filepath.Join("protocols", "protocols.go")); err != nil {
		t.Error(err)
	}
}

// TestEveryProtocolDirectoryRegistersOne: a protocol.json and a registration, in both directions.
func TestEveryProtocolDirectoryRegistersOne(t *testing.T) {
	t.Parallel()
	markers, err := filepath.Glob(filepath.Join("protocols", "*", "protocol.json"))
	if err != nil || len(markers) == 0 {
		t.Fatalf("no protocol directory: %v", err)
	}
	for _, marker := range markers {
		raw, err := os.ReadFile(marker)
		if err != nil {
			t.Fatal(err)
		}
		var declared struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &declared); err != nil {
			t.Fatal(err)
		}
		if _, registered := push.Lookup(declared.Name); !registered {
			t.Errorf("%s declares %q, which no init() registers", marker, declared.Name)
		}
	}
}

// TestValuesConvertOrAreRefused is PS-AC11 at the type layer: every vocabulary type converts what it
// should, and an unsigned 64-bit value above the signed range is refused, never wrapped.
func TestValuesConvertOrAreRefused(t *testing.T) {
	t.Parallel()
	accepted := []struct {
		columnType string
		value      any
		want       any
	}{
		{push.TypeInteger, uint64(math.MaxInt64), int64(math.MaxInt64)},
		{push.TypeInteger, "42", int64(42)},
		{push.TypeInteger, float64(7), int64(7)},
		{push.TypeReal, "0.5", 0.5},
		{push.TypeText, []byte{0xab, 0x01}, "ab01"},
		{push.TypeBoolean, "true", true},
		{push.TypeIP, "2001:db8::1", netip.MustParseAddr("2001:db8::1")},
		{push.TypeIP, "::ffff:192.0.2.1", netip.MustParseAddr("192.0.2.1")},
		{push.TypeInstant, float64(1_790_467_200.5), time.Unix(1_790_467_200, 500_000_000).UTC()},
		{push.TypeInstant, "2026-09-27T00:00:00+02:00", time.Date(2026, 9, 26, 22, 0, 0, 0, time.UTC)},
	}
	for _, c := range accepted {
		got, err := push.Convert(c.columnType, c.value)
		if err != nil {
			t.Errorf("%s %v: %v", c.columnType, c.value, err)
			continue
		}
		if instant, isTime := got.(time.Time); isTime {
			if !instant.Equal(c.want.(time.Time)) {
				t.Errorf("%s %v = %v", c.columnType, c.value, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("%s %v = %v (%T), want %v", c.columnType, c.value, got, got, c.want)
		}
	}
	mac, err := push.Convert(push.TypeMAC, "00:00:5e:00:53:01")
	if err != nil || mac.(net.HardwareAddr).String() != "00:00:5e:00:53:01" {
		t.Errorf("a MAC address converted to %v, %v", mac, err)
	}
	refused := []struct {
		columnType string
		value      any
	}{
		{push.TypeInteger, uint64(math.MaxInt64) + 1}, {push.TypeInteger, 1.5}, {push.TypeInteger, "x"},
		{push.TypeReal, "nan"}, {push.TypeBoolean, "1"}, {push.TypeIP, "not an address"}, {push.TypeMAC, "zz"},
		{push.TypeInstant, "2026-09-27T00:00:00"}, {push.TypeText, struct{}{}},
	}
	for _, c := range refused {
		if got, err := push.Convert(c.columnType, c.value); err == nil {
			t.Errorf("%s %v converted to %v", c.columnType, c.value, got)
		}
	}
}
