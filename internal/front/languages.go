package front

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Languages and strings.
//
// A LANGUAGE IS ONE FILE, in the format of every catalogue file of the product:
// {"language": "<BCP 47 code>", "strings": {key: text}}, the code equal to the file's base name,
// and strings["language.name"] -- the language's name in itself -- required. It may carry any key
// of any namespace: the core's, a surface's, a plugin's, a widget's. English is the source
// language and the only one the product ships; a second language is a file dropped into
// <data-dir>/languages/ and a restart.
//
// THE CATALOGUE OF ONE LANGUAGE is the union, in this order and the first holder of a key winning,
// of the core's and the mounted surfaces' and the loaded plugins' strings (web.Host's
// CatalogueStrings), the front's own locale file, every operator or front-only widget's locale file,
// and the language file. The browser looks a key up in the active language, then in English, then
// shows the key itself marked as missing (front/core/strings.js).

// LanguageNameKey is the key a language file names its language with, in that language.
const LanguageNameKey = "language.name"

// SourceLanguage is the language every other is translated from, and the last before a missing
// key.
const SourceLanguage = "en"

var languagePattern = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// Language is one installed language.
type Language struct {
	Code   string
	Name   string
	Origin string
	File   string
	// Strings are the file's own strings.
	Strings map[string]string
}

// catalogueFile is the shape of every catalogue file.
type catalogueFile struct {
	Language string                     `json:"language"`
	Strings  map[string]json.RawMessage `json:"strings"`
}

// parseCatalogue reads one catalogue file: its code and its strings, every value a string.
func parseCatalogue(part, id, path string, raw []byte, code string) (string, map[string]string, *Refusal) {
	fail := func(refusalCode, format string, arguments ...any) (string, map[string]string, *Refusal) {
		refusal := refuse(part, id, path, refusalCode, format, arguments...)
		return "", nil, &refusal
	}
	var file catalogueFile
	if err := strictDecode(raw, &file); err != nil {
		return fail(RefusalLanguageMalformed, "the file is not a catalogue: %v", err)
	}
	if !languagePattern.MatchString(file.Language) {
		return fail(RefusalLanguageMalformed, "language %q is not a language code", file.Language)
	}
	if file.Language != code {
		return fail(RefusalLanguageCodeMismatch, "the file %s.json declares the language %q: they must be the same",
			code, file.Language)
	}
	entries := make(map[string]string, len(file.Strings))
	for key, value := range file.Strings {
		var text string
		if err := json.Unmarshal(value, &text); err != nil || string(value) == "null" {
			return fail(RefusalLanguageMalformed, "strings[%q] is not a string", key)
		}
		entries[key] = text
	}
	return file.Language, entries, nil
}

// parseLanguage reads and validates one language file. code is the file's base name.
func parseLanguage(code, path, origin string, raw []byte) (Language, *Refusal) {
	language, entries, refusal := parseCatalogue(PartLanguage, code, path, raw, code)
	if refusal != nil {
		return Language{}, refusal
	}
	name := strings.TrimSpace(entries[LanguageNameKey])
	if name == "" {
		failed := refuse(PartLanguage, code, path, RefusalLanguageNameMissing,
			"strings[%q], the language's name in itself, is missing", LanguageNameKey)
		return Language{}, &failed
	}
	return Language{Code: language, Name: name, Origin: origin, File: path, Strings: entries}, nil
}

// builtinLanguages reads the front's own locale files, front/locale/<code>.json. A broken one is a
// defect of the build.
func builtinLanguages(builtin fs.FS) ([]Language, error) {
	entries, err := fs.ReadDir(builtin, "locale")
	if err != nil {
		return nil, err
	}
	var languages []Language
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		code := strings.TrimSuffix(entry.Name(), ".json")
		path := "front/locale/" + entry.Name()
		raw, err := fs.ReadFile(builtin, "locale/"+entry.Name())
		if err != nil {
			return nil, err
		}
		language, refusal := parseLanguage(code, path, OriginBuiltin, raw)
		if refusal != nil {
			return nil, refusal
		}
		languages = append(languages, language)
	}
	return languages, nil
}

// dataDirectoryFiles lists the regular .json files of one directory of the data directory, by
// name; a directory that does not exist holds none.
func dataDirectoryFiles(directory string) []string {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") && !strings.HasPrefix(entry.Name(), ".") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

// widgetStrings reads an operator or front-only widget's locale/ directory: each file a catalogue of
// one language whose keys are all widget.<type>.*. A file breaking a rule is refused, and the
// widget is served without it.
func widgetStrings(widgetType, directory string) (map[string]map[string]string, []Refusal) {
	found := map[string]map[string]string{}
	var refusals []Refusal
	localeDirectory := filepath.Join(directory, "locale")
	prefix := "widget." + widgetType + "."
	for _, name := range dataDirectoryFiles(localeDirectory) {
		path := filepath.Join(localeDirectory, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			refusals = append(refusals, refuse(PartWidgetStrings, widgetType, path, RefusalLanguageMalformed,
				"the file cannot be read: %v", err))
			continue
		}
		code := strings.TrimSuffix(name, ".json")
		language, entries, refusal := parseCatalogue(PartWidgetStrings, widgetType, path, raw, code)
		if refusal != nil {
			refusals = append(refusals, *refusal)
			continue
		}
		outside := []string{}
		for key := range entries {
			if !strings.HasPrefix(key, prefix) {
				outside = append(outside, key)
			}
		}
		if len(outside) > 0 {
			sort.Strings(outside)
			refusals = append(refusals, refuse(PartWidgetStrings, widgetType, path, RefusalStringOutsideNamespace,
				"the file defines %v, outside the widget's own namespace %s*", outside, prefix))
			continue
		}
		found[language] = entries
	}
	return found, refusals
}
