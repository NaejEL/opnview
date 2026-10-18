package front

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/NaejEL/opnview/internal/widget"
)

// Options are what the index is built from.
type Options struct {
	// Builtin is the built-in front, rooted at the repository's front/ (opnview.BuiltinFront).
	Builtin fs.FS
	// DataDir is the data directory: themes/ and languages/ are read below it. Empty reads none.
	DataDir string
	// Widgets is the widget service: its loaded widgets' renderers are indexed, and an operator or
	// front-only widget's strings read from its directory. Nil indexes no widget.
	Widgets *widget.Service
	// CoreStrings answers the core's, the mounted surfaces' and the loaded plugins' strings of one
	// language (web.Host's CatalogueStrings). Nil is none.
	CoreStrings func(language string) map[string]string
}

// Asset is one file the front is served.
type Asset struct {
	// Path is the published path below /front/.
	Path string
	Body []byte
	// ContentType is the media type, with its charset for text.
	ContentType string
	// ETag is the content's tag, quoted.
	ETag string
}

// Catalogue is one language's merged strings and their tag.
type Catalogue struct {
	Language string
	Strings  map[string]string
	ETag     string
	// Missing are the keys of the English catalogue this language does not carry, for a language
	// of the data directory; empty for English.
	Missing []string
}

// Index is everything the front is served, built once at start.
type Index struct {
	assets     map[string]*Asset
	shell      *Asset
	themes     []Theme
	languages  []Language
	catalogues map[string]*Catalogue
	widgets    []WidgetRow
	shapes     []ShapeRow
	refused    []Refusal
	scripts    []string
	sheets     []string

	// THE CATALOGUES ARE MERGED ON FIRST USE, not at Build: the core's strings are those of the
	// surfaces the server accepted, which it knows only once every surface -- this one among them
	// -- has been vetted. coreStrings is asked then, once.
	merged        sync.Once
	coreStrings   func(language string) map[string]string
	widgetStrings map[string]map[string]string
}

// The paths of the core's own assets.
const (
	// AssetPrefix is the path every asset is published under.
	AssetPrefix = "/front/"
	// shellFile is the shell document of the built-in front.
	shellFile = "shell.html"
	// stylesheetFile is the core's stylesheet.
	stylesheetFile = "style.css"
	// PalettesPath is the generated palette stylesheet's published path below AssetPrefix.
	PalettesPath = "palettes.css"
)

// The directories of the built-in front whose files are served as they are, each its media type.
var servedDirectories = map[string]string{
	"core":   "text/javascript; charset=utf-8",
	"views":  "text/javascript; charset=utf-8",
	"shapes": "text/javascript; charset=utf-8",
}

// Build builds the index. It fails only on a defect of the build: a built-in part that breaks a
// rule. A part of the data directory that breaks one is refused, listed, and left out.
func Build(options Options) (*Index, error) {
	if options.Builtin == nil {
		return nil, errors.New("front: no built-in front was given")
	}
	index := &Index{assets: map[string]*Asset{}, catalogues: map[string]*Catalogue{}}
	shell, err := fs.ReadFile(options.Builtin, shellFile)
	if err != nil {
		return nil, fmt.Errorf("front: reading the shell: %w", err)
	}
	index.shell = newAsset(shellFile, shell, "text/html; charset=utf-8")
	stylesheet, err := fs.ReadFile(options.Builtin, stylesheetFile)
	if err != nil {
		return nil, fmt.Errorf("front: reading the stylesheet: %w", err)
	}
	index.add(newAsset(stylesheetFile, stylesheet, "text/css; charset=utf-8"))
	directories := make([]string, 0, len(servedDirectories))
	for directory := range servedDirectories {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	for _, directory := range directories {
		entries, err := fs.ReadDir(options.Builtin, directory)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("front: listing %s: %w", directory, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".js") {
				continue
			}
			published := directory + "/" + entry.Name()
			body, err := fs.ReadFile(options.Builtin, published)
			if err != nil {
				return nil, fmt.Errorf("front: reading %s: %w", published, err)
			}
			index.add(newAsset(published, body, servedDirectories[directory]))
			if directory == "shapes" {
				index.shapes = append(index.shapes, ShapeRow{Shape: strings.TrimSuffix(entry.Name(), ".js"),
					URL: AssetPrefix + published})
			}
		}
	}
	if err := index.readThemes(options); err != nil {
		return nil, err
	}
	if err := index.readLanguages(options); err != nil {
		return nil, err
	}
	index.widgetStrings = index.readWidgets(options)
	index.coreStrings = options.CoreStrings
	if err := index.readShellReferences(); err != nil {
		return nil, err
	}
	sort.SliceStable(index.refused, func(i, j int) bool {
		if index.refused[i].Part != index.refused[j].Part {
			return index.refused[i].Part < index.refused[j].Part
		}
		return index.refused[i].Path < index.refused[j].Path
	})
	return index, nil
}

// newAsset tags one asset by its content.
func newAsset(published string, body []byte, contentType string) *Asset {
	sum := sha256.Sum256(body)
	return &Asset{Path: published, Body: body, ContentType: contentType,
		ETag: `"` + hex.EncodeToString(sum[:16]) + `"`}
}

func (i *Index) add(asset *Asset) { i.assets[asset.Path] = asset }

// readThemes reads the built-in themes, then the data directory's, and generates the palette
// stylesheet from those accepted.
func (i *Index) readThemes(options Options) error {
	tokens, err := readTokenSet(options.Builtin)
	if err != nil {
		return err
	}
	entries, err := fs.ReadDir(options.Builtin, "themes")
	if err != nil {
		return fmt.Errorf("front: listing the built-in themes: %w", err)
	}
	taken := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		raw, err := fs.ReadFile(options.Builtin, "themes/"+entry.Name())
		if err != nil {
			return fmt.Errorf("front: reading the built-in theme %s: %w", entry.Name(), err)
		}
		theme, refusal := parseTheme(tokens, id, "front/themes/"+entry.Name(), raw)
		if refusal != nil {
			return refusal
		}
		theme.Origin = OriginBuiltin
		i.themes = append(i.themes, theme)
		taken[theme.ID] = theme.File
	}
	if _, present := taken[DefaultPalette]; !present {
		return fmt.Errorf("front: the default palette %q has no built-in theme file", DefaultPalette)
	}
	if options.DataDir != "" {
		directory := filepath.Join(options.DataDir, "themes")
		for _, name := range dataDirectoryFiles(directory) {
			id := strings.TrimSuffix(name, ".json")
			file := filepath.Join(directory, name)
			raw, err := os.ReadFile(file)
			if err != nil {
				i.refused = append(i.refused, refuse(PartTheme, id, file, RefusalThemeMalformed, "the file cannot be read: %v", err))
				continue
			}
			theme, refusal := parseTheme(tokens, id, file, raw)
			if refusal != nil {
				i.refused = append(i.refused, *refusal)
				continue
			}
			if holder, collides := taken[theme.ID]; collides {
				i.refused = append(i.refused, refuse(PartTheme, theme.ID, file, RefusalNameCollision,
					"the id %q is already taken by %s", theme.ID, holder))
				continue
			}
			theme.Origin = OriginDataDirectory
			i.themes = append(i.themes, theme)
			taken[theme.ID] = file
		}
	}
	// The default first, then by English name: the order a selector offers them in.
	sort.SliceStable(i.themes, func(a, b int) bool {
		if (i.themes[a].ID == DefaultPalette) != (i.themes[b].ID == DefaultPalette) {
			return i.themes[a].ID == DefaultPalette
		}
		return strings.ToLower(i.themes[a].Name["en"]) < strings.ToLower(i.themes[b].Name["en"])
	})
	for _, theme := range i.themes {
		i.add(newAsset("themes/"+theme.ID+".json", theme.Source, "application/json"))
	}
	i.add(newAsset(PalettesPath, palettesStylesheet(tokens, i.themes), "text/css; charset=utf-8"))
	return nil
}

// readLanguages reads the built-in languages, then the data directory's.
func (i *Index) readLanguages(options Options) error {
	builtins, err := builtinLanguages(options.Builtin)
	if err != nil {
		return fmt.Errorf("front: the built-in languages: %w", err)
	}
	taken := map[string]string{}
	for _, language := range builtins {
		i.languages = append(i.languages, language)
		taken[language.Code] = language.File
	}
	if _, present := taken[SourceLanguage]; !present {
		return fmt.Errorf("front: the source language %q has no built-in file", SourceLanguage)
	}
	if options.DataDir == "" {
		return nil
	}
	directory := filepath.Join(options.DataDir, "languages")
	for _, name := range dataDirectoryFiles(directory) {
		code := strings.TrimSuffix(name, ".json")
		file := filepath.Join(directory, name)
		raw, err := os.ReadFile(file)
		if err != nil {
			i.refused = append(i.refused, refuse(PartLanguage, code, file, RefusalLanguageMalformed, "the file cannot be read: %v", err))
			continue
		}
		language, refusal := parseLanguage(code, file, OriginDataDirectory, raw)
		if refusal != nil {
			i.refused = append(i.refused, *refusal)
			continue
		}
		if holder, collides := taken[language.Code]; collides {
			i.refused = append(i.refused, refuse(PartLanguage, language.Code, file, RefusalNameCollision,
				"the language %q is already installed by %s", language.Code, holder))
			continue
		}
		i.languages = append(i.languages, language)
		taken[language.Code] = file
	}
	sort.SliceStable(i.languages, func(a, b int) bool {
		if (i.languages[a].Code == SourceLanguage) != (i.languages[b].Code == SourceLanguage) {
			return i.languages[a].Code == SourceLanguage
		}
		return i.languages[a].Code < i.languages[b].Code
	})
	return nil
}

// ensureCatalogues merges the catalogues once.
func (i *Index) ensureCatalogues() {
	i.merged.Do(func() {
		i.mergeCatalogues(i.coreStrings, i.widgetStrings)
		i.noteMissingWidgetStrings()
	})
}

// mergeCatalogues builds every installed language's catalogue: the core's, the front's own, the
// widgets' and the language file's, the first holder of a key winning.
func (i *Index) mergeCatalogues(coreStrings func(string) map[string]string, widgetStrings map[string]map[string]string) {
	for _, language := range i.languages {
		merged := map[string]string{}
		layer := func(entries map[string]string) {
			for key, text := range entries {
				if _, held := merged[key]; !held {
					merged[key] = text
				}
			}
		}
		if coreStrings != nil {
			layer(coreStrings(language.Code))
		}
		if language.Origin == OriginBuiltin {
			layer(language.Strings)
		}
		layer(widgetStrings[language.Code])
		if language.Origin != OriginBuiltin {
			layer(language.Strings)
		}
		i.catalogues[language.Code] = &Catalogue{Language: language.Code, Strings: merged, ETag: catalogueTag(merged)}
	}
	english := i.catalogues[SourceLanguage]
	for code, catalogue := range i.catalogues {
		if code == SourceLanguage {
			continue
		}
		for key := range english.Strings {
			if _, carried := catalogue.Strings[key]; !carried {
				catalogue.Missing = append(catalogue.Missing, key)
			}
		}
		sort.Strings(catalogue.Missing)
	}
}

// catalogueTag tags a catalogue by its content.
func catalogueTag(entries map[string]string) string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	digest := sha256.New()
	for _, key := range keys {
		digest.Write([]byte(key))
		digest.Write([]byte{0})
		digest.Write([]byte(entries[key]))
		digest.Write([]byte{0})
	}
	return `"` + hex.EncodeToString(digest.Sum(nil)[:16]) + `"`
}

// readShellReferences reads the scripts and stylesheets the shell loads, so /api/front can list the
// core's asset URLs, and checks that each is an asset of the index: a shell naming a file nobody
// declared is a defect of the build.
func (i *Index) readShellReferences() error {
	document := string(i.shell.Body)
	for _, attribute := range []string{`src="`, `href="`} {
		rest := document
		for {
			start := strings.Index(rest, attribute)
			if start < 0 {
				break
			}
			rest = rest[start+len(attribute):]
			end := strings.Index(rest, `"`)
			if end < 0 {
				break
			}
			reference := rest[:end]
			rest = rest[end:]
			if !strings.HasPrefix(reference, AssetPrefix) {
				continue
			}
			if _, declared := i.assets[strings.TrimPrefix(reference, AssetPrefix)]; !declared {
				return fmt.Errorf("front: the shell loads %s, which the index does not declare", reference)
			}
			if attribute == `src="` {
				i.scripts = append(i.scripts, reference)
			} else {
				i.sheets = append(i.sheets, reference)
			}
		}
	}
	return nil
}

// Asset returns a published asset by its path below /front/.
func (i *Index) Asset(published string) (*Asset, bool) {
	asset, found := i.assets[published]
	return asset, found
}

// Shell returns the shell document.
func (i *Index) Shell() *Asset { return i.shell }

// AssetPaths returns every published path, sorted, for a test.
func (i *Index) AssetPaths() []string {
	paths := make([]string, 0, len(i.assets))
	for published := range i.assets {
		paths = append(paths, published)
	}
	sort.Strings(paths)
	return paths
}

// Themes returns the accepted themes, the default first.
func (i *Index) Themes() []Theme { return append([]Theme(nil), i.themes...) }

// Languages returns the installed languages, English first.
func (i *Index) Languages() []Language { return append([]Language(nil), i.languages...) }

// Catalogue returns one installed language's catalogue.
func (i *Index) Catalogue(language string) (*Catalogue, bool) {
	i.ensureCatalogues()
	catalogue, found := i.catalogues[language]
	return catalogue, found
}

// Refused returns every part left out, by kind then path.
func (i *Index) Refused() []Refusal { return append([]Refusal(nil), i.refused...) }

// Scripts and Stylesheets are the core's asset URLs, in the order the shell loads them.
func (i *Index) Scripts() []string     { return append([]string(nil), i.scripts...) }
func (i *Index) Stylesheets() []string { return append([]string(nil), i.sheets...) }

// HasTheme and HasLanguage report whether an id or a code is installed.
func (i *Index) HasTheme(id string) bool {
	for _, theme := range i.themes {
		if theme.ID == id {
			return true
		}
	}
	return false
}

// HasLanguage reports whether a language code is installed.
func (i *Index) HasLanguage(code string) bool {
	for _, language := range i.languages {
		if language.Code == code {
			return true
		}
	}
	return false
}

// rendererPath is the published path of one widget's renderer.
func rendererPath(widgetType, entry string) string { return path.Join("widgets", widgetType, entry) }
