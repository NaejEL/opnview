package dashboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The format versions this build reads and writes (docs/dashboard-format.md, "Versioning and
// compatibility"): it writes CurrentFormatVersion and reads every version from
// MinimumFormatVersion to it. A newer file is refused rather than read optimistically, and an older
// one below the floor is refused rather than guessed at.
const (
	CurrentFormatVersion = 1
	MinimumFormatVersion = 1
)

// MaxDocumentBytes bounds a document: one above it is refused whole (ConditionDocumentTooLarge).
const MaxDocumentBytes = 1 << 20

// maxContainerDepth is the deepest a container may sit below a widget's `parameters` or a canvas's
// `scope`: a parameter value (1), an object of an ordered series list (2), a reference object it
// carries (3). docs/dashboard-format.md, "Parameters": nesting stops there.
const maxContainerDepth = 3

// The closed codes a document is refused with (specs/SPEC-step-7-frontend.md, A7).
const (
	ConditionMalformedBody             = "malformed_body"
	ConditionFormatVersionMissing      = "format_version_missing"
	ConditionFormatVersionTooNew       = "format_version_too_new"
	ConditionFormatVersionTooOld       = "format_version_too_old"
	ConditionCanvasesMissing           = "canvases_missing"
	ConditionCanvasesEmpty             = "canvases_empty"
	ConditionCanvasIDMissing           = "canvas_id_missing"
	ConditionCanvasIDMalformed         = "canvas_id_malformed"
	ConditionCanvasTitleMissing        = "canvas_title_missing"
	ConditionWidgetIDMissing           = "widget_id_missing"
	ConditionWidgetTypeAndShapeMissing = "widget_type_and_shape_missing"
	ConditionPlacementMissing          = "placement_missing"
	ConditionPlacementNotInteger       = "placement_not_integer"
	ConditionDuplicateID               = "duplicate_id"
	ConditionNestingTooDeep            = "nesting_too_deep"
	ConditionDocumentTooLarge          = "document_too_large"
)

// slugPattern is a canvas id: docs/dashboard-format.md, "Canvas".
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// placementKeys are the four members of a placement, in the order they are checked.
var placementKeys = []string{"x", "y", "w", "h"}

// Refusal is a document refused whole: its closed code, where the defect is, and for a format
// version refusal the versions involved. CanvasIndex and WidgetIndex are null where the defect is
// not inside a canvas or a widget; Field is the last member named in Path.
type Refusal struct {
	Condition   string   `json:"condition"`
	CanvasIndex *int     `json:"canvas_index"`
	WidgetIndex *int     `json:"widget_index"`
	Field       string   `json:"field"`
	Path        string   `json:"path"`
	FileVersion *float64 `json:"file_version,omitempty"`
	Current     *int     `json:"current,omitempty"`
	Minimum     *int     `json:"minimum,omitempty"`
}

// Error names the code and the path.
func (r *Refusal) Error() string {
	return "dashboard: refused (" + r.Condition + ") at " + r.Path
}

// at builds a refusal at a path inside a canvas and a widget; a negative index is "not inside".
func at(condition string, canvas, widget int, path string) *Refusal {
	refusal := &Refusal{Condition: condition, Path: path, Field: lastMember(path)}
	if canvas >= 0 {
		index := canvas
		refusal.CanvasIndex = &index
	}
	if widget >= 0 {
		index := widget
		refusal.WidgetIndex = &index
	}
	return refusal
}

// lastMember is the last member name of a path: "canvases[0].widgets[1].placement.x" is "x",
// "parameters.series[2]" is "series".
func lastMember(path string) string {
	if cut := strings.LastIndexByte(path, '.'); cut >= 0 {
		path = path[cut+1:]
	}
	if cut := strings.IndexByte(path, '['); cut >= 0 {
		path = path[:cut]
	}
	return path
}

// Validate checks one document's text and returns the first structural defect in document order,
// or nil for a document a save may store.
func Validate(raw []byte) *Refusal {
	if len(raw) > MaxDocumentBytes {
		return at(ConditionDocumentTooLarge, -1, -1, "")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var parsed any
	if err := decoder.Decode(&parsed); err != nil {
		return at(ConditionMalformedBody, -1, -1, "")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return at(ConditionMalformedBody, -1, -1, "")
	}
	document, ok := parsed.(map[string]any)
	if !ok {
		return at(ConditionMalformedBody, -1, -1, "")
	}
	if refusal := validateVersion(document); refusal != nil {
		return refusal
	}
	canvases, ok := document["canvases"].([]any)
	if !ok {
		return at(ConditionCanvasesMissing, -1, -1, "canvases")
	}
	if len(canvases) == 0 {
		return at(ConditionCanvasesEmpty, -1, -1, "canvases")
	}
	seen := map[string]bool{}
	for index, candidate := range canvases {
		if refusal := validateCanvas(candidate, index, seen); refusal != nil {
			return refusal
		}
	}
	return nil
}

// validateVersion checks format_version against the versions this build reads.
func validateVersion(document map[string]any) *Refusal {
	version, ok := wholeNumber(document["format_version"])
	if !ok {
		return at(ConditionFormatVersionMissing, -1, -1, "format_version")
	}
	condition := ""
	switch {
	case version > CurrentFormatVersion:
		condition = ConditionFormatVersionTooNew
	case version < MinimumFormatVersion:
		condition = ConditionFormatVersionTooOld
	default:
		return nil
	}
	refusal := at(condition, -1, -1, "format_version")
	current, minimum := CurrentFormatVersion, MinimumFormatVersion
	refusal.FileVersion, refusal.Current, refusal.Minimum = &version, &current, &minimum
	return refusal
}

// validateCanvas checks one canvas and its widgets.
func validateCanvas(candidate any, index int, seen map[string]bool) *Refusal {
	path := "canvases[" + strconv.Itoa(index) + "]"
	canvas, ok := candidate.(map[string]any)
	if !ok {
		return at(ConditionCanvasIDMissing, index, -1, path+".id")
	}
	id, ok := canvas["id"].(string)
	if !ok || id == "" {
		return at(ConditionCanvasIDMissing, index, -1, path+".id")
	}
	if !slugPattern.MatchString(id) {
		return at(ConditionCanvasIDMalformed, index, -1, path+".id")
	}
	if seen[id] {
		return at(ConditionDuplicateID, index, -1, path+".id")
	}
	seen[id] = true
	if title, ok := canvas["title"].(string); !ok || title == "" {
		return at(ConditionCanvasTitleMissing, index, -1, path+".title")
	}
	if scope, present := canvas["scope"]; present && scope != nil {
		if _, ok := scope.(map[string]any); !ok {
			return at(ConditionMalformedBody, index, -1, path+".scope")
		}
		if deep := tooDeep(scope, path+".scope", 0); deep != "" {
			return at(ConditionNestingTooDeep, index, -1, deep)
		}
	}
	widgets, present := canvas["widgets"]
	if !present || widgets == nil {
		// An absent widget list is an empty canvas, which the format allows.
		return nil
	}
	list, ok := widgets.([]any)
	if !ok {
		return at(ConditionMalformedBody, index, -1, path+".widgets")
	}
	ids := map[string]bool{}
	for widgetIndex, candidate := range list {
		if refusal := validateWidget(candidate, index, widgetIndex, path+".widgets["+strconv.Itoa(widgetIndex)+"]",
			ids); refusal != nil {
			return refusal
		}
	}
	return nil
}

// validateWidget checks one widget: its identity, what it draws, its placement and how deep its
// parameters nest.
func validateWidget(candidate any, canvas, index int, path string, ids map[string]bool) *Refusal {
	widget, ok := candidate.(map[string]any)
	if !ok {
		return at(ConditionWidgetIDMissing, canvas, index, path+".id")
	}
	id, ok := widget["id"].(string)
	if !ok || id == "" {
		return at(ConditionWidgetIDMissing, canvas, index, path+".id")
	}
	if ids[id] {
		return at(ConditionDuplicateID, canvas, index, path+".id")
	}
	ids[id] = true
	widgetType, typed := widget["type"].(string)
	shape, shaped := widget["shape"].(string)
	if (!typed || widgetType == "") && (!shaped || shape == "") {
		return at(ConditionWidgetTypeAndShapeMissing, canvas, index, path+".type")
	}
	placement, ok := widget["placement"].(map[string]any)
	if !ok {
		return at(ConditionPlacementMissing, canvas, index, path+".placement")
	}
	for _, key := range placementKeys {
		value, present := placement[key]
		if !present || value == nil {
			return at(ConditionPlacementMissing, canvas, index, path+".placement."+key)
		}
		if number, ok := wholeNumber(value); !ok || number < 0 {
			return at(ConditionPlacementNotInteger, canvas, index, path+".placement."+key)
		}
	}
	if parameters, present := widget["parameters"]; present && parameters != nil {
		if _, ok := parameters.(map[string]any); !ok {
			return at(ConditionMalformedBody, canvas, index, path+".parameters")
		}
		if deep := tooDeep(parameters, path+".parameters", 0); deep != "" {
			return at(ConditionNestingTooDeep, canvas, index, deep)
		}
	}
	return nil
}

// tooDeep returns the path of the first container nested deeper than maxContainerDepth below the
// root container at depth 0, visiting members in sorted order and elements in order, or "".
func tooDeep(value any, path string, depth int) string {
	switch typed := value.(type) {
	case map[string]any:
		if depth > maxContainerDepth {
			return path
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if deep := tooDeep(typed[key], path+"."+key, depth+1); deep != "" {
				return deep
			}
		}
	case []any:
		if depth > maxContainerDepth {
			return path
		}
		for index, element := range typed {
			if deep := tooDeep(element, path+"["+strconv.Itoa(index)+"]", depth+1); deep != "" {
				return deep
			}
		}
	}
	return ""
}

// wholeNumber reads a JSON number holding a whole value: 3 and 3.0 are whole, 3.5, "3", Infinity
// and a non-number are not. A float64 is what a JavaScript reader holds too, so both validators
// agree on every value.
func wholeNumber(value any) (float64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(number.String(), 64)
	if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) || parsed != math.Trunc(parsed) {
		return 0, false
	}
	return parsed, true
}
