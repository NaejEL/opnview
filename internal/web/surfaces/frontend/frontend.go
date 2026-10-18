package frontend

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	opnview "github.com/NaejEL/opnview"
	"github.com/NaejEL/opnview/internal/front"
	"github.com/NaejEL/opnview/internal/web"
)

// The surface's marker.
//
//go:embed surface.json
var files embed.FS

func init() {
	web.RegisterSurface(web.Surface{
		Name:  "frontend",
		Files: files,
		Routes: func(host web.Host) []web.Route {
			index, err := front.Build(front.Options{Builtin: opnview.BuiltinFront(), DataDir: host.DataDir(),
				Widgets: host.Widgets(), CoreStrings: host.CatalogueStrings})
			if err != nil {
				// A built-in part breaking a rule is a defect of the build: the server refuses the
				// surface with this reason rather than serving a front that is not the product's.
				panic(err)
			}
			for _, refused := range index.Refused() {
				fmt.Fprintf(os.Stderr, "opnview: %v\n", refused)
			}
			return (&surface{Host: host, index: index}).routes()
		},
	})
}

// The paths.
const (
	// PathShell is every path no other route answers: the shell.
	PathShell = "/"
	// PathAssetsPattern is every asset of the index.
	PathAssetsPattern = front.AssetPrefix + "{path...}"
	// PathAPIUnknown is every path under /api/ no other route answers.
	PathAPIUnknown = "/api/"
	// PathAPIFront is the front's registry.
	PathAPIFront = "/api/front"
	// PathAPIStringsPattern is one language's catalogue.
	PathAPIStringsPattern = "/api/strings/{language}"
	// PathAPIAppearance is the appearance.
	PathAPIAppearance = "/api/appearance"
	// PathAPIWidgets is the widget registry.
	PathAPIWidgets = "/api/widgets"
)

// The conditions this surface answers.
const (
	// ConditionUnknownRoute is a path under /api/ that no route answers.
	ConditionUnknownRoute = "unknown_route"
	// ConditionUnknownAsset is a path under /front/ the index does not declare.
	ConditionUnknownAsset = "unknown_asset"
	// ConditionLanguageUnknown is a language that is not installed.
	ConditionLanguageUnknown = "language_unknown"
)

// surface is the surface's handlers over what the host grants and the index built at start.
type surface struct {
	web.Host
	index *front.Index
}

func (s *surface) routes() []web.Route {
	return []web.Route{
		// The shell carries no data, so it is public: it decides from GET /api/session which views
		// a reader may see.
		{Method: http.MethodGet, Pattern: PathShell, Access: web.AccessPublic, Handler: s.handleShell},
		{Method: http.MethodGet, Pattern: PathAssetsPattern, Access: web.AccessPublic, Handler: s.handleAsset},
		{Method: http.MethodGet, Pattern: PathAPIUnknown, Access: web.AccessPublic, JSON: true,
			Handler: s.handleUnknownAPI},
		// What the front is made of -- the themes, the languages, the core's asset URLs -- is what
		// the setup and sign-in views need before any session exists.
		{Method: http.MethodGet, Pattern: PathAPIFront, Access: web.AccessPublic, JSON: true, Handler: s.handleFront},
		{Method: http.MethodGet, Pattern: PathAPIStringsPattern, Access: web.AccessPublic, JSON: true,
			Handler: s.handleStrings},
		{Method: http.MethodGet, Pattern: PathAPIAppearance, Access: web.AccessPublic, JSON: true,
			Handler: s.handleAppearance},
		{Method: http.MethodPost, Pattern: PathAPIAppearance, Access: web.AccessAuthenticated, JSON: true,
			Handler: s.handleAppearanceWrite},
		{Method: http.MethodGet, Pattern: PathAPIWidgets, Access: web.AccessAuthenticated, JSON: true,
			Handler: s.handleWidgets},
	}
}

func (s *surface) handleShell(writer http.ResponseWriter, request *http.Request) {
	shell := s.index.Shell()
	front.Serve(writer, request, shell.ContentType, shell.ETag, shell.Body)
}

func (s *surface) handleAsset(writer http.ResponseWriter, request *http.Request) {
	asset, found := s.index.Asset(request.PathValue("path"))
	if !found {
		web.RefuseJSON(writer, http.StatusNotFound, ConditionUnknownAsset, "")
		return
	}
	front.Serve(writer, request, asset.ContentType, asset.ETag, asset.Body)
}

func (s *surface) handleUnknownAPI(writer http.ResponseWriter, _ *http.Request) {
	web.RefuseJSON(writer, http.StatusNotFound, ConditionUnknownRoute, "")
}

// ThemeEntry is one theme of the registry.
type ThemeEntry struct {
	ID string `json:"id"`
	// Name is the palette's name keyed by language, English always present.
	Name   map[string]string `json:"name"`
	URL    string            `json:"url"`
	Origin string            `json:"origin"`
}

// LanguageEntry is one installed language.
type LanguageEntry struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Origin string `json:"origin"`
}

// FrontAnswer is GET /api/front's answer.
type FrontAnswer struct {
	Themes    []ThemeEntry    `json:"themes"`
	Languages []LanguageEntry `json:"languages"`
	Assets    struct {
		Scripts     []string `json:"scripts"`
		Stylesheets []string `json:"stylesheets"`
	} `json:"assets"`
}

func (s *surface) themes() []ThemeEntry {
	entries := []ThemeEntry{}
	for _, theme := range s.index.Themes() {
		entries = append(entries, ThemeEntry{ID: theme.ID, Name: theme.Name, Origin: theme.Origin,
			URL: front.AssetPrefix + "themes/" + theme.ID + ".json"})
	}
	return entries
}

func (s *surface) languages() []LanguageEntry {
	entries := []LanguageEntry{}
	for _, language := range s.index.Languages() {
		entries = append(entries, LanguageEntry{Code: language.Code, Name: language.Name, Origin: language.Origin})
	}
	return entries
}

func (s *surface) handleFront(writer http.ResponseWriter, _ *http.Request) {
	answer := FrontAnswer{Themes: s.themes(), Languages: s.languages()}
	answer.Assets.Scripts = s.index.Scripts()
	answer.Assets.Stylesheets = s.index.Stylesheets()
	web.WriteJSON(writer, http.StatusOK, answer)
}

// StringsAnswer is one language's catalogue.
type StringsAnswer struct {
	Language string            `json:"language"`
	Strings  map[string]string `json:"strings"`
}

func (s *surface) handleStrings(writer http.ResponseWriter, request *http.Request) {
	catalogue, found := s.index.Catalogue(request.PathValue("language"))
	if !found {
		web.RefuseJSON(writer, http.StatusNotFound, ConditionLanguageUnknown, "")
		return
	}
	body, err := json.Marshal(StringsAnswer{Language: catalogue.Language, Strings: catalogue.Strings})
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	front.Serve(writer, request, "application/json; charset=utf-8", catalogue.ETag, body)
}

// The registry answers the parts loaded, with where each came from, and every part refused with
// its code and path.

// RegistryTheme is one loaded theme of the registry.
type RegistryTheme struct {
	ID     string `json:"id"`
	Origin string `json:"origin"`
	File   string `json:"file"`
}

// RegistryLanguage is one installed language of the registry, with the English keys it does not
// carry when it is a part of the data directory.
type RegistryLanguage struct {
	Code           string   `json:"code"`
	Origin         string   `json:"origin"`
	File           string   `json:"file"`
	MissingStrings []string `json:"missing_strings"`
}

// WidgetsAnswer is GET /api/widgets's answer.
type WidgetsAnswer struct {
	Widgets   []front.WidgetRow  `json:"widgets"`
	Shapes    []front.ShapeRow   `json:"shapes"`
	Themes    []RegistryTheme    `json:"themes"`
	Languages []RegistryLanguage `json:"languages"`
	Refused   []front.Refusal    `json:"refused"`
}

func (s *surface) handleWidgets(writer http.ResponseWriter, _ *http.Request) {
	answer := WidgetsAnswer{Widgets: s.index.Widgets(), Shapes: s.index.Shapes(), Refused: s.index.Refused(),
		Themes: []RegistryTheme{}, Languages: []RegistryLanguage{}}
	if answer.Widgets == nil {
		answer.Widgets = []front.WidgetRow{}
	}
	if answer.Shapes == nil {
		answer.Shapes = []front.ShapeRow{}
	}
	if answer.Refused == nil {
		answer.Refused = []front.Refusal{}
	}
	for _, theme := range s.index.Themes() {
		answer.Themes = append(answer.Themes, RegistryTheme{ID: theme.ID, Origin: theme.Origin, File: theme.File})
	}
	for _, language := range s.index.Languages() {
		row := RegistryLanguage{Code: language.Code, Origin: language.Origin, File: language.File,
			MissingStrings: []string{}}
		if catalogue, found := s.index.Catalogue(language.Code); found && catalogue.Missing != nil {
			row.MissingStrings = catalogue.Missing
		}
		answer.Languages = append(answer.Languages, row)
	}
	web.WriteJSON(writer, http.StatusOK, answer)
}
