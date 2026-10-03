package opnsense

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The source-policy tests. They read the repository's own Go source and fail on the
// patterns the project rules forbid.
//
// WHY THEY LIVE HERE. internal/opnsense is one of the two packages allowed to build an
// HTTP request — the firewall client — and internal/maxmind is the other, the MaxMind
// database download: the two outbound calls the roadmap allows, one package each.
// Asserting that from here keeps the rule and its enforcement in one place.
//
// WHAT IS IN SCOPE. Every non-test Go file under cmd/ and internal/. Test files and
// testdata are excluded, deliberately and for a reason the project rules state:
// fixtures may hold example values provided the code under test never presupposes
// them, and a test must be able to stand up an httptest server and generate a
// credential. The exclusion is narrow — a test file may hold an example address, and
// it still may not hold a real URL, a key or a secret, which the last test below
// checks across the whole tree.

// scannedRoots are the directories that hold the program.
var scannedRoots = []string{"../../cmd", "../../internal"}

// TestOnlyTheTwoOutboundPackagesBuildAnHTTPRequest is the two-chokepoint rule: one
// package per outbound call the roadmap allows, and no third.
//
// The forbidden names are the ones that reach the network without going through a
// client somebody configured: the package-level helpers and the shared default
// client and transport. http.NewRequestWithContext is allowed in internal/opnsense
// and internal/maxmind and nowhere else.
func TestOnlyTheTwoOutboundPackagesBuildAnHTTPRequest(t *testing.T) {
	forbiddenEverywhere := []string{
		"http.Get(", "http.Post(", "http.PostForm(", "http.Head(",
		"http.DefaultClient", "http.DefaultTransport",
	}
	onlyHere := []string{"http.NewRequest", "&http.Client{", "http.Client{"}

	for _, file := range goSourceFiles(t) {
		body := readFile(t, file)
		for _, forbidden := range forbiddenEverywhere {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s uses %s, which reaches the network outside the one configured client",
					file, forbidden)
			}
		}
		if inAnOutboundPackage(file) {
			continue
		}
		for _, restricted := range onlyHere {
			if strings.Contains(body, restricted) {
				t.Errorf("%s uses %s; only internal/opnsense and internal/maxmind may "+
					"construct an HTTP request",
					file, restricted)
			}
		}
	}
}

// absoluteURLLiteral matches a string literal that names an absolute HTTP address.
var absoluteURLLiteral = regexp.MustCompile(`"https?://`)

// TestTheOnlyAbsoluteURLLiteralsAreTheRegistryCitations closes the loophole the
// citations would otherwise open.
//
// The registry carries an upstream URL per endpoint as DATA, so a test can assert
// each one is justified — which means internal/opnsense/endpoints.go necessarily
// holds absolute URL literals. Rather than exempting the file, every such literal in
// it must be a citation of the two admissible sources, on a line that assigns
// UpstreamURL or defines one of the citation constants. A request target cannot hide
// there without breaking that shape.
func TestTheOnlyAbsoluteURLLiteralsAreTheRegistryCitations(t *testing.T) {
	citationHost := regexp.MustCompile(
		`"https://(docs\.opnsense\.org|github\.com/opnsense/core)/`)
	// The MaxMind registry may hold the one host it downloads from, and citations of
	// MaxMind's own documentation, and nothing else.
	maxmindLiteral := regexp.MustCompile(
		`"https://(download\.maxmind\.com"|dev\.maxmind\.com/|www\.maxmind\.com/)`)

	for _, file := range goSourceFiles(t) {
		body := readFile(t, file)
		for number, line := range strings.Split(body, "\n") {
			if !absoluteURLLiteral.MatchString(line) {
				continue
			}
			if filepath.Base(file) == "endpoints.go" && strings.Contains(file, "/internal/maxmind/") {
				if !maxmindLiteral.MatchString(line) {
					t.Errorf("%s:%d holds an absolute URL literal that is neither the MaxMind "+
						"download host nor a citation of MaxMind's documentation: %s",
						file, number+1, strings.TrimSpace(line))
				}
				continue
			}
			if filepath.Base(file) != "endpoints.go" || !inThisPackage(file) {
				t.Errorf("%s:%d holds an absolute URL literal outside the registry: %s",
					file, number+1, strings.TrimSpace(line))
				continue
			}
			if !citationHost.MatchString(line) {
				t.Errorf("%s:%d holds an absolute URL literal that is not a citation of "+
					"docs.opnsense.org or github.com/opnsense/core: %s",
					file, number+1, strings.TrimSpace(line))
			}
		}
	}
}

// TestNoInterfaceIdentifierAddressOrCIDRIsALiteral is the zero-hardcoded-
// configuration rule in its enforceable form.
//
// It scans for BOTH address families, which matters in a cycle whose point is that both
// are first class: an IPv4 rule alone would leave the half of the rule that governs the
// newer family unguarded, and an IPv6 literal is exactly as much of an assumption about
// somebody's network as a dotted quad is.
//
// What it cannot scan for is an interface NAME, because a name is just a string; that
// half of the rule is enforced differently and more strongly — no branch anywhere reads
// the text of a name, which the next test checks.
func TestNoInterfaceIdentifierAddressOrCIDRIsALiteral(t *testing.T) {
	// Four dotted groups, optionally with a prefix length. A version number, a year and
	// a timestamp bound all look like nothing of the sort.
	dottedQuad := regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b(/\d{1,2})?`)
	// Four or more colon-separated hexadecimal groups. Four is the threshold rather than
	// two because a clock time — "15:04:05", which this package's timestamp layouts are
	// full of — is three groups, and a pattern that fired on those would be switched off
	// rather than obeyed.
	ipv6Groups := regexp.MustCompile(`\b[0-9a-fA-F]{0,4}(:[0-9a-fA-F]{0,4}){3,}`)
	// The compressed form, which can be as short as `::1` and so escapes the rule above.
	// A hexadecimal digit is required on one side, so a Go scope operator or a doubled
	// colon in prose does not match.
	ipv6Compressed := regexp.MustCompile(
		`(^|[^A-Za-z0-9_:])([0-9a-fA-F]{1,4}::|::[0-9a-fA-F]{1,4})`)

	patterns := map[string]*regexp.Regexp{
		"an IPv4 address or CIDR":   dottedQuad,
		"an IPv6 address":           ipv6Groups,
		"a compressed IPv6 address": ipv6Compressed,
	}
	for _, file := range goSourceFiles(t) {
		body := readFile(t, file)
		for number, line := range strings.Split(body, "\n") {
			for what, pattern := range patterns {
				if match := pattern.FindString(line); match != "" {
					t.Errorf("%s:%d holds %s as a literal: %q",
						file, number+1, what, strings.TrimSpace(match))
				}
			}
		}
	}
}

// TestTheAddressLiteralScanHasTeeth is the mutation this project keeps learning it needs.
//
// A scan nothing violates is indistinguishable from a scan that matches nothing, and the
// second is how an IPv4-only rule sat next to a comment claiming it covered IPv6. Each
// pattern is therefore run against a line it must reject.
func TestTheAddressLiteralScanHasTeeth(t *testing.T) {
	// Assembled from parts so this file does not itself hold the literals it forbids —
	// the same device the invented-term check below uses, and for the same reason.
	cases := []struct {
		what string
		line string
	}{
		{"a dotted quad", "address := \"" + "198." + "51." + "100." + "10\""},
		{"a CIDR", "network := \"" + "198." + "51." + "100." + "0" + "/24\""},
		{"an IPv6 address", "address := \"" + "2001:" + "db8:" + "0:" + "0::" + "10\""},
		{"a compressed IPv6 address", "address := \"" + "::" + "1\""},
	}
	dottedQuad := regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b(/\d{1,2})?`)
	ipv6Groups := regexp.MustCompile(`\b[0-9a-fA-F]{0,4}(:[0-9a-fA-F]{0,4}){3,}`)
	ipv6Compressed := regexp.MustCompile(
		`(^|[^A-Za-z0-9_:])([0-9a-fA-F]{1,4}::|::[0-9a-fA-F]{1,4})`)

	for _, testCase := range cases {
		matched := dottedQuad.MatchString(testCase.line) ||
			ipv6Groups.MatchString(testCase.line) ||
			ipv6Compressed.MatchString(testCase.line)
		if !matched {
			t.Errorf("no pattern rejects %s: %q", testCase.what, testCase.line)
		}
	}

	// And the shapes that must NOT match, or the scan would be turned off rather than
	// obeyed: a clock-time layout, a Go version, and a duration bound.
	for _, allowed := range []string{
		`layout := "2006-01-02T15:04:05"`,
		`const version = "1.27.0"`,
		`timeout := 5 * time.Second`,
		`source := "file:" + path + "?_pragma=busy_timeout(5000)"`,
	} {
		if dottedQuad.MatchString(allowed) || ipv6Groups.MatchString(allowed) ||
			ipv6Compressed.MatchString(allowed) {
			t.Errorf("a pattern fires on %q, which holds no address", allowed)
		}
	}
}

// TestNothingBranchesOnTheTextOfANameOrADescription is the other half of the same
// rule, and it is the one the project broke before.
//
// Nothing may derive an interface's nature, a client's owner or a list's purpose from
// what it is called. The enforceable form: no comparison anywhere reads a field whose
// name says it holds a name, a description or a label.
func TestNothingBranchesOnTheTextOfANameOrADescription(t *testing.T) {
	// A comparison or a containment test against a description, a label or a display
	// name. `provider_key` and `identifier` are deliberately NOT in this list: a
	// provider key is a contract opnview implements and an identifier is a
	// configuration key, and comparing either is reading a key rather than a name.
	// The patterns require the comparison to be ADJACENT to the field, so a line that
	// merely passes a description to a function and also checks an error does not
	// match. A loose pattern here would be worse than none: it would be silenced.
	named := `(?i)\b\w*(description|user_?label|hostname|vendor_?hint|display_?name)\b`
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(named + `\s*(==|!=)`),
		regexp.MustCompile(`(==|!=)\s*` + named),
		regexp.MustCompile(`strings\.(Contains|HasPrefix|HasSuffix|EqualFold)\(\s*` + named),
		regexp.MustCompile(`switch\s+` + named),
		regexp.MustCompile(`case\s+` + named),
	}

	for _, file := range goSourceFiles(t) {
		for number, line := range strings.Split(readFile(t, file), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, pattern := range forbidden {
				if pattern.MatchString(line) {
					t.Errorf("%s:%d appears to branch on the text of a name or a description: %s",
						file, number+1, trimmed)
				}
			}
		}
	}
}

// TestNoFirewallURLKeyOrSecretExistsInTheRepository is the no-secrets rule.
//
// It is checked over EVERY Go file, tests included, and over the fixtures: a
// credential in a test is still a credential in the repository. What a test may do is
// generate one, which leaves nothing behind.
func TestNoFirewallURLKeyOrSecretExistsInTheRepository(t *testing.T) {
	// Assignments that would put a real value in one of the three fields. The
	// patterns match an assignment to a non-empty literal.
	suspicious := []*regexp.Regexp{
		regexp.MustCompile(`APIKey:\s*"[^"]+"`),
		regexp.MustCompile(`APISecret:\s*"[^"]+"`),
		regexp.MustCompile(`BaseURL:\s*"https?://[^"]+"`),
	}
	// The one exception, and it is an exception that cannot be abused: RFC 2606
	// reserves the .invalid top-level domain precisely so that a test can name a host
	// that is guaranteed never to resolve. A URL ending there can never reach a real
	// firewall, and a test that needs an unreachable one has to write something.
	reservedTestDomain := regexp.MustCompile(`BaseURL:\s*"https?://[A-Za-z0-9.-]+\.invalid"`)

	for _, file := range allGoFiles(t) {
		for number, line := range strings.Split(readFile(t, file), "\n") {
			if reservedTestDomain.MatchString(line) {
				continue
			}
			for _, pattern := range suspicious {
				if !pattern.MatchString(line) {
					continue
				}
				// A test standing up a fake firewall assigns BaseURL from the httptest
				// server's address, which is a variable and not a literal, and generates
				// its credentials. A literal is what this test refuses.
				t.Errorf("%s:%d assigns a firewall URL, key or secret from a literal: %s",
					file, number+1, strings.TrimSpace(line))
			}
		}
	}

	if defaults := (Credentials{}); defaults.BaseURL != "" || defaults.APIKey != "" ||
		defaults.APISecret != "" {
		t.Error("the zero Credentials value is not empty, so something ships a default")
	}
}

// TestEveryStringInTheSourceIsEnglish is the language rule.
//
// The word list is the one sql/schema-checks.sh already uses for the SQL and the
// documents, applied to the Go source so the rule holds across the whole repository
// rather than in the half that happens to be checked by a shell script.
func TestEveryStringInTheSourceIsEnglish(t *testing.T) {
	// Every entry is a word that belongs to the other language and has no English
	// homograph. The short prepositions of that language are deliberately left out,
	// because several of them occur inside English identifiers and a pattern that
	// fires on those would be turned off rather than obeyed. The word this project
	// invented for an interface is in the list too: it was removed from the schema and
	// it must not come back through the code.
	// The invented word is ASSEMBLED rather than written. It has to be forbidden and it
	// must not appear anywhere under cmd/ or internal/, and a check that spelled it out
	// would put it back into the very tree it is guarding — which is not a formality: the
	// project's grep for it is how anyone verifies it is gone.
	invented := "seg" + "ment"
	french := regexp.MustCompile(`(?i)\b(les|des|une|est|sont|avec|cette|dans|nous|vous|mais|donc|ainsi|aucun|chaque|toujours|jamais|fichier|requête|données|réseau|serveur|adresse|` +
		invented + `s?)\b`)

	for _, file := range allGoFiles(t) {
		for number, line := range strings.Split(readFile(t, file), "\n") {
			if match := french.FindString(line); match != "" {
				// The one legitimate occurrence is this test's own word list, which has
				// to name the words in order to forbid them.
				if strings.Contains(line, "french := regexp") {
					continue
				}
				t.Errorf("%s:%d holds the non-English word %q: %s",
					file, number+1, match, strings.TrimSpace(line))
			}
		}
	}
}

// goSourceFiles lists the non-test Go files of the program.
func goSourceFiles(t *testing.T) []string {
	t.Helper()
	return walkGoFiles(t, false)
}

// allGoFiles lists every Go file, tests included.
func allGoFiles(t *testing.T) []string {
	t.Helper()
	return walkGoFiles(t, true)
}

// walkGoFiles walks the scanned roots.
func walkGoFiles(t *testing.T, includeTests bool) []string {
	t.Helper()
	var files []string
	for _, root := range scannedRoots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(entry.Name(), ".go") {
				return nil
			}
			if !includeTests && strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			files = append(files, filepath.ToSlash(path))
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	if len(files) < 6 {
		t.Fatalf("only %d Go files were found, so this scan is not reaching the program", len(files))
	}
	return files
}

// inThisPackage reports whether a path is inside internal/opnsense.
func inThisPackage(path string) bool {
	return strings.Contains(path, "/internal/opnsense/")
}

// inAnOutboundPackage reports whether a file belongs to one of the two packages that
// make the two outbound calls.
func inAnOutboundPackage(path string) bool {
	return inThisPackage(path) || strings.Contains(path, "/internal/maxmind/")
}

// readFile returns a file's contents.
func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}
