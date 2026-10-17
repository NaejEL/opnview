package collection

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/provider"
)

// TestEverySettingsCardIsPlacedByData holds placement.json to the cards it places: the file decodes
// strictly, it places every settings card that is not at the head of the page and no card that does not
// exist, and every card it places one after is a core kind's card or another settings card -- so a broken
// file cannot silently draw the cards last.
func TestEverySettingsCardIsPlacedByData(t *testing.T) {
	t.Parallel()
	var placement struct {
		Comment string              `json:"comment"`
		After   map[string][]string `json:"after"`
	}
	decoder := json.NewDecoder(bytes.NewReader(placementFile))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&placement); err != nil {
		t.Fatalf("placement.json does not decode: %v", err)
	}
	cards := map[string]bool{}
	for _, spec := range settingCardSpecs() {
		cards[spec.ID] = true
		if _, placed := placement.After[spec.ID]; spec.Leading == placed {
			t.Errorf("the card %s leads the page: %v, and placement.json places it: %v; exactly one holds", spec.ID,
				spec.Leading, placed)
		}
		if !spec.Leading && len(spec.After) == 0 {
			t.Errorf("the card %s follows no card", spec.ID)
		}
	}
	for card, after := range placement.After {
		if !cards[card] {
			t.Errorf("placement.json places %s, which is no settings card", card)
		}
		for _, anchor := range after {
			if !provider.IsCoreKind(anchor) && !cards[anchor] {
				t.Errorf("placement.json places %s after %s, which is neither a core kind nor a settings card", card, anchor)
			}
		}
	}
}
