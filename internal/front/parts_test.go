package front_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	opnview "github.com/NaejEL/opnview"
	"github.com/NaejEL/opnview/internal/front"
)

// The front's parts of the data directory (A1, A2; F7-AC1 and F7-AC2 for the themes and the
// languages, which need no database): a valid theme or language file dropped into the data
// directory is installed with no file of the repository changed; each malformed one is refused with
// its own code and its path, and the index is still built; a part colliding with a built-in is
// refused name_collision and the built-in is served unchanged.

// industrialModes is a valid pair of modes: the built-in industrial theme's own.
func industrialModes(t *testing.T) map[string]map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repositoryRoot(t), "front", "themes", "industrial.json"))
	if err != nil {
		t.Fatal(err)
	}
	var theme struct {
		Modes map[string]map[string]string `json:"modes"`
	}
	if err := json.Unmarshal(raw, &theme); err != nil {
		t.Fatal(err)
	}
	return theme.Modes
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// themeFile writes one theme file into a data directory.
func themeFile(t *testing.T, dataDir, file string, theme any) {
	t.Helper()
	writeJSON(t, filepath.Join(dataDir, "themes", file), theme)
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, ok := value.(string)
	if !ok {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		raw = string(encoded)
	}
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func theme(id string, modes map[string]map[string]string) map[string]any {
	return map[string]any{"format_version": 1, "id": id, "name": map[string]string{"en": "Example " + id},
		"modes": modes}
}

// copyModes copies a pair of modes, changing one token of one mode by edit.
func copyModes(modes map[string]map[string]string, edit func(map[string]map[string]string)) map[string]map[string]string {
	copied := map[string]map[string]string{}
	for mode, values := range modes {
		copied[mode] = map[string]string{}
		for token, value := range values {
			copied[mode][token] = value
		}
	}
	if edit != nil {
		edit(copied)
	}
	return copied
}

func build(t *testing.T, dataDir string) *front.Index {
	t.Helper()
	index, err := front.Build(front.Options{Builtin: opnview.BuiltinFront(), DataDir: dataDir,
		CoreStrings: func(string) map[string]string { return map[string]string{} }})
	if err != nil {
		t.Fatalf("the index was not built: %v", err)
	}
	return index
}

func themeIDs(index *front.Index) []string {
	var ids []string
	for _, theme := range index.Themes() {
		ids = append(ids, theme.ID)
	}
	sort.Strings(ids)
	return ids
}

func TestAnEmptyDataDirectoryServesTheSixBuiltInThemesAndEnglish(t *testing.T) {
	t.Parallel()
	index := build(t, t.TempDir())
	if got := strings.Join(themeIDs(index), " "); got != "catppuccin dracula industrial nord rose-pine tokyo-night" {
		t.Errorf("the built-in themes are %s", got)
	}
	if themes := index.Themes(); themes[0].ID != front.DefaultPalette {
		t.Errorf("the default palette is not offered first: %s", themes[0].ID)
	}
	languages := index.Languages()
	if len(languages) != 1 || languages[0].Code != "en" || languages[0].Name == "" {
		t.Errorf("the built-in languages are %+v", languages)
	}
	if refused := index.Refused(); len(refused) != 0 {
		t.Errorf("an empty data directory refused %v", refused)
	}
}

func TestAThemeAndALanguageDroppedIntoTheDataDirectoryAreInstalled(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	themeFile(t, dataDir, "example-sepia.json", theme("example-sepia", industrialModes(t)))
	writeJSON(t, filepath.Join(dataDir, "languages", "fr.json"), map[string]any{"language": "fr",
		"strings": map[string]string{"language.name": "Français", "settings.title": "Réglages"}})
	index := build(t, dataDir)
	if !index.HasTheme("example-sepia") || !index.HasLanguage("fr") {
		t.Fatalf("the dropped parts are not installed: themes %v, refused %v", themeIDs(index), index.Refused())
	}
	if _, served := index.Asset("themes/example-sepia.json"); !served {
		t.Error("the dropped theme file is not served")
	}
	palettes, _ := index.Asset(front.PalettesPath)
	if !strings.Contains(string(palettes.Body), `:root[data-theme="example-sepia-dark"]`) {
		t.Error("the dropped theme has no block in the generated palette stylesheet")
	}
	french, _ := index.Catalogue("fr")
	if french.Strings["settings.title"] != "Réglages" {
		t.Errorf("the language file's strings are not in its catalogue")
	}
	// Removing them and building again removes them.
	if err := os.RemoveAll(filepath.Join(dataDir, "themes")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dataDir, "languages")); err != nil {
		t.Fatal(err)
	}
	again := build(t, dataDir)
	if again.HasTheme("example-sepia") || again.HasLanguage("fr") {
		t.Error("the removed parts are still installed after a rebuild")
	}
}

func TestEachMalformedThemeOrLanguageIsRefusedWithItsOwnCodeAndPath(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	modes := industrialModes(t)
	themeFile(t, dataDir, "example-malformed.json", `{"format_version": 1, "id": "example-malformed",`)
	themeFile(t, dataDir, "example-missing.json", theme("example-missing", copyModes(modes,
		func(m map[string]map[string]string) { delete(m["light"], "accent") })))
	themeFile(t, dataDir, "example-unknown.json", theme("example-unknown", copyModes(modes,
		func(m map[string]map[string]string) { m["dark"]["glow"] = "#000000" })))
	themeFile(t, dataDir, "example-value.json", theme("example-value", copyModes(modes,
		func(m map[string]map[string]string) { m["light"]["accent"] = "url(https://cdn.example/x.png)" })))
	themeFile(t, dataDir, "example-file.json", theme("example-other-id", modes))
	themeFile(t, dataDir, "industrial.json", theme("industrial", copyModes(modes,
		func(m map[string]map[string]string) { m["light"]["accent"] = "#123456" })))
	languages := filepath.Join(dataDir, "languages")
	writeJSON(t, filepath.Join(languages, "de.json"), `{"language": "de", "strings": {"language.name": 7}}`)
	writeJSON(t, filepath.Join(languages, "it.json"), map[string]any{"language": "es",
		"strings": map[string]string{"language.name": "Español"}})
	writeJSON(t, filepath.Join(languages, "nl.json"), map[string]any{"language": "nl",
		"strings": map[string]string{"settings.title": "Instellingen"}})
	writeJSON(t, filepath.Join(languages, "en.json"), map[string]any{"language": "en",
		"strings": map[string]string{"language.name": "English", "settings.title": "Overridden"}})

	index := build(t, dataDir)
	want := map[string]string{
		"example-malformed": front.RefusalThemeMalformed,
		"example-missing":   front.RefusalThemeTokenMissing,
		"example-unknown":   front.RefusalThemeTokenUnknown,
		"example-value":     front.RefusalThemeValueInvalid,
		"example-file":      front.RefusalThemeIDMismatch,
		"industrial":        front.RefusalNameCollision,
		"de":                front.RefusalLanguageMalformed,
		"it":                front.RefusalLanguageCodeMismatch,
		"nl":                front.RefusalLanguageNameMissing,
		"en":                front.RefusalNameCollision,
	}
	got := map[string]front.Refusal{}
	for _, refused := range index.Refused() {
		got[refused.ID] = refused
	}
	for id, code := range want {
		refused, found := got[id]
		switch {
		case !found:
			t.Errorf("%s was not refused", id)
		case refused.Code != code:
			t.Errorf("%s was refused %s, want %s (%s)", id, refused.Code, code, refused.Detail)
		case !strings.HasPrefix(refused.Path, dataDir):
			t.Errorf("%s's refusal names the path %q", id, refused.Path)
		}
	}
	if len(got) != len(want) {
		t.Errorf("refused %d parts, want %d: %v", len(got), len(want), index.Refused())
	}
	// The built-in wins: industrial and English are served unchanged.
	served, _ := index.Asset("themes/industrial.json")
	builtin, err := os.ReadFile(filepath.Join(repositoryRoot(t), "front", "themes", "industrial.json"))
	if err != nil || string(served.Body) != string(builtin) {
		t.Error("the built-in industrial theme is not served unchanged after a colliding part")
	}
	if english, _ := index.Catalogue("en"); english.Strings["settings.title"] == "Overridden" {
		t.Error("a colliding en.json changed the English catalogue")
	}
}
