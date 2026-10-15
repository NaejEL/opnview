package suffixlist

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store/reads"
)

// The list and the algorithm, tested against the list's own test vectors.

// excerpt is the list's own rules the test vectors exercise.
func excerpt(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "public_suffix_list_excerpt.dat"))
	if err != nil {
		t.Fatalf("reading the excerpt: %v", err)
	}
	return raw
}

// vectorLine is one line of the list's own test file.
var vectorLine = regexp.MustCompile(`^checkPublicSuffix\((null|'[^']*'), (null|'[^']*')\);$`)

// TestTheRegistrableDomainPassesTheListsOwnTestVectors is AC7.
func TestTheRegistrableDomainPassesTheListsOwnTestVectors(t *testing.T) {
	list, err := parse(excerpt(t), 1)
	if err != nil {
		t.Fatalf("parsing the excerpt: %v", err)
	}
	file, err := os.Open(filepath.Join("testdata", "test_psl.txt"))
	if err != nil {
		t.Fatalf("opening the vectors: %v", err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	vectors := 0
	categories := map[string]bool{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		match := vectorLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		vectors++
		input, expected := match[1], match[2]
		if input == "null" {
			continue
		}
		input = strings.Trim(input, "'")
		got, ok := list.RegistrableDomain(input)
		switch {
		case expected == "null" && ok:
			t.Errorf("%s has no registrable domain, and the function gave %s", input, got)
		case expected != "null" && !ok:
			t.Errorf("%s: the function gave none, the vector expects %s", input, expected)
		case expected != "null" && got != strings.Trim(expected, "'"):
			t.Errorf("%s: the function gave %s, the vector expects %s", input, got, expected)
		}
		switch {
		case strings.HasSuffix(input, ".ck") || strings.HasSuffix(input, ".mm"):
			categories["wildcard"] = true
		}
		if strings.Contains(input, "kobe.jp") || input == "www.ck" || input == "www.www.ck" {
			categories["exception"] = true
		}
		if strings.Contains(input, "k12.ak.us") || strings.Contains(input, "ide.kyoto.jp") {
			categories["multi-label"] = true
		}
	}
	if vectors < 75 {
		t.Fatalf("only %d vectors were read", vectors)
	}
	for _, category := range []string{"wildcard", "exception", "multi-label"} {
		if !categories[category] {
			t.Errorf("no %s rule was exercised", category)
		}
	}

	// The grouping never alters a site name: each group carries the stored names verbatim,
	// whatever the case, the trailing dot or the script they were written in, and the totals
	// it was handed are left as they were. The names are chosen so that any normalisation --
	// lower-casing, stripping the dot, punycode -- would change at least one of them.
	stored := []reads.SiteTotal{
		{SiteName: "WWW.Example.co.uk", Bytes: 10, Connections: 1},
		{SiteName: "shop.example.co.uk.", Bytes: 20, Connections: 2},
		{SiteName: "bücher.example.de", Bytes: 30, Connections: 3},
		{SiteName: "co.uk", Bytes: 40, Connections: 4},
	}
	handed := append([]reads.SiteTotal(nil), stored...)
	var carried []string
	for _, group := range GroupSites(list.RegistrableDomain, handed) {
		carried = append(carried, group.SiteNames...)
	}
	sort.Strings(carried)
	var want []string
	for _, site := range stored {
		want = append(want, site.SiteName)
	}
	sort.Strings(want)
	if strings.Join(carried, "|") != strings.Join(want, "|") {
		t.Errorf("the groups carry the site names %q, not the stored %q verbatim", carried, want)
	}
	for index := range stored {
		if handed[index] != stored[index] {
			t.Errorf("grouping altered the site total it was handed: %+v became %+v", stored[index], handed[index])
		}
	}
	if got, ok := list.RegistrableDomain("www.example.com."); !ok || got != "example.com." {
		t.Errorf("a trailing dot gave %q", got)
	}
}

// TestPunycodeEncodesTheRFCExample is the encoder against RFC 3492's own sample.
func TestPunycodeEncodesTheRFCExample(t *testing.T) {
	for input, want := range map[string]string{
		"bücher": "bcher-kva",
		"中国":     "fiqs8s",
		"公司":     "55qx5d",
		"食狮":     "85x722f",
	} {
		got, ok := punycodeEncode([]rune(input))
		if !ok || got != want {
			t.Errorf("%s encodes to %s, not %s", input, got, want)
		}
	}
}
