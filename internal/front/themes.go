package front

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Themes.
//
// A THEME IS ONE FILE: {"format_version": 1, "id", "name": {<language>: <name>}, "modes":
// {"light": {...}, "dark": {...}}}, each mode mapping EXACTLY the core's colour-token set
// (front/tokens.json) to a CSS hex, rgb() or rgba() colour, or to var(--<token of the set>). A
// palette's proper name is data, as a plugin's display name is, so the file carries it, keyed by
// language, English required. Non-colour tokens -- the radius, the fonts, the spacing -- are the
// core's and no theme can carry them (decision D10).
//
// THE BROWSER NEVER READS A THEME FILE TO DRAW. At start every valid theme becomes two blocks of
// one generated stylesheet, palettes.css, keyed by the document's data-theme attribute,
// "<id>-light" and "<id>-dark"; the document with no attribute takes the default palette, light,
// or dark under the operating system's dark preference -- which is the first-launch behaviour
// before any script has run. Every value written there was validated here first.

// DefaultPalette is the palette an installation that chose none is drawn with: the maintainer's
// industrial palette (ROADMAP.md; docs/ui-references.md, "The product as decided").
const DefaultPalette = "industrial"

// ThemeFormatVersion is the theme file format this build reads.
const ThemeFormatVersion = 1

// The two modes every theme carries.
const (
	ModeLight = "light"
	ModeDark  = "dark"
)

// Theme is one validated theme.
type Theme struct {
	ID     string
	Name   map[string]string
	Modes  map[string]map[string]string
	Origin string
	// File is the file it was read from, for a refusal of a later part colliding with it.
	File string
	// Source is the file's bytes, served at /front/themes/<id>.json.
	Source []byte
}

// The origins of a part.
const (
	OriginBuiltin       = "builtin"
	OriginDataDirectory = "data_directory"
)

var (
	partIDPattern  = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	hexColour      = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)
	rgbColour      = regexp.MustCompile(`^rgb\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})\s*\)$`)
	rgbaColour     = regexp.MustCompile(`^rgba\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(0|1|0?\.\d+|1\.0+)\s*\)$`)
	tokenReference = regexp.MustCompile(`^var\(--([a-z0-9-]+)\)$`)
)

// tokenSet is the core's colour-token set, read from front/tokens.json.
type tokenSet struct {
	ordered []string
	known   map[string]bool
}

// readTokenSet reads the core's token set.
func readTokenSet(builtin fs.FS) (tokenSet, error) {
	raw, err := fs.ReadFile(builtin, "tokens.json")
	if err != nil {
		return tokenSet{}, fmt.Errorf("front: reading the token set: %w", err)
	}
	var file struct {
		FormatVersion int      `json:"format_version"`
		Description   string   `json:"description"`
		Tokens        []string `json:"tokens"`
	}
	if err := strictDecode(raw, &file); err != nil {
		return tokenSet{}, fmt.Errorf("front: the token set: %w", err)
	}
	set := tokenSet{known: map[string]bool{}}
	for _, token := range file.Tokens {
		if !partIDPattern.MatchString(token) || set.known[token] {
			return tokenSet{}, fmt.Errorf("front: the token set lists %q, which is not a token name or is listed twice", token)
		}
		set.known[token] = true
		set.ordered = append(set.ordered, token)
	}
	if len(set.ordered) == 0 {
		return tokenSet{}, errors.New("front: the token set lists no token")
	}
	return set, nil
}

// strictDecode reads exactly one JSON value into target, refusing an unknown member.
func strictDecode(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("data after the object")
	}
	return nil
}

// parseTheme reads and validates one theme file. id is the file's base name.
func parseTheme(tokens tokenSet, id, path string, raw []byte) (Theme, *Refusal) {
	var file struct {
		FormatVersion int                          `json:"format_version"`
		ID            string                       `json:"id"`
		Name          map[string]string            `json:"name"`
		Modes         map[string]map[string]string `json:"modes"`
	}
	fail := func(code, format string, arguments ...any) (Theme, *Refusal) {
		refusal := refuse(PartTheme, id, path, code, format, arguments...)
		return Theme{}, &refusal
	}
	if err := strictDecode(raw, &file); err != nil {
		return fail(RefusalThemeMalformed, "the file is not a theme: %v", err)
	}
	if file.FormatVersion != ThemeFormatVersion {
		return fail(RefusalThemeMalformed, "format_version is %d and this build reads %d", file.FormatVersion,
			ThemeFormatVersion)
	}
	if !partIDPattern.MatchString(file.ID) {
		return fail(RefusalThemeMalformed, "id %q is not lower-case letters and digits joined by hyphens", file.ID)
	}
	if file.ID != id {
		return fail(RefusalThemeIDMismatch, "the file %s.json declares the id %q: they must be the same", id, file.ID)
	}
	if strings.TrimSpace(file.Name["en"]) == "" {
		return fail(RefusalThemeMalformed, "name carries no English name (name.en)")
	}
	for language, name := range file.Name {
		if !languagePattern.MatchString(language) || strings.TrimSpace(name) == "" {
			return fail(RefusalThemeMalformed, "name.%s is not a language code with a name", language)
		}
	}
	for mode := range file.Modes {
		if mode != ModeLight && mode != ModeDark {
			return fail(RefusalThemeMalformed, "modes carries %q; the modes are %q and %q", mode, ModeLight, ModeDark)
		}
	}
	for _, mode := range []string{ModeLight, ModeDark} {
		values, present := file.Modes[mode]
		if !present {
			return fail(RefusalThemeMalformed, "modes.%s is missing: every theme carries both modes", mode)
		}
		for _, token := range tokens.ordered {
			if _, mapped := values[token]; !mapped {
				return fail(RefusalThemeTokenMissing, "modes.%s leaves out the token %q", mode, token)
			}
		}
		unknown := []string{}
		for token := range values {
			if !tokens.known[token] {
				unknown = append(unknown, token)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return fail(RefusalThemeTokenUnknown, "modes.%s maps %v, which the core's token set does not hold", mode, unknown)
		}
		for _, token := range tokens.ordered {
			if reason := colourValueRefusal(tokens, token, values[token]); reason != "" {
				return fail(RefusalThemeValueInvalid, "modes.%s.%s: %s", mode, token, reason)
			}
		}
		// A chain of var() references that comes back on itself is no colour at all in CSS.
		for _, token := range tokens.ordered {
			seen := map[string]bool{token: true}
			for current := token; ; {
				match := tokenReference.FindStringSubmatch(values[current])
				if match == nil {
					break
				}
				if seen[match[1]] {
					return fail(RefusalThemeValueInvalid, "modes.%s.%s: its var() references come back to %q",
						mode, token, match[1])
				}
				seen[match[1]] = true
				current = match[1]
			}
		}
	}
	return Theme{ID: file.ID, Name: file.Name, Modes: file.Modes, File: path, Source: raw}, nil
}

// colourValueRefusal is why a token's value is not a colour this build writes, or "".
func colourValueRefusal(tokens tokenSet, token, value string) string {
	if hexColour.MatchString(value) {
		return ""
	}
	match := rgbColour.FindStringSubmatch(value)
	if match == nil {
		match = rgbaColour.FindStringSubmatch(value)
	}
	if match != nil {
		for _, component := range match[1:4] {
			if number, err := strconv.Atoi(component); err != nil || number > 255 {
				return fmt.Sprintf("%q has a component above 255", value)
			}
		}
		return ""
	}
	if match := tokenReference.FindStringSubmatch(value); match != nil {
		if !tokens.known[match[1]] {
			return fmt.Sprintf("%q names a token outside the core's set", value)
		}
		if match[1] == token {
			return fmt.Sprintf("%q names the token itself", value)
		}
		return ""
	}
	return fmt.Sprintf("%q is not a hex, rgb() or rgba() colour, nor var(--<token>)", value)
}

// palettesStylesheet writes the generated stylesheet: the default palette for a document with no
// data-theme attribute, following the operating system's preference, then every theme's two
// modes keyed by the attribute.
func palettesStylesheet(tokens tokenSet, themes []Theme) []byte {
	var out bytes.Buffer
	out.WriteString("/* Generated at start from the theme files (internal/front/themes.go). Not a file of the tree. */\n")
	block := func(selector string, values map[string]string, indent string) {
		out.WriteString(indent + selector + " {\n")
		for _, token := range tokens.ordered {
			out.WriteString(indent + "  --" + token + ": " + values[token] + ";\n")
		}
		out.WriteString(indent + "}\n")
	}
	for _, theme := range themes {
		if theme.ID != DefaultPalette {
			continue
		}
		block(":root:not([data-theme])", theme.Modes[ModeLight], "")
		out.WriteString("@media (prefers-color-scheme: dark) {\n")
		block(":root:not([data-theme])", theme.Modes[ModeDark], "  ")
		out.WriteString("}\n")
	}
	for _, theme := range themes {
		block(`:root[data-theme="`+theme.ID+`-light"]`, theme.Modes[ModeLight], "")
		block(`:root[data-theme="`+theme.ID+`-dark"]`, theme.Modes[ModeDark], "")
	}
	return out.Bytes()
}
