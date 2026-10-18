package front

import "fmt"

// The closed codes a front part is refused with (A2). A widget's own refusals are the widget
// loader's (internal/widget/params) and are listed beside these.
const (
	// RefusalThemeMalformed is a theme file that is not a theme: not JSON, an unknown member, a
	// format_version other than 1, an id that is not [a-z0-9-]+, no English name, or a missing
	// mode.
	RefusalThemeMalformed = "theme_malformed"
	// RefusalThemeTokenMissing is a mode that leaves out a token of the core's set.
	RefusalThemeTokenMissing = "theme_token_missing"
	// RefusalThemeTokenUnknown is a mode that maps a token outside the core's set.
	RefusalThemeTokenUnknown = "theme_token_unknown"
	// RefusalThemeValueInvalid is a token whose value is not a CSS hex, rgb() or rgba() colour,
	// or var(--<token of the set>).
	RefusalThemeValueInvalid = "theme_value_invalid"
	// RefusalThemeIDMismatch is a theme whose id is not its file's base name.
	RefusalThemeIDMismatch = "theme_id_mismatch"
	// RefusalLanguageMalformed is a language file that is not a catalogue: not JSON, an unknown
	// member, a code that is not a language tag, or a string that is not a string.
	RefusalLanguageMalformed = "language_malformed"
	// RefusalLanguageCodeMismatch is a language file whose code is not its file's base name.
	RefusalLanguageCodeMismatch = "language_code_mismatch"
	// RefusalLanguageNameMissing is a language file with no strings["language.name"].
	RefusalLanguageNameMissing = "language_name_missing"
	// RefusalNameCollision is a part whose id, language code or type a built-in or another part
	// already takes: the built-in wins every collision, and the first part by name among the
	// others.
	RefusalNameCollision = "name_collision"
	// RefusalStringOutsideNamespace is a widget directory's string file defining a key outside
	// widget.<type>.*: the file is refused, and the widget is served without its strings.
	RefusalStringOutsideNamespace = "string_outside_namespace"
)

// The kinds of part a refusal is about.
const (
	PartTheme         = "theme"
	PartLanguage      = "language"
	PartWidget        = "widget"
	PartWidgetStrings = "widget_strings"
)

// Refusal is one part left out of the index, with why.
type Refusal struct {
	// Part is the kind of part: theme, language, widget or widget_strings.
	Part string `json:"part"`
	// ID is the part's id, language code or type, or its file name when none could be read.
	ID string `json:"id"`
	// Code is one of the closed codes above, or a widget loader's.
	Code string `json:"code"`
	// Path is the file or directory the part was read from.
	Path string `json:"path"`
	// Detail says what, precisely, was wrong: the operator's diagnostic, in the log's words and
	// never drawn as a sentence of the interface.
	Detail string `json:"detail"`
}

// Error names the part and the code.
func (r Refusal) Error() string {
	return fmt.Sprintf("front: the %s %q at %s was refused (%s): %s", r.Part, r.ID, r.Path, r.Code, r.Detail)
}

func refuse(part, id, path, code, format string, arguments ...any) Refusal {
	return Refusal{Part: part, ID: id, Path: path, Code: code, Detail: fmt.Sprintf(format, arguments...)}
}
