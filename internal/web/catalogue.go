package web

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"

	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/plugin"
)

// The string catalogue.
//
// NO USER-VISIBLE STRING IS A LITERAL, anywhere in this package or in a surface. ROADMAP.md,
// step 7: every user-visible string comes from a catalogue, none is a literal in a
// template, a handler or a script, and the only exemption is the step-3 mockup.
//
// A LANGUAGE IS A FILE, which is the shape step 7 grows. locale/en.json is that
// file: a language identifier and a flat map from key to string. Adding a second
// language is adding a second file of the same shape and a row in whatever
// selects between them; this cycle ships one, and the language picker is step 7.
// English is the SOURCE language, so en.json is the one every other file is
// translated from.
//
// THE CATALOGUE IS THE UNION OF FILES, ONE SET PER OWNER. The keys the layout and the
// vocabulary shared by several surfaces use are the core's (locale/ here); every other key is
// in the locale/ directory of the one surface that uses it, so a surface carries its own
// words. At start the core merges its file with those of every accepted surface for the
// language; a key defined twice is refused (the surface that redefines it is left out, with
// that reason), never silently overwritten.
//
// A HANDLER NEVER HOLDS A STRING EITHER. What a handler chooses is a KEY -- the MessageKey
// type -- and the template resolves it. That is what makes "no handler emits a
// user-visible literal" a property a test can check rather than a habit.

// locales holds the core's catalogue files.
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

// decodeCatalogue parses one catalogue file and checks that it declares the language its
// name says.
func decodeCatalogue(language string, raw []byte) (Catalogue, error) {
	var catalogue Catalogue
	if err := json.Unmarshal(raw, &catalogue); err != nil {
		return Catalogue{}, fmt.Errorf("decoding the %s catalogue: %w", language, err)
	}
	if catalogue.Language != language {
		return Catalogue{}, fmt.Errorf("the %s catalogue declares the language %q", language, catalogue.Language)
	}
	if len(catalogue.Strings) == 0 {
		return Catalogue{}, fmt.Errorf("the %s catalogue holds no strings", language)
	}
	return catalogue, nil
}

// LoadCatalogue reads the core's catalogue file for one language: the layout's strings and
// the vocabulary shared by several surfaces. The product's catalogue is the union of this
// and every surface's file; Server.Catalogue returns it.
func LoadCatalogue(language string) (Catalogue, error) {
	raw, err := locales.ReadFile("locale/" + language + ".json")
	if err != nil {
		return Catalogue{}, fmt.Errorf("web: reading the %s catalogue: %w", language, err)
	}
	catalogue, err := decodeCatalogue(language, raw)
	if err != nil {
		return Catalogue{}, fmt.Errorf("web: %w", err)
	}
	return catalogue, nil
}

// coreCatalogueKeys is the set of keys the core defines for a language, empty when it has no
// file for it.
func coreCatalogueKeys(language string) map[string]struct{} {
	catalogue, err := LoadCatalogue(language)
	if err != nil {
		return map[string]struct{}{}
	}
	keys := make(map[string]struct{}, len(catalogue.Strings))
	for key := range catalogue.Strings {
		keys[key] = struct{}{}
	}
	return keys
}

// buildCatalogue merges the core's file, the accepted surfaces' files and the loaded plugins'
// strings for a language. A plugin's keys are confined to its own namespace by the loader
// (internal/plugin, string_outside_namespace), so one it shares with the core is not reachable
// from a loaded plugin; were it reached, the core's word is kept.
func buildCatalogue(language string, accepted []vetted, plugins map[string]string) (Catalogue, error) {
	merged, err := LoadCatalogue(language)
	if err != nil {
		return Catalogue{}, err
	}
	combined := make(map[string]string, len(merged.Strings))
	for key, value := range merged.Strings {
		combined[key] = value
	}
	for _, candidate := range accepted {
		for key, value := range candidate.strings[language] {
			if _, duplicate := combined[key]; duplicate {
				// vetSurfaces refuses a duplicate before this point; reaching here is a defect.
				return Catalogue{}, fmt.Errorf("web: the catalogue key %q is defined twice", key)
			}
			combined[key] = value
		}
	}
	for key, value := range plugins {
		if _, taken := combined[key]; !taken {
			combined[key] = value
		}
	}
	merged.Strings = combined
	return merged, nil
}

// Catalogue returns the catalogue this server draws its pages with: the core's strings, those
// of every surface it mounts and those of every plugin it was given.
func (s *Server) Catalogue() Catalogue { return s.renderer.catalogue }

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

// Translate resolves one key, for a test that compares a rendered page against the
// catalogue. A surface never builds a user-visible string with it.
func (c Catalogue) Translate(key MessageKey) (string, error) { return c.translate(string(key)) }

// isMissing reports whether an error is a file that is not there.
func isMissing(err error) bool { return errors.Is(err, fs.ErrNotExist) }

// ProductCatalogue returns the catalogue of every surface linked into this binary and of every
// built-in plugin, together with the core's: what a server built with the registered surfaces
// draws its pages with. A test uses it to name the string a reader sees without building a
// server.
func ProductCatalogue(language string) (Catalogue, error) {
	merged, err := LoadCatalogue(language)
	if err != nil {
		return Catalogue{}, err
	}
	combined := make(map[string]string, len(merged.Strings))
	for key, value := range merged.Strings {
		combined[key] = value
	}
	for _, surface := range RegisteredSurfaces() {
		localised, err := surfaceStrings(surface.Files)
		if err != nil {
			return Catalogue{}, fmt.Errorf("web: the surface %q: %s", surface.Name, err)
		}
		for key, value := range localised[language] {
			combined[key] = value
		}
	}
	for key, value := range withDatasetStrings(language, plugin.BuiltinsLoaded().Strings(language)) {
		if _, taken := combined[key]; !taken {
			combined[key] = value
		}
	}
	merged.Strings = combined
	return merged, nil
}

// CatalogueSources returns the text of every catalogue file of the core and of every
// registered surface, by a path that names its owner. A test reads them to assert what no
// language file may contain.
func CatalogueSources() (map[string]string, error) {
	sources := map[string]string{}
	entries, err := locales.ReadDir("locale")
	if err != nil {
		return nil, fmt.Errorf("web: listing the core's catalogue files: %w", err)
	}
	for _, entry := range entries {
		raw, err := locales.ReadFile("locale/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("web: reading %s: %w", entry.Name(), err)
		}
		sources["locale/"+entry.Name()] = string(raw)
	}
	for _, surface := range RegisteredSurfaces() {
		if surface.Files == nil {
			continue
		}
		localeEntries, err := fs.ReadDir(surface.Files, "locale")
		if err != nil {
			continue
		}
		for _, entry := range localeEntries {
			raw, err := fs.ReadFile(surface.Files, "locale/"+entry.Name())
			if err != nil {
				return nil, fmt.Errorf("web: reading %s of %s: %w", entry.Name(), surface.Name, err)
			}
			sources["surfaces/"+surface.Name+"/locale/"+entry.Name()] = string(raw)
		}
	}
	return sources, nil
}

// withDatasetStrings adds the core datasets' labels to a set of plugin strings: each core dataset
// carries its own (internal/dataset), so adding one edits no shipped catalogue file.
func withDatasetStrings(language string, plugins map[string]string) map[string]string {
	merged := dataset.Strings(language)
	for key, value := range plugins {
		merged[key] = value
	}
	return merged
}
