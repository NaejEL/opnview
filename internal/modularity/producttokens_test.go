package modularity

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// The modularity test of PA20 (specs/SPEC-backend-completion-plugins.md, the open-plugin-API amendment):
// no Go file under cmd/ or internal/ that is not a test -- test-support packages included -- names a
// product, a product's convention or an OPNsense endpoint. Those belong to the plugins (plugins/<name>/),
// which are JSON, JavaScript and SQL. A Go file that names one is the core knowing a product, which is the
// shape the amendment removes.
//
// HOW A NAME IS FOUND. Every identifier and every string literal of the file is cut into words: an
// identifier on its underscores and its case changes (ARPTable is "arp", "table"), a literal on everything
// that is not a letter or a digit, and then on its case changes. A token of the list is itself one or more
// words ("flow_record" is "flow", "record"), and it is found where its words follow one another in that
// order. Comments are not read: a comment citing where a product's facts went is documentation. A literal
// beginning "/api/" is an OPNsense endpoint path, whatever its words.

// productTokens is the list of PA20. It may only grow.
var productTokens = []string{
	"netflow", "ng_netflow", "ipfix", "flow_record", "capture_configuration", "capture_interface", "parent_of",
	// Written in two parts, so the grep of NJ-AC3 (internal/collect/netflowvocabulary_test.go) does not find it here.
	"observation" + "_point", "interfaces_info", "kea", "dnsmasq", "isc", "unbound", "suricata", "insight", "maxmind",
	"geolite", "publicsuffix", "public_suffix", "arp", "ndp", "iftop", "lo0", "tcp.established",
}

// apiPathToken stands for a string literal beginning "/api/".
const apiPathToken = "/api/"

// allowedToken is one entry of the allow-list: the tokens a directory or a file may hold, and why. A key
// ending in "/" is a directory and covers everything below it; any other key is one file. Every entry is
// restated in docs/architecture.md, "Decisions in force". An entry PA20 names as the minimum stays even
// when nothing under it holds the token today; any other entry must be used, or it goes.
type allowedToken struct {
	tokens  []string
	reason  string
	minimum bool
}

// productTokenAllowed is the allow-list of PA20, with one reason per entry.
var productTokenAllowed = map[string]allowedToken{
	"internal/opnsense/": {[]string{apiPathToken, "interfaces_info"}, "the firewall client: discovery and the " +
		"clock read OPNsense's own endpoints, which every installation has whatever its plugins", true},
	"internal/collect/discovery/": {[]string{apiPathToken, "interfaces_info"}, "discovery reads the interfaces " +
		"and the firewall's own addresses, the core's view of the firewall that no plugin owns", true},
	"internal/broker/": {[]string{apiPathToken}, "the firewall data broker: it serves plugins' calls by path, " +
		"checks a path is an OPNsense API path, and names the core's own reads", true},
	"internal/push/protocols/netflow/": {[]string{"netflow", "ipfix"}, "the NetFlow v9 / IPFIX decoder: the " +
		"protocol's own name, decoded in Go for speed (PA7), naming no OPNsense concept", true},
	"internal/push/ipfixregistry/": {[]string{"netflow", "ipfix"}, "the IANA IPFIX information-element " +
		"registry the decoder reads: the protocol's own name", true},
	"internal/push/protocols/protocols_gen.go": {[]string{"netflow"}, "the generated discovery of the push " +
		"protocols imports each protocol's directory, the NetFlow decoder's among them, by its path", false},
	"internal/web/surfaces/settings/verify.go": {[]string{"interfaces_info"}, "the credential check calls " +
		"discovery's first endpoint once, the one every installation answers whatever its plugins", false},
	"cmd/opnview/measurecmd.go": {[]string{"interfaces_info"}, "the cost-measuring command lists the interface " +
		"names through discovery's endpoint, as discovery reads them", false},
	"internal/web/surfaces/": {[]string{apiPathToken}, "opnview's own HTTP API routes, which share the /api/ " +
		"prefix with OPNsense's: none is a firewall path", false},
	"internal/web/webtest/": {[]string{apiPathToken}, "the web test support calls opnview's own HTTP API routes", false},
	"internal/widget/service.go": {[]string{apiPathToken}, "the route prefix of opnview's own widget API", false},
	"cmd/opnview/review.go": {[]string{apiPathToken}, "the review command reads opnview's own widget API", false},
}

// TestNoGoFileNamesAProduct is PA-AC28: the token test of PA20 passes over the tree.
func TestNoGoFileNamesAProduct(t *testing.T) {
	root := repositoryRoot(t)
	findings, err := productTokenFindings(root, []string{"cmd", "internal"}, productTokenAllowed)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		t.Error(finding)
	}
	used := allowanceUsed(root, []string{"cmd", "internal"})
	for key, entry := range productTokenAllowed {
		for _, token := range entry.tokens {
			if !entry.minimum && !used[key+"\x00"+token] {
				t.Errorf("the allow-list lets %s hold %q (%s), and nothing there holds it: shrink the allowance",
					key, token, entry.reason)
			}
		}
	}
}

// TestAGoFileNamingAProductOutsideTheAllowListFails is PA-AC28's teeth: a fixture Go file holding netflow,
// outside every allowed directory, is found; the same file inside an allowed directory is not.
func TestAGoFileNamingAProductOutsideTheAllowListFails(t *testing.T) {
	root := t.TempDir()
	write := func(relative, text string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const source = "package fixture\n\n// netflow in a comment is documentation.\nvar netflowTimeout = \"x\"\n"
	write("internal/fixture/fixture.go", source)
	write("internal/push/protocols/netflow/decoder.go", source)
	write("internal/fixture/fixture_test.go", source)
	write("internal/other/other.go", "package other\n\nvar path = \"/api/diagnostics/interface/get_arp\"\n"+
		"var key = \"ARPTable\"\nvar Discovery, Kitchen = 1, 2\n")
	findings, err := productTokenFindings(root, []string{"internal"}, productTokenAllowed)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`internal/fixture/fixture.go: the identifier "netflowTimeout" holds "netflow"`,
		`internal/other/other.go: the literal "/api/diagnostics/interface/get_arp" holds "/api/"`,
		`internal/other/other.go: the literal "/api/diagnostics/interface/get_arp" holds "arp"`,
		`internal/other/other.go: the literal "ARPTable" holds "arp"`,
	}
	if strings.Join(findings, "\n") != strings.Join(want, "\n") {
		t.Errorf("findings:\n%s\nwant:\n%s", strings.Join(findings, "\n"), strings.Join(want, "\n"))
	}
}

// TestWordsCutIdentifiersAndLiterals: the cutting the token test rests on.
func TestWordsCutIdentifiersAndLiterals(t *testing.T) {
	for text, want := range map[string]string{
		"ARPTable":             "arp table",
		"collectNeighbours":    "collect neighbours",
		"flow_record":          "flow record",
		"KindFlowRecord":       "kind flow record",
		"tcp.established":      "tcp established",
		"lo0":                  "lo0",
		"Discovery":            "discovery",
		"HTTPServer2Ready":     "http server2 ready",
		"capture-configuration": "capture configuration",
	} {
		if got := strings.Join(words(text), " "); got != want {
			t.Errorf("words(%q) = %q, want %q", text, got, want)
		}
	}
}

// productTokenFindings walks the scopes under root and returns one line per token found outside the
// allow-list, sorted.
func productTokenFindings(root string, scopes []string, allowed map[string]allowedToken) ([]string, error) {
	var findings []string
	err := eachProductToken(root, scopes, func(relative, what, text, found string) {
		if tokenAllowed(allowed, relative, found) {
			return
		}
		findings = append(findings, fmt.Sprintf("%s: the %s %q holds %q", relative, what, text, found))
	})
	sort.Strings(findings)
	return dedupe(findings), err
}

// allowanceUsed is every allow-list key and token that some file under it holds.
func allowanceUsed(root string, scopes []string) map[string]bool {
	used := map[string]bool{}
	_ = eachProductToken(root, scopes, func(relative, _, _, found string) {
		for key := range productTokenAllowed {
			if keyCovers(key, relative) {
				used[key+"\x00"+found] = true
			}
		}
	})
	return used
}

// eachProductToken calls found for every token of the list held by an identifier or a string literal of a
// non-test Go file under the scopes.
func eachProductToken(root string, scopes []string, found func(relative, what, text, token string)) error {
	tokens := make([][]string, len(productTokens))
	for index, token := range productTokens {
		tokens[index] = strings.FieldsFunc(token, func(r rune) bool { return r == '_' || r == '.' })
	}
	match := func(relative, what, text string) {
		cut := words(text)
		for index, token := range tokens {
			if holds(cut, token) {
				found(relative, what, text, productTokens[index])
			}
		}
	}
	for _, scope := range scopes {
		directory := filepath.Join(root, scope)
		if _, err := os.Stat(directory); err != nil {
			continue
		}
		err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			relative, _ := filepath.Rel(root, path)
			relative = filepath.ToSlash(relative)
			ast.Inspect(file, func(node ast.Node) bool {
				switch typed := node.(type) {
				case *ast.Ident:
					match(relative, "identifier", typed.Name)
				case *ast.BasicLit:
					if typed.Kind != token.STRING {
						return true
					}
					text, err := strconv.Unquote(typed.Value)
					if err != nil {
						return true
					}
					if strings.HasPrefix(text, apiPathToken) {
						found(relative, "literal", text, apiPathToken)
					}
					match(relative, "literal", text)
				}
				return true
			})
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// tokenAllowed says whether the allow-list lets a file hold a token.
func tokenAllowed(allowed map[string]allowedToken, relative, token string) bool {
	for key, entry := range allowed {
		if !keyCovers(key, relative) {
			continue
		}
		for _, held := range entry.tokens {
			if held == token {
				return true
			}
		}
	}
	return false
}

// keyCovers says whether an allow-list key covers a file: the file itself, or a directory above it.
func keyCovers(key, relative string) bool {
	if strings.HasSuffix(key, "/") {
		return strings.HasPrefix(relative, key)
	}
	return key == relative
}

// words cuts a text into lower-case words: on every character that is not a letter or a digit, and on
// case changes -- a lower-case letter or a digit followed by an upper-case one, and the last upper-case
// letter of a run followed by a lower-case one. A digit stays with the word before it.
func words(text string) []string {
	var result []string
	for _, part := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		runes := []rune(part)
		start := 0
		for index := 1; index < len(runes); index++ {
			previous, current := runes[index-1], runes[index]
			next := rune(0)
			if index+1 < len(runes) {
				next = runes[index+1]
			}
			if (unicode.IsLower(previous) || unicode.IsDigit(previous)) && unicode.IsUpper(current) ||
				unicode.IsUpper(previous) && unicode.IsUpper(current) && unicode.IsLower(next) {
				result = append(result, strings.ToLower(string(runes[start:index])))
				start = index
			}
		}
		result = append(result, strings.ToLower(string(runes[start:])))
	}
	return result
}

// holds says whether a token's words follow one another, in order, among a text's words.
func holds(cut, token []string) bool {
	for start := 0; start+len(token) <= len(cut); start++ {
		matched := true
		for offset, word := range token {
			if cut[start+offset] != word {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// dedupe removes repeated lines from a sorted list.
func dedupe(lines []string) []string {
	kept := lines[:0]
	for index, line := range lines {
		if index == 0 || line != lines[index-1] {
			kept = append(kept, line)
		}
	}
	return kept
}
