package config

// The appearance: the palette, the mode and the language the interface is drawn in
// (specs/SPEC-step-7-frontend.md, A5 and decision D9).
//
// THEY ARE ROWS FOR THE INSTALLATION and not per-account preferences: the schema has no such
// concept and one account cannot justify inventing one. An absent row is the first-launch
// behaviour, decided in the browser and never written back: the mode follows the operating
// system's colour-scheme preference, the palette is the industrial one, and the language is the
// first of the browser's languages that is installed, else English.
//
// The values are validated by the front's own registry (internal/front): a palette is a theme
// file's id, a language a language file's code, and a mode one of AppearanceModes. Nothing here
// names a palette or a language.
const (
	// KeyAppearancePalette is the id of the theme file the interface is drawn with.
	KeyAppearancePalette = "appearance_palette"
	// KeyAppearanceMode is the mode: system, light or dark.
	KeyAppearanceMode = "appearance_mode"
	// KeyAppearanceLanguage is the code of the language the interface is drawn in.
	KeyAppearanceLanguage = "appearance_language"
)

// AppearanceModes are the values KeyAppearanceMode may hold, in the order a selector offers them:
// following the operating system, and the two modes every theme carries.
func AppearanceModes() []string { return []string{"system", "light", "dark"} }
