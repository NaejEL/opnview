package modularity

import (
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/NaejEL/opnview/internal/store/vocab"
)

// measureNameAllowed are the paths -- a file, or a directory and everything under it -- whose code may
// name a declared measure, and why. Everything else of the core -- its Go code (identifiers, split on
// case and underscores, and string literals), its SQL outside comments, its statement names and its enum
// values -- names none (PA-AC7 of the plugin-API amendment): a measure is a plugin's declaration. The
// list may only shrink.
var measureNameAllowed = map[string]string{
	// The Go NetFlow join's paths left this list with the join in Lot 3 (PA9); these two name the protocol.
	"internal/collect/collecttest/": "the fake firewall's endpoint names, insight's netflow_is_enabled among them",
	"internal/store/schema.sql":     "the push gap reasons named after the NetFlow protocol",
	// The protocol, not the measure: NetFlow v9 and IPFIX are push protocols, named after their RFCs.
	"internal/push/protocols/": "the NetFlow push protocol's directory and the generated list linking it " +
		"(PA20's allow-list)",
	"internal/store/vocab/rows.go": "the push gap reasons netflow_sequence_gap and netflow_template_missing, " +
		"named after the protocol",
	"internal/opnsense/endpoints.go":             "the compiled registry's OPNsense NetFlow endpoints (/api/diagnostics/netflow)",
	"cmd/opnview/main.go":                        "the start, which names the NetFlow protocol's listener",
	"internal/web/messages/messages_registry.go": "the catalogue keys of the NetFlow protocol's listener",
	"internal/plugin/harness/testpush.go":        "the plugin test world's NetFlow protocol fixtures",
}

// TestNoCoreCodeNamesADeclaredMeasure is the grep of PA-AC7: no core Go or SQL names a declared measure
// outside the allowed paths.
func TestNoCoreCodeNamesADeclaredMeasure(t *testing.T) {
	root := repositoryRoot(t)
	measures := vocab.Measures()
	if len(measures) == 0 {
		t.Fatal("no measure is declared: the test reads nothing")
	}
	names := map[string]bool{}
	for _, measure := range measures {
		names[measure.Name] = true
	}
	allowed := func(relative string) bool {
		for path := range measureNameAllowed {
			if relative == path || (strings.HasSuffix(path, "/") && strings.HasPrefix(relative, path)) {
				return true
			}
		}
		return false
	}
	var found []string
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			relative, _ := filepath.Rel(root, path)
			relative = filepath.ToSlash(relative)
			if entry.IsDir() || allowed(relative) || strings.HasSuffix(relative, "_test.go") ||
				strings.Contains(relative, "/testdata/") {
				return nil
			}
			var tokens []string
			switch {
			case strings.HasSuffix(relative, ".go"):
				tokens = goTokens(t, path)
			case strings.HasSuffix(relative, ".sql"):
				tokens = sqlTokens(t, path)
			default:
				return nil
			}
			for _, word := range tokens {
				if names[word] {
					found = append(found, relative+": "+word)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(found)
	if len(found) > 0 {
		t.Errorf("core code names a declared measure outside the allowed paths:\n%s", strings.Join(found, "\n"))
	}
}

// goTokens are the words of a Go file's code: its identifiers, split on case and underscores, and its
// string literals, whole and split on what is not a letter or a digit. Comments are not code.
func goTokens(t *testing.T, path string) []string {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	files := token.NewFileSet()
	file := files.AddFile(path, files.Base(), len(source))
	var reader scanner.Scanner
	reader.Init(file, source, nil, 0)
	var words []string
	for {
		_, kind, literal := reader.Scan()
		if kind == token.EOF {
			break
		}
		switch kind {
		case token.IDENT:
			words = append(words, identifierWords(literal)...)
		case token.STRING:
			text, err := strconv.Unquote(literal)
			if err != nil {
				text = literal
			}
			words = append(words, strings.ToLower(text))
			words = append(words, textWords(text)...)
		}
	}
	return words
}

// sqlTokens are the words of a SQL file outside its comment lines.
func sqlTokens(t *testing.T, path string) []string {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var words []string
	for _, line := range strings.Split(string(source), "\n") {
		if code, _, _ := strings.Cut(line, "--"); strings.TrimSpace(code) != "" {
			words = append(words, textWords(code)...)
			words = append(words, strings.FieldsFunc(strings.ToLower(code), func(r rune) bool {
				return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
			})...)
		}
	}
	return words
}

// identifierWords splits an identifier on case and underscores, lower-cased: NetflowState is netflow
// and state.
func identifierWords(identifier string) []string {
	var words []string
	var current []rune
	flush := func() {
		if len(current) > 0 {
			words = append(words, strings.ToLower(string(current)))
			current = nil
		}
	}
	runes := []rune(identifier)
	for index, r := range runes {
		switch {
		case r == '_':
			flush()
			continue
		case unicode.IsUpper(r) && index > 0 && (unicode.IsLower(runes[index-1]) ||
			(index+1 < len(runes) && unicode.IsLower(runes[index+1]) && unicode.IsUpper(runes[index-1]))):
			flush()
		}
		current = append(current, r)
	}
	flush()
	return words
}

// textWords splits text on everything that is not a letter or a digit, lower-cased.
func textWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}
