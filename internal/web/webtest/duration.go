package webtest

import (
	"github.com/NaejEL/opnview/internal/web"
	"strings"
	"testing"
)

// durationText is the markup a duration is drawn as, built from the catalogue: what a
// test compares a rendered page against.
func DurationText(t *testing.T, seconds int64) string {
	t.Helper()
	var parts []string
	for _, part := range web.SpelledDuration(seconds) {
		parts = append(parts, `<span class="duration-part"><span class="figure">`+part.Figure+
			"</span> "+CatalogueText(t, part.UnitKey)+"</span>")
	}
	return strings.Join(parts, " ")
}
