package suffixlist

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
