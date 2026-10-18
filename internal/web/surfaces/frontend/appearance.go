package frontend

import (
	"context"
	"net/http"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/front"
	"github.com/NaejEL/opnview/internal/web"
)

// The appearance (A5 and decision D9): the palette, the mode and the language the interface is
// drawn in, stored as installation settings.
//
// AN ABSENT VALUE IS THE FIRST-LAUNCH BEHAVIOUR, decided in the browser and never written back: the
// mode follows the operating system's colour-scheme preference, the palette is the default one, and
// the language is the first of the browser's languages that is installed, else English. The answer
// therefore says which values are set -- null for one that is not -- beside what is offered.
//
// A WRITE IS VALIDATED WHOLE BEFORE ANYTHING IS WRITTEN: a palette that no theme file carries, a
// mode that is not one of the three, a language that is not installed, and nothing is stored. A
// null member clears its setting; an absent one leaves it as it is.

// The refusals of a write.
const (
	ConditionPaletteUnknown = "palette_unknown"
	ConditionModeUnknown    = "mode_unknown"
	// ConditionLanguageUnknown (frontend.go) refuses a language that is not installed.
)

// PaletteEntry is one palette offered.
type PaletteEntry struct {
	ID   string            `json:"id"`
	Name map[string]string `json:"name"`
}

// AppearanceAnswer is GET /api/appearance's answer, and a write's.
type AppearanceAnswer struct {
	// Palette, Mode and Language are the stored values, null where none is set.
	Palette  *string `json:"palette"`
	Mode     *string `json:"mode"`
	Language *string `json:"language"`
	// DefaultPalette is the palette an installation that chose none is drawn with.
	DefaultPalette string          `json:"default_palette"`
	Palettes       []PaletteEntry  `json:"palettes"`
	Modes          []string        `json:"modes"`
	Languages      []LanguageEntry `json:"languages"`
}

// appearanceSettings are the three settings, each with its member and its refusal.
var appearanceSettings = []struct {
	member, key, refusal string
}{
	{"palette", config.KeyAppearancePalette, ConditionPaletteUnknown},
	{"mode", config.KeyAppearanceMode, ConditionModeUnknown},
	{"language", config.KeyAppearanceLanguage, ConditionLanguageUnknown},
}

// appearance reads the stored values and what is offered.
func (s *surface) appearance(ctx context.Context) (AppearanceAnswer, error) {
	answer := AppearanceAnswer{DefaultPalette: front.DefaultPalette, Palettes: []PaletteEntry{},
		Modes: config.AppearanceModes(), Languages: s.languages()}
	for _, theme := range s.index.Themes() {
		answer.Palettes = append(answer.Palettes, PaletteEntry{ID: theme.ID, Name: theme.Name})
	}
	targets := map[string]**string{"palette": &answer.Palette, "mode": &answer.Mode, "language": &answer.Language}
	for _, setting := range appearanceSettings {
		value, present, err := s.Store().Setting(ctx, setting.key)
		if err != nil {
			return AppearanceAnswer{}, err
		}
		if present {
			stored := value
			*targets[setting.member] = &stored
		}
	}
	return answer, nil
}

func (s *surface) handleAppearance(writer http.ResponseWriter, request *http.Request) {
	answer, err := s.appearance(request.Context())
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	web.WriteJSON(writer, http.StatusOK, answer)
}

// offered reports whether a value of one setting is offered by this installation.
func (s *surface) offered(member, value string) bool {
	switch member {
	case "palette":
		return s.index.HasTheme(value)
	case "language":
		return s.index.HasLanguage(value)
	default:
		for _, mode := range config.AppearanceModes() {
			if mode == value {
				return true
			}
		}
		return false
	}
}

func (s *surface) handleAppearanceWrite(writer http.ResponseWriter, request *http.Request) {
	body, ok := web.DecodeJSONObject(request.Body)
	if !ok {
		web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionMalformedBody, "")
		return
	}
	type change struct {
		key   string
		value *string
	}
	var changes []change
	for _, setting := range appearanceSettings {
		raw, present := body[setting.member]
		if !present {
			continue
		}
		if raw == nil {
			changes = append(changes, change{key: setting.key})
			continue
		}
		value, isText := raw.(string)
		if !isText || !s.offered(setting.member, value) {
			web.RefuseJSON(writer, http.StatusBadRequest, setting.refusal, setting.member)
			return
		}
		changes = append(changes, change{key: setting.key, value: &value})
	}
	for member := range body {
		known := false
		for _, setting := range appearanceSettings {
			known = known || setting.member == member
		}
		if !known {
			web.RefuseJSON(writer, http.StatusBadRequest, web.ConditionMalformedBody, member)
			return
		}
	}
	ctx := request.Context()
	for _, applied := range changes {
		var err error
		if applied.value == nil {
			err = s.Store().DeleteSetting(ctx, applied.key)
		} else {
			err = s.Store().SetSetting(ctx, applied.key, *applied.value, s.Now().UTC().Unix())
		}
		if err != nil {
			web.FailJSON(writer, request, err)
			return
		}
	}
	s.handleAppearance(writer, request)
}
