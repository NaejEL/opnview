package collectiontest

import (
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/web/settingfields"
)

// DurationText is the markup a duration is drawn as, built from the catalogue: what a
// test compares a rendered page against.
func DurationText(t *testing.T, seconds int64) string {
	t.Helper()
	var parts []string
	for _, part := range settingfields.SpelledDuration(seconds) {
		parts = append(parts, `<span class="duration-part"><span class="figure">`+part.Figure+
			"</span> "+CatalogueText(t, part.UnitKey)+"</span>")
	}
	return strings.Join(parts, " ")
}
