package dataset

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
)

// The core's datasets, one directory each under core/:
//
//	core/<name>/dataset.json   the declaration, in the format of a plugin table's columns
//	core/<name>/relation.sql   the SELECT it reads, which may use :from and :to
//	core/<name>/earliest.sql   optional: the instant of its oldest row, for its coverage
//	core/<name>/locale/*.json  its label, under the key dataset.<name>
//
// They are found by walking the embedded tree, so adding one is adding a directory: no list,
// switch or registry names them.
//
//go:embed core
var coreFiles embed.FS

// coreDeclaration is a dataset.json file.
type coreDeclaration struct {
	Nature    string     `json:"nature"`
	Freshness Freshness  `json:"freshness"`
	Retention *Retention `json:"retention,omitempty"`
	Columns   []Column   `json:"columns"`
}

var (
	coreOnce     sync.Once
	coreDatasets []Declaration
	coreStrings  map[string]map[string]string
	coreErr      error
)

// Core returns the core's datasets, ordered by name. A directory that cannot be read is a defect
// of the build, reported as an error.
func Core() ([]Declaration, error) {
	coreOnce.Do(loadCore)
	return append([]Declaration(nil), coreDatasets...), coreErr
}

// Strings returns the core datasets' catalogue strings in a language.
func Strings(language string) map[string]string {
	coreOnce.Do(loadCore)
	result := map[string]string{}
	for key, value := range coreStrings[language] {
		result[key] = value
	}
	return result
}

func loadCore() {
	coreDatasets, coreStrings, coreErr = readTree(coreFiles)
}

// readTree reads every dataset directory under core/ of a tree: the embedded one, or a test's.
func readTree(fsys fs.FS) ([]Declaration, map[string]map[string]string, error) {
	texts := map[string]map[string]string{}
	entries, err := fs.ReadDir(fsys, "core")
	if err != nil {
		return nil, nil, fmt.Errorf("dataset: reading the core datasets: %w", err)
	}
	var declarations []Declaration
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		declaration, own, err := readCore(fsys, entry.Name())
		if err != nil {
			return nil, nil, err
		}
		declarations = append(declarations, declaration)
		for language, localised := range own {
			if texts[language] == nil {
				texts[language] = map[string]string{}
			}
			for key, value := range localised {
				texts[language][key] = value
			}
		}
	}
	sort.Slice(declarations, func(i, j int) bool { return declarations[i].Name < declarations[j].Name })
	return declarations, texts, nil
}

// readCore reads one core dataset's directory.
func readCore(fsys fs.FS, name string) (Declaration, map[string]map[string]string, error) {
	directory := path.Join("core", name)
	if !namePattern.MatchString(name) {
		return Declaration{}, nil, fmt.Errorf("dataset: the core dataset directory %q is not a name", name)
	}
	raw, err := fs.ReadFile(fsys, path.Join(directory, "dataset.json"))
	if err != nil {
		return Declaration{}, nil, fmt.Errorf("dataset %s: %w", name, err)
	}
	var file coreDeclaration
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return Declaration{}, nil, fmt.Errorf("dataset %s: dataset.json: %w", name, err)
	}
	relation, err := fs.ReadFile(fsys, path.Join(directory, "relation.sql"))
	if err != nil {
		return Declaration{}, nil, fmt.Errorf("dataset %s: %w", name, err)
	}
	translated, err := windowRelation(strings.TrimRight(strings.TrimSpace(sqlBody(string(relation))), ";"))
	if err != nil {
		return Declaration{}, nil, fmt.Errorf("dataset %s: relation.sql: %w", name, err)
	}
	declaration := Declaration{Name: name, Origin: OriginCore, Label: "dataset." + name, Nature: file.Nature,
		Freshness: file.Freshness, Retention: file.Retention, Columns: file.Columns, relation: translated}
	if earliest, err := fs.ReadFile(fsys, path.Join(directory, "earliest.sql")); err == nil {
		declaration.earliest = strings.TrimRight(strings.TrimSpace(sqlBody(string(earliest))), ";")
	}
	declaration, err = Normalise(declaration)
	if err != nil {
		return Declaration{}, nil, err
	}
	texts := map[string]map[string]string{}
	locales, _ := fs.ReadDir(fsys, path.Join(directory, "locale"))
	for _, locale := range locales {
		if locale.IsDir() || !strings.HasSuffix(locale.Name(), ".json") {
			continue
		}
		raw, err := fs.ReadFile(fsys, path.Join(directory, "locale", locale.Name()))
		if err != nil {
			return Declaration{}, nil, fmt.Errorf("dataset %s: %w", name, err)
		}
		var catalogue struct {
			Language string            `json:"language"`
			Strings  map[string]string `json:"strings"`
		}
		if err := json.Unmarshal(raw, &catalogue); err != nil {
			return Declaration{}, nil, fmt.Errorf("dataset %s: locale/%s: %w", name, locale.Name(), err)
		}
		for key := range catalogue.Strings {
			if key != declaration.Label {
				return Declaration{}, nil, fmt.Errorf("dataset %s: locale/%s defines %q, which is not its label",
					name, locale.Name(), key)
			}
		}
		texts[catalogue.Language] = catalogue.Strings
	}
	return declaration, texts, nil
}

// sqlBody drops the comment lines of a SQL file.
func sqlBody(text string) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
