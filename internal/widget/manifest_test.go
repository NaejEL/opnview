package widget

import (
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/widget/params"
)

// minimalManifest is the smallest manifest a declarative widget needs: what it leaves out takes
// its stated default.
const minimalManifest = `{"manifest_version": 1, "type": "example_minimal", "shape": "table", "queries": ["query.sql"]}`

func TestAManifestMayLeaveOutWhatHasADefault(t *testing.T) {
	t.Parallel()
	manifest, err := params.DecodeManifest([]byte(minimalManifest))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.NoDomains != params.NoDomainsNone || !manifest.Renderer.Reserved || manifest.Windowed {
		t.Errorf("the defaults are no_domains none, renderer reserved, not windowed: %+v", manifest)
	}
	if refusal := params.ValidateManifest(manifest); refusal != nil {
		t.Errorf("the minimal manifest is refused: %v", refusal)
	}
	withParameter, err := params.DecodeManifest([]byte(strings.Replace(minimalManifest, `"queries"`,
		`"parameters": [{"key": "limit", "kind": "integer", "min": 1, "default": 5}], "queries"`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if withParameter.Parameters[0].Class != params.ClassData {
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
		if _, err := params.DecodeManifest([]byte(text)); err == nil {
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
		"version":      {mutate(`"manifest_version": 1`, `"manifest_version": 7`), params.RefusalUnsupportedManifestVersion},
		"type":         {mutate(`example_minimal`, `Example Minimal`), params.RefusalManifestInvalid},
		"shape":        {mutate(`"table"`, `"gauge"`), params.RefusalUnknownShape},
		"provider":     {mutate(`"queries"`, `"providers": ["x"], "queries"`), params.RefusalUnknownProviderKind},
		"no_domains":   {mutate(`"queries"`, `"no_domains": "some", "queries"`), params.RefusalManifestInvalid},
		"measure":      {mutate(`"queries"`, `"measure": "Bad Measure", "queries"`), params.RefusalManifestInvalid},
		"renderer":     {mutate(`"queries"`, `"renderer": {}, "queries"`), params.RefusalRendererInvalid},
		"query path":   {mutate(`query.sql`, `sub/query.sql`), params.RefusalManifestInvalid},
		"query suffix": {mutate(`query.sql`, `query.txt`), params.RefusalManifestInvalid},
		"duplicate parameter": {mutate(`"queries"`, `"parameters": [{"key": "a", "kind": "boolean", "default": true},
			{"key": "a", "kind": "boolean", "default": true}], "queries"`), params.RefusalManifestInvalid},
		"windowed without period": {mutate(`"queries"`, `"windowed": true, "queries"`), params.RefusalManifestInvalid},
		"period outside the periods": {mutate(`"queries"`, `"windowed": true, "parameters": [{"key": "period",
			"kind": "enum", "values": ["1h", "2h"], "default": "1h"}], "queries"`), params.RefusalParameterVocabulary},
		"integer with values": {mutate(`"queries"`, `"parameters": [{"key": "n", "kind": "integer", "values": ["a"],
			"default": 1}], "queries"`), params.RefusalParameterVocabulary},
		"min above max": {mutate(`"queries"`, `"parameters": [{"key": "n", "kind": "integer", "min": 5, "max": 1,
			"default": 3}], "queries"`), params.RefusalParameterVocabulary},
		"default out of range": {mutate(`"queries"`, `"parameters": [{"key": "n", "kind": "integer", "min": 1,
			"default": 0}], "queries"`), params.RefusalBadDefault},
		"series default": {mutate(`"queries"`, `"parameters": [{"key": "s", "kind": "series", "default": [1]}],
			"queries"`), params.RefusalBadDefault},
		"reference default": {mutate(`"queries"`, `"references": ["interface"], "parameters": [{"key": "i",
			"kind": "reference", "reference": "interface", "default": {"kind": "interface"}}], "queries"`), params.RefusalBadDefault},
		"references never accepted": {mutate(`"queries"`, `"references": ["interface"], "queries"`), params.RefusalReferencesMismatch},
		"unknown reference kind":    {mutate(`"queries"`, `"references": ["galaxy"], "queries"`), params.RefusalReferencesMismatch},
	}
	for name, c := range cases {
		manifest, err := params.DecodeManifest([]byte(c.text))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		refusal := params.ValidateManifest(manifest)
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
	refusal := params.Refuse("example", params.RefusalUnknownShape, "the shape %q is unknown", "pie")
	if !strings.Contains(refusal.Error(), "example") || !strings.Contains(refusal.Error(), params.RefusalUnknownShape) ||
		!strings.Contains(refusal.Error(), "pie") {
		t.Errorf("the error does not name the widget, the code and the detail: %v", refusal)
	}
}
