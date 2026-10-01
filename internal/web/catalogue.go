package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
)

// The string catalogue.
//
// NO USER-VISIBLE STRING IS A LITERAL, anywhere in this package. ROADMAP.md,
// step 7: every user-visible string comes from a catalogue, none is a literal in a
// template, a handler or a script, and the only exemption is the step-3 mockup.
// This cycle ships real interface, so shipping it with literals would break that
// rule the day it was written.
//
// A LANGUAGE IS A FILE, which is the shape step 7 grows. locale/en.json is that
// file: a language identifier and a flat map from key to string. Adding a second
// language is adding a second file of the same shape and a row in whatever
// selects between them; this cycle ships one, and the language picker is step 7.
// English is the SOURCE language, so en.json is the one every other file is
// translated from.
//
// A HANDLER NEVER HOLDS A STRING EITHER. What a handler chooses is a KEY — the
// messageKey type in messages.go — and the template resolves it. That is what
// makes "no handler emits a user-visible literal" a property a test can check
// rather than a habit.

// locales holds the catalogue files.
//
//go:embed locale/*.json
var locales embed.FS

// SourceLanguage is the language every other catalogue is translated from.
const SourceLanguage = "en"

// Catalogue is one language's strings.
type Catalogue struct {
	// Language is the language identifier, which is what a template puts in the
	// document's lang attribute.
	Language string `json:"language"`
	// Strings maps a key to the string a reader sees.
	Strings map[string]string `json:"strings"`
}

// LoadCatalogue reads one language's catalogue file.
func LoadCatalogue(language string) (Catalogue, error) {
	raw, err := locales.ReadFile("locale/" + language + ".json")
	if err != nil {
		return Catalogue{}, fmt.Errorf("web: reading the %s catalogue: %w", language, err)
	}
	var catalogue Catalogue
	if err := json.Unmarshal(raw, &catalogue); err != nil {
		return Catalogue{}, fmt.Errorf("web: decoding the %s catalogue: %w", language, err)
	}
	if catalogue.Language != language {
		return Catalogue{}, fmt.Errorf(
			"web: the %s catalogue declares the language %q", language, catalogue.Language)
	}
	if len(catalogue.Strings) == 0 {
		return Catalogue{}, fmt.Errorf("web: the %s catalogue holds no strings", language)
	}
	return catalogue, nil
}

// Keys returns the catalogue's keys, sorted. A test uses it to assert that every
// entry is reachable and that every key a template or a handler reaches exists.
func (c Catalogue) Keys() []string {
	keys := make([]string, 0, len(c.Strings))
	for key := range c.Strings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// errMissingString is what a template execution fails with when it reaches a key
// the catalogue does not carry.
//
// IT FAILS RATHER THAN FALLING BACK to the key, to the empty string or to
// English. A missing string that renders as its own key ships a debugging artefact
// to a reader; a missing string that renders as nothing ships a blank label. Both
// are worse than a page that does not render, because both pass unnoticed.
type errMissingString struct{ key string }

// Error names the missing key.
func (e errMissingString) Error() string {
	return fmt.Sprintf("web: the catalogue has no string for %q", e.key)
}

// translate resolves one key, or fails.
func (c Catalogue) translate(key string) (string, error) {
	value, present := c.Strings[key]
	if !present {
		return "", errMissingString{key: key}
	}
	return value, nil
}
