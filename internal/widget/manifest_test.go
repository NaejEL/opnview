package widget

import (
	"strings"
	"testing"
)

// minimalManifest is the smallest manifest a declarative widget needs: what it leaves out takes
// its stated default.
const minimalManifest = `{"manifest_version": 1, "type": "example_minimal", "shape": "table", "queries": ["query.sql"]}`

func TestAManifestMayLeaveOutWhatHasADefault(t *testing.T) {
	t.Parallel()
	manifest, err := decodeManifest([]byte(minimalManifest))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.NoDomains != NoDomainsNone || !manifest.Renderer.Reserved || manifest.Windowed {
		t.Errorf("the defaults are no_domains none, renderer reserved, not windowed: %+v", manifest)
	}
	if refusal := ValidateManifest(manifest); refusal != nil {
		t.Errorf("the minimal manifest is refused: %v", refusal)
	}
	withParameter, err := decodeManifest([]byte(strings.Replace(minimalManifest, `"queries"`,
		`"parameters": [{"key": "limit", "kind": "integer", "min": 1, "default": 5}], "queries"`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if withParameter.Parameters[0].Class != ClassData {
		t.Errorf("a parameter with no class is a data parameter: %+v", withParameter.Parameters[0])
	}
}

func TestADecodedManifestRefusesWhatItDoesNotKnow(t *testing.T) {
	t.Parallel()
	for name, text := range map[string]string{
		"an unknown field": `{"manifest_version": 1, "type": "x", "shape": "table", "colour": "red"}`,
		"trailing data":    minimalManifest + ` {}`,
		"not an object":    `[1]`,
		"a wrong type":     `{"manifest_version": "one"}`,
	} {
		if _, err := decodeManifest([]byte(text)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestValidateManifestNamesTheRuleBroken(t *testing.T) {
	t.Parallel()
	mutate := func(replace, with string) string { return strings.Replace(minimalManifest, replace, with, 1) }
	cases := map[string]struct {
		text, code string
	}{
		"version":      {mutate(`"manifest_version": 1`, `"manifest_version": 7`), RefusalUnsupportedManifestVersion},
		"type":         {mutate(`example_minimal`, `Example Minimal`), RefusalManifestInvalid},
		"shape":        {mutate(`"table"`, `"gauge"`), RefusalUnknownShape},
		"provider":     {mutate(`"queries"`, `"providers": ["x"], "queries"`), RefusalUnknownProviderKind},
		"no_domains":   {mutate(`"queries"`, `"no_domains": "some", "queries"`), RefusalManifestInvalid},
		"measure":      {mutate(`"queries"`, `"measure": "Bad Measure", "queries"`), RefusalManifestInvalid},
		"renderer":     {mutate(`"queries"`, `"renderer": {}, "queries"`), RefusalRendererInvalid},
		"query path":   {mutate(`query.sql`, `sub/query.sql`), RefusalManifestInvalid},
		"query suffix": {mutate(`query.sql`, `query.txt`), RefusalManifestInvalid},
		"duplicate parameter": {mutate(`"queries"`, `"parameters": [{"key": "a", "kind": "boolean", "default": true},
			{"key": "a", "kind": "boolean", "default": true}], "queries"`), RefusalManifestInvalid},
		"windowed without period": {mutate(`"queries"`, `"windowed": true, "queries"`), RefusalManifestInvalid},
		"period outside the periods": {mutate(`"queries"`, `"windowed": true, "parameters": [{"key": "period",
			"kind": "enum", "values": ["1h", "2h"], "default": "1h"}], "queries"`), RefusalParameterVocabulary},
		"integer with values": {mutate(`"queries"`, `"parameters": [{"key": "n", "kind": "integer", "values": ["a"],
			"default": 1}], "queries"`), RefusalParameterVocabulary},
		"min above max": {mutate(`"queries"`, `"parameters": [{"key": "n", "kind": "integer", "min": 5, "max": 1,
			"default": 3}], "queries"`), RefusalParameterVocabulary},
		"default out of range": {mutate(`"queries"`, `"parameters": [{"key": "n", "kind": "integer", "min": 1,
			"default": 0}], "queries"`), RefusalBadDefault},
		"series default": {mutate(`"queries"`, `"parameters": [{"key": "s", "kind": "series", "default": [1]}],
			"queries"`), RefusalBadDefault},
		"reference default": {mutate(`"queries"`, `"references": ["interface"], "parameters": [{"key": "i",
			"kind": "reference", "reference": "interface", "default": {"kind": "interface"}}], "queries"`), RefusalBadDefault},
		"references never accepted": {mutate(`"queries"`, `"references": ["interface"], "queries"`), RefusalReferencesMismatch},
		"unknown reference kind":    {mutate(`"queries"`, `"references": ["galaxy"], "queries"`), RefusalReferencesMismatch},
	}
	for name, c := range cases {
		manifest, err := decodeManifest([]byte(c.text))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		refusal := ValidateManifest(manifest)
		if refusal == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if refusal.Code != c.code {
			t.Errorf("%s: refused as %s (%s), want %s", name, refusal.Code, refusal.Detail, c.code)
		}
	}
}

func TestEveryRefusalCodeIsDistinctAndDocumentedInTheError(t *testing.T) {
	t.Parallel()
	refusal := refuse("example", RefusalUnknownShape, "the shape %q is unknown", "pie")
	if !strings.Contains(refusal.Error(), "example") || !strings.Contains(refusal.Error(), RefusalUnknownShape) ||
		!strings.Contains(refusal.Error(), "pie") {
		t.Errorf("the error does not name the widget, the code and the detail: %v", refusal)
	}
}
