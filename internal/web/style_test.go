package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// innermostBlock is one rule's declarations: braces holding no brace.
var innermostBlock = regexp.MustCompile(`\{[^{}]*\}`)

// colourLiteral is a colour written as a value rather than through a token.
var colourLiteral = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|\b(?:rgba?|hsla?)\(`)

// TestOnlyThePalettesCarryAColourAndEveryCornerIsTheToken is AC25: a colour is written
// once, in the industrial palette's blocks, and every rule outside them reaches it
// through a custom property; and every corner radius is the palette's token.
func TestOnlyThePalettesCarryAColourAndEveryCornerIsTheToken(t *testing.T) {
	t.Parallel()
	raw, err := files.ReadFile("asset/style.css")
	if err != nil {
		t.Fatalf("reading the stylesheet: %v", err)
	}
	rules := stripCSSComments(string(raw))
	palettes, checked := 0, 0
	for _, block := range innermostBlock.FindAllString(rules, -1) {
		// A palette block is the one that defines the colour tokens.
		if strings.Contains(block, "--accent:") {
			palettes++
			continue
		}
		checked++
		if literal := colourLiteral.FindString(block); literal != "" {
			t.Errorf("a rule outside the palettes writes the colour %q: %s", literal, block)
		}
		for _, declaration := range strings.Split(strings.Trim(block, "{}"), ";") {
			// The token's own definition is not a use of it.
			if strings.HasPrefix(strings.TrimSpace(declaration), "--") {
				continue
			}
			if strings.Contains(declaration, "border-radius:") &&
				!strings.Contains(declaration, "var(--border-radius)") {
				t.Errorf("a corner is not the palette's radius token: %s", strings.TrimSpace(declaration))
			}
		}
	}
	if palettes != 4 || checked == 0 {
		t.Fatalf("found %d palette blocks and %d other rules, want the four palettes and the rest",
			palettes, checked)
	}
}

// ipv6Literal is an IPv6 address or network written out: a run of hexadecimal groups
// around a "::", or eight full groups. A match holding no digit is a CSS
// pseudo-element after a hexadecimal-looking word -- ".bad::before" -- and not one.
var ipv6Literal = regexp.MustCompile(
	`(?i)\b[0-9a-f]{1,4}(?::[0-9a-f]{1,4})*::(?:[0-9a-f]{1,4}(?::[0-9a-f]{1,4})*)?(?:/\d{1,3})?|` +
		`\b(?:[0-9a-f]{1,4}:){7}[0-9a-f]{1,4}\b`)

// TestNoAddressIsWrittenIntoATemplateTheCatalogueOrTheStylesheet is AC26 over the
// three kinds of file a reader sees the contents of: no address, no network of
// either family and no interface name is written into a template, a language file or
// the stylesheet. Unlike the repository-wide scan, nothing here is exempted by the
// shape of its line: a stylesheet line carrying a colour is checked all the same.
func TestNoAddressIsWrittenIntoATemplateTheCatalogueOrTheStylesheet(t *testing.T) {
	t.Parallel()
	inspected := 0
	for _, directory := range []string{"template", "locale", "asset"} {
		entries, err := fs.ReadDir(files, directory)
		if directory == "locale" {
			entries, err = fs.ReadDir(locales, directory)
		}
		if err != nil {
			t.Fatalf("listing %s: %v", directory, err)
		}
		for _, entry := range entries {
			path := directory + "/" + entry.Name()
			raw, err := fs.ReadFile(files, path)
			if directory == "locale" {
				raw, err = fs.ReadFile(locales, path)
			}
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			inspected++
			for number, line := range strings.Split(string(raw), "\n") {
				for name, pattern := range map[string]*regexp.Regexp{
					"an IPv4 address": patternIPv4, "an IPv4 network": patternCIDR,
					"an interface name": patternInterfaceName,
				} {
					if match := pattern.FindString(line); match != "" {
						t.Errorf("%s:%d carries %s (%q)", path, number+1, name, match)
					}
				}
				for _, match := range ipv6Literal.FindAllString(line, -1) {
					if strings.ContainsAny(match, "0123456789") {
						t.Errorf("%s:%d carries an IPv6 address or network (%q)", path, number+1, match)
					}
				}
			}
		}
	}
	if inspected < 3 {
		t.Fatalf("only %d files were inspected, so this assertion is close to vacuous", inspected)
	}
	// The pattern itself finds what it is for.
	for _, sample := range []string{"2001:db8::/32", "fe80::1", "2001:db8:0:0:0:0:0:1"} {
		if !ipv6Literal.MatchString(sample) {
			t.Errorf("the IPv6 pattern misses %q", sample)
		}
	}
}
