package collect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEveryFixtureSaysInItsOwnFileThatItIsSynthesisedAndNotACapture is the honesty
// guard on this cycle's largest risk.
//
// Every collector test runs against a fake built from a document. If the survey is
// wrong about a field, an envelope or a status code, these tests pass and the
// collection is wrong — and the failure mode that makes that dangerous is not the
// error itself but somebody in six months treating testdata as evidence of what an
// OPNsense returns. So each fixture carries its provenance in its own file, this test
// refuses one that does not, and the statement it must carry says outright that it is
// not a capture.
func TestEveryFixtureSaysInItsOwnFileThatItIsSynthesisedAndNotACapture(t *testing.T) {
	t.Parallel()
	names := fixtureNames(t)
	if len(names) < 20 {
		t.Fatalf("only %d fixtures were found, so this check is not reaching them", len(names))
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			fixture := readFixture(t, name)
			provenance := fixture.Provenance

			if provenance.SynthesisedFrom != "docs/opnsense-api-survey.md" {
				t.Errorf("the fixture says it was synthesised from %q, not from the survey",
					provenance.SynthesisedFrom)
			}
			if provenance.CapturedFromAFirewall {
				t.Error("the fixture claims to be a capture from a firewall; this cycle cannot " +
					"reach one, so such a claim is false")
			}
			for _, phrase := range []string{
				"SYNTHESISED FROM A DOCUMENT",
				"NOT A CAPTURE",
				"Do not treat this file as evidence",
			} {
				if !strings.Contains(provenance.Statement, phrase) {
					t.Errorf("the statement does not say %q, so a reader could mistake the file "+
						"for a recording", phrase)
				}
			}
			if provenance.SurveySection == "" {
				t.Error("the fixture names no survey section, so its field list is unjustified")
			}
			if provenance.FieldListFrom == "" {
				t.Error("the fixture does not say which field list it was built from")
			}
			if provenance.WhyTheseValues == "" {
				t.Error("the fixture does not say why it holds the values it holds, so a reader " +
					"cannot tell which cases it exercises on purpose")
			}
			if !strings.Contains(provenance.ExampleValues, "synthesised") {
				t.Error("the fixture does not say that its identifiers and addresses are " +
					"synthesised example values")
			}
			if len(fixture.Body) == 0 {
				t.Error("the fixture carries no response body")
			}
		})
	}
}

// TestEveryFixtureNamesASurveySectionThatExists keeps a citation from pointing at a
// heading somebody renamed. The endpoint registry's own citations are checked the same
// way in internal/opnsense.
func TestEveryFixtureNamesASurveySectionThatExists(t *testing.T) {
	t.Parallel()
	survey := readSurveyDocument(t)
	for _, name := range fixtureNames(t) {
		section := readFixture(t, name).Provenance.SurveySection
		// The section is written for a human — "Data source 4 - Degradation; Runtime
		// discovery (iv)" — so the check is that its leading phrase exists, which is
		// what a rename would break. The trailing roman numeral is dropped because the
		// survey marks those subsections with markdown emphasis rather than as part of
		// the heading, so the numeral is not a literal string in the document.
		leading := strings.TrimSpace(strings.Split(strings.Split(section, ";")[0], " - ")[0])
		if open := strings.Index(leading, "("); open > 0 {
			leading = strings.TrimSpace(leading[:open])
		}
		if leading == "" {
			t.Errorf("%s names an empty survey section", name)
			continue
		}
		if !strings.Contains(survey, leading) {
			t.Errorf("%s cites the survey section %q, and the document does not contain %q",
				name, section, leading)
		}
	}
}

// readSurveyDocument returns the survey. The tests run with the package directory as
// the working directory.
func readSurveyDocument(t *testing.T) string {
	t.Helper()
	return readTextFile(t, "../../docs/opnsense-api-survey.md")
}

// TestMain sets a non-UTC zone for every test in this package.
//
// It is here as well as in internal/decode, and deliberately not only there. Normalisation is
// where a host zone would be read by accident, and that package tests it; but the collection
// path composes instants of its own — the epoch every column stores, the reference time handed
// to the normaliser, the day boundary an identity is keyed on — and each of those is a second
// chance to read the wrong clock. The container is somebody's laptop and the firewall is
// somewhere else, so a suite that ran only in UTC would pass whether or not the rule held.
//
// The zone is chosen to be wrong in BOTH directions at different times of year: a large offset
// and a southern-hemisphere summer-time rule, so an accidental read lands hours out rather than
// by a suspiciously round amount.
func TestMain(m *testing.M) {
	if err := os.Setenv("TZ", "Australia/Sydney"); err != nil {
		panic("collect_test: setting TZ: " + err.Error())
	}
	// Go reads TZ when it first needs the local zone, so the location is loaded here to make
	// the setting take effect for every test below.
	location, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		panic("collect_test: the zone database does not carry Australia/Sydney: " + err.Error())
	}
	time.Local = location
	os.Exit(m.Run())
}

// readTextFile returns a file's contents.
func readTextFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}
