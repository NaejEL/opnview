package web

import (
	"context"
	"fmt"

	"github.com/NaejEL/opnview/internal/config"
)

// The theme.
//
// THE RULE IS ROADMAP.md STEP 3'S AND docs/ui-references.md'S, NOT A NEW ONE:
// on first launch the theme follows the OPERATING SYSTEM's colour-scheme
// preference, the user can override it afterwards, and the override PERSISTS. The
// industrial palette is the default. Here that is three values and one mechanism:
//
//   - `system`, the value of an installation nobody has overridden, renders NO
//     data-theme attribute at all, so the stylesheet's
//     `prefers-color-scheme` block is what decides. The operating system is
//     followed because nothing in the document contradicts it, which is exactly
//     how the industrial palette's own stylesheet behaves — docs/ui-references.md,
//     Family C, records that the file uses the media query and has no manual
//     override mechanism at all.
//   - `light` and `dark` render the attribute, and a `[data-theme]` rule in the
//     stylesheet outranks the media query in both directions.
//
// The override persists as a `setting` row, which is where internal/config already
// looks and is one row FOR THE INSTALLATION rather than per account: the schema has
// no per-account preference and one account cannot justify inventing one.
//
// THE FIVE NAMED PALETTES ARE NOT HERE. Tokyo Night, Dracula, Nord, Rosé Pine and
// Catppuccin are offered as named options at the values published on the
// maintainer's own site, and a palette is a FILE of the same kind a third party
// adds — which is the theme registry of step 7, listed as out of scope for this
// cycle. What ships here is the default palette in its two variants plus the
// operating system's preference, which is the part of the rule this cycle's three
// surfaces need.

// Theme is the value of the `theme` setting row.
type Theme string

// The three values, and no fourth until step 7 adds the palette registry.
const (
	// ThemeSystem follows the operating system. It is the value of an
	// installation that has not been overridden, and the default when the row is
	// absent.
	ThemeSystem Theme = "system"
	// ThemeLight is the industrial palette's light variant.
	ThemeLight Theme = "light"
	// ThemeDark is the industrial palette's low-light variant.
	ThemeDark Theme = "dark"
)

// themes is the three values in the order the selector offers them, each with the
// catalogue key that names it.
func themes() []struct {
	Value Theme
	Label messageKey
} {
	return []struct {
		Value Theme
		Label messageKey
	}{
		{ThemeSystem, msgThemeSystem},
		{ThemeLight, msgThemeLight},
		{ThemeDark, msgThemeDark},
	}
}

// ParseTheme accepts one of the three values, and refuses anything else rather
// than falling back. A theme nobody offered, silently replaced by the default, is
// a control that does not do what it says.
func ParseTheme(value string) (Theme, error) {
	for _, candidate := range themes() {
		if string(candidate.Value) == value {
			return candidate.Value, nil
		}
	}
	return ThemeSystem, fmt.Errorf("web: %q is not a theme this build offers", value)
}

// DocumentAttribute is what the document's data-theme attribute carries, or the
// empty string when there is none.
//
// The empty string is the load-bearing case: no attribute means the stylesheet's
// media query decides, which is how the operating system's preference is followed
// on first launch.
func (t Theme) DocumentAttribute() string {
	switch t {
	case ThemeLight:
		return "industrial-light"
	case ThemeDark:
		return "industrial-dark"
	default:
		return ""
	}
}

// readTheme reads the stored theme, defaulting to ThemeSystem when there is no
// row — which is the first-launch state.
//
// A row holding something this build does not offer is NOT an error here: the
// interface still has to render, and the setting form is where the reader fixes
// it. It is reported by falling back to following the operating system, which is
// the documented default rather than an invented one.
func (s *Server) readTheme(ctx context.Context) Theme {
	value, present, err := s.store.Setting(ctx, config.KeyTheme)
	if err != nil || !present {
		return ThemeSystem
	}
	theme, err := ParseTheme(value)
	if err != nil {
		return ThemeSystem
	}
	return theme
}
