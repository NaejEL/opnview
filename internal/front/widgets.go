package front

import (
	"sort"

	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// The widget registry: every loaded widget as the front needs it (GET /api/widgets).
//
// A WIDGET'S PARAMETERS ARE ITS MANIFEST'S, exactly, plus `volume_measure` where the core gives it
// (a widget declaring `volumes`): the picker, the form and the request builder of the front read
// them from here and from nowhere else, so the front never holds a list of widgets or of their
// keys.
//
// A WIDGET'S STRINGS ARE KEYS, NEVER WORDS: a plugin widget's live in its plugin's locale/ under
// plugin.<plugin>.widget.<type>.*, an operator or front-only widget's in its own directory's locale/
// under widget.<type>.* (docs/front-plugins.md).

// WidgetRow is one loaded widget.
type WidgetRow struct {
	Type    string `json:"type"`
	Shape   string `json:"shape"`
	Origin  string `json:"origin"`
	Plugin  string `json:"plugin"`
	Backend bool   `json:"backend"`
	// Windowed says the widget takes a period.
	Windowed   bool                       `json:"windowed"`
	Providers  []string                   `json:"providers"`
	Primary    string                     `json:"primary"`
	Parameters []params.ManifestParameter `json:"parameters"`
	References []string                   `json:"references"`
	// RendererURL is the widget's own renderer, or null when its shape's renderer draws it.
	RendererURL *string `json:"renderer_url"`
	TitleKey    string  `json:"title_key"`
	QuestionKey string  `json:"question_key"`
	// MissingStrings are the English keys of the widget's own namespace its part does not carry,
	// for a widget of the data directory: listed rather than refused.
	MissingStrings []string `json:"missing_strings"`
}

// ShapeRow is one shape renderer of the core.
type ShapeRow struct {
	Shape string `json:"shape"`
	URL   string `json:"url"`
}

// stringKeyPrefix is the namespace of a widget's strings.
func stringKeyPrefix(preset request.Preset) string {
	if preset.Plugin != "" {
		return "plugin." + preset.Plugin + ".widget." + preset.Type + "."
	}
	return "widget." + preset.Type + "."
}

// rowParameters are a widget's manifest parameters, and volume_measure where the core adds it.
func rowParameters(preset request.Preset) []params.ManifestParameter {
	declared := append([]params.ManifestParameter{}, preset.Manifest.Parameters...)
	if preset.Volumes {
		core := request.VolumeMeasureParam()
		declared = append(declared, params.ManifestParameter{Key: core.Key, Kind: string(core.Kind),
			Values: append([]string(nil), core.Values...), Default: core.Default, Class: params.ClassData})
	}
	return declared
}

// readWidgets indexes every loaded widget: its row, its renderer as an asset, and, for an operator
// or front-only widget, the strings of its directory. It answers those strings by language.
func (i *Index) readWidgets(options Options) map[string]map[string]string {
	merged := map[string]map[string]string{}
	if options.Widgets == nil {
		return merged
	}
	service := options.Widgets
	types := append(service.Types(), service.FrontOnlyTypes()...)
	sort.Strings(types)
	for _, widgetType := range types {
		preset, found := service.Known(widgetType)
		if !found {
			continue
		}
		prefix := stringKeyPrefix(preset)
		row := WidgetRow{Type: preset.Type, Shape: preset.Shape, Origin: preset.Origin, Plugin: preset.Plugin,
			Backend: !preset.FrontOnly, Windowed: preset.Windowed,
			Providers:  append([]string{}, preset.Manifest.Providers...),
			Primary:    preset.Manifest.Primary,
			Parameters: rowParameters(preset), References: append([]string{}, preset.Manifest.References...),
			TitleKey: prefix + "title", QuestionKey: prefix + "question", MissingStrings: []string{}}
		if preset.RendererEntry != "" {
			published := rendererPath(preset.Type, preset.RendererEntry)
			i.add(newAsset(published, preset.RendererSource, "text/javascript; charset=utf-8"))
			url := AssetPrefix + published
			row.RendererURL = &url
		}
		if preset.Plugin == "" && preset.Location != "" {
			found, refusals := widgetStrings(preset.Type, preset.Location)
			i.refused = append(i.refused, refusals...)
			for language, entries := range found {
				if merged[language] == nil {
					merged[language] = map[string]string{}
				}
				for key, text := range entries {
					merged[language][key] = text
				}
			}
		}
		i.widgets = append(i.widgets, row)
	}
	for _, refused := range service.Refusals() {
		i.refused = append(i.refused, Refusal{Part: PartWidget, ID: refused.Type, Code: refused.Code,
			Path: refused.Path, Detail: refused.Detail})
	}
	return merged
}

// noteMissingWidgetStrings lists, on the row of each widget of the data directory, the English
// keys of its title and question its part does not carry. A built-in that lacks one fails the
// test suite instead (front_test.go).
func (i *Index) noteMissingWidgetStrings() {
	english := i.catalogues[SourceLanguage]
	for index := range i.widgets {
		row := &i.widgets[index]
		if row.Origin == request.OriginBuiltin {
			continue
		}
		for _, key := range []string{row.TitleKey, row.QuestionKey} {
			if _, carried := english.Strings[key]; !carried {
				row.MissingStrings = append(row.MissingStrings, key)
			}
		}
	}
}

// Widgets returns every loaded widget's row, by type.
func (i *Index) Widgets() []WidgetRow {
	i.ensureCatalogues()
	return append([]WidgetRow(nil), i.widgets...)
}

// Shapes returns the core's shape renderers, by shape.
func (i *Index) Shapes() []ShapeRow { return append([]ShapeRow(nil), i.shapes...) }
