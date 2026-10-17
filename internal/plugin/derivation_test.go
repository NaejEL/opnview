package plugin

import (
	"testing"

	"github.com/NaejEL/opnview/internal/plugin/contract"
)

// derivationManifest is pushManifest with a table a derivation may write and one valid derivation.
func derivationManifest() map[string]any {
	manifest := pushManifest()
	manifest["script"] = "example.js"
	manifest["tables"] = append(manifest["tables"].([]any), map[string]any{"name": "words", "columns": []any{
		map[string]any{"name": "word", "type": "text"},
		map[string]any{"name": "seen", "type": "integer"},
	}, "primary_key": []any{"word"}}, map[string]any{"name": "loose", "columns": []any{
		map[string]any{"name": "word", "type": "text"},
	}})
	manifest["derivations"] = []any{map[string]any{"name": "count_words", "after_push": []any{"lines"},
		"after_passes": []any{"firewall_log/pf"}, "outputs": map[string]any{"tables": []any{"words"}}}}
	return manifest
}

const derivationScript = `exports.derivations = { count_words: function (host, run) { return {}; } };`

// TestEveryDerivationRefusalHasItsCode: a derivation the loader cannot run is refused with
// derivation_invalid, whatever is wrong with it; the valid one loads.
func TestEveryDerivationRefusalHasItsCode(t *testing.T) {
	t.Parallel()
	if refusals := loadManifest(t, derivationManifest(), derivationScript).Refusals(); len(refusals) != 0 {
		t.Fatalf("the valid derivation was refused: %+v", refusals)
	}
	first := func(m map[string]any) map[string]any { return m["derivations"].([]any)[0].(map[string]any) }
	cases := map[string]struct {
		mutate func(map[string]any)
		script string
	}{
		"a function the script does not export": {func(m map[string]any) {}, `exports.derivations = {};`},
		"no trigger": {func(m map[string]any) {
			delete(first(m), "after_push")
			delete(first(m), "after_passes")
		}, derivationScript},
		"a pass of no core kind": {func(m map[string]any) { first(m)["after_passes"] = []any{"no_such_kind"} },
			derivationScript},
		"a push entry the plugin lacks": {func(m map[string]any) { first(m)["after_push"] = []any{"other"} },
			derivationScript},
		"an undeclared dataset": {func(m map[string]any) { first(m)["datasets"] = []any{"traffic"} }, derivationScript},
		"a table of no plugin's": {func(m map[string]any) {
			first(m)["outputs"] = map[string]any{"tables": []any{"elsewhere"}}
		}, derivationScript},
		"a table with no primary key": {func(m map[string]any) {
			first(m)["outputs"] = map[string]any{"tables": []any{"loose"}}
		}, derivationScript},
		"an undeclared measure": {func(m map[string]any) {
			first(m)["outputs"] = map[string]any{"measures": []any{"no_such_measure"}}
		}, derivationScript},
		"no output": {func(m map[string]any) { first(m)["outputs"] = map[string]any{} }, derivationScript},
		"two of one name": {func(m map[string]any) {
			m["derivations"] = append(m["derivations"].([]any), first(m))
		}, derivationScript},
	}
	for name, c := range cases {
		manifest := derivationManifest()
		c.mutate(manifest)
		refusals := loadManifest(t, manifest, c.script).Refusals()
		if len(refusals) != 1 || refusals[0].Code != contract.RefusalDerivationInvalid {
			t.Errorf("%s: want %s, got %+v", name, contract.RefusalDerivationInvalid, refusals)
		}
	}
}
