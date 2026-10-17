package vocab

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"sync"

	opnview "github.com/NaejEL/opnview"
)

// A MEASURE IS A DECLARATION (PA5 of the plugin-API amendment to specs/SPEC-backend-completion-plugins.md,
// 17 October 2026). A plugin's manifest declares the measures its derivations fill: a name, which becomes a
// value of the widget API's volume_measure, the states a direction of a connection can be in, which of them
// count as covered, the markers a direction can carry, the names of its coverage classes and of the states
// a window of it can be in. The core holds every measure's figures beside the logged ones, keyed by the
// measure's name, and serves them through one generic path: no core table, column, statement or enum names
// a measure, and adding one adds no column.
//
// "Measure" and "marker" are opnview's own terms (docs/data-model.md, *Vocabulary*): no product the
// project reads names a second way of counting the same connections.

// The bounds of a declaration. Each is a figure of the generic layout every reader decodes, so a
// measure's parts fit a fixed shape and two figures of one measure compare as plain values.
const (
	// MaxMeasureStates is the most direction states a measure declares.
	MaxMeasureStates = 32
	// MaxMeasureMarkers is the most markers a measure declares: a direction's markers are held as the
	// bits of one integer.
	MaxMeasureMarkers = 16
)

// DeclaredMeasureState is one state a direction of a connection can be in.
type DeclaredMeasureState struct {
	// Name is the state, a snake_case word, the key it is counted under in a coverage object.
	Name string `json:"name"`
	// Covered says a direction in it is covered by the measure: an answer counting a connection with a
	// direction in a state not covered is not given in the measure by best_available.
	Covered bool `json:"covered"`
}

// MeasureClasses are the names a coverage object counts connections under: every direction in the
// complete state; some measured bytes, not every direction complete; rows but no measured byte; no row
// at all -- the connection started where the measure knew nothing.
type MeasureClasses struct {
	Complete   string `json:"complete"`
	Partial    string `json:"partial"`
	Unmeasured string `json:"unmeasured"`
	NotKnown   string `json:"not_known"`
}

// MeasureWindowStates are the names of the states a window is answered with, by role. The core decides
// the three conditions; the measure names them.
type MeasureWindowStates struct {
	// Covered is the state of an interval the measure covers.
	Covered string `json:"covered"`
	// Unrecorded is the state of an instant no recorded interval holds.
	Unrecorded string `json:"unrecorded"`
	// NoProvider is the state of a window with no active provider of the measure's kind.
	NoProvider string `json:"no_provider"`
	// NotCovering is the state of a window mixing intervals in different states, or whose answer counts
	// a connection not covered.
	NotCovering string `json:"not_covering"`
	// NotServed is the state of an answer a widget type built from a figure the core did not read in
	// the measure.
	NotServed string `json:"not_served"`
	// Own are the other states the measure's derivation records per interval.
	Own []string `json:"own,omitempty"`
}

// DeclaredMeasure is one declared measure.
type DeclaredMeasure struct {
	// Name is the measure's name: a value of volume_measure and of a figure's `measure`.
	Name string `json:"name"`
	// Label is the catalogue key naming it, in the declaring plugin's locale.
	Label string `json:"label"`
	// Description says what it counts.
	Description string `json:"description"`
	// ProviderKind is the kind whose active provider the measure needs: none active, and a window is
	// answered with the no_provider state.
	ProviderKind string `json:"provider_kind"`
	// States are the direction states, in the order a coverage object lists them.
	States []DeclaredMeasureState `json:"states"`
	// CompleteState is the state of a direction counted complete, the complete class's.
	CompleteState string `json:"complete_state"`
	// RefusedState is the state the forward direction of a refused second leg is counted in: pf dropped
	// it before it could leave, so nothing could count it (NJ5's exit-row rule, decided question 3).
	RefusedState string `json:"refused_state"`
	// Markers are the named booleans a direction can carry, in the order a coverage object lists them.
	Markers []string `json:"markers,omitempty"`
	// Classes name the coverage classes.
	Classes MeasureClasses `json:"classes"`
	// WindowStates name the states of a window.
	WindowStates MeasureWindowStates `json:"window_states"`
	// BestAvailable says best_available may answer in the measure.
	BestAvailable bool `json:"best_available"`
	// Plugin is the declaring plugin, set by the loader.
	Plugin string `json:"-"`
}

// measureWord is a measure, state, marker or class name: a snake_case word.
var measureWord = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// reservedMeasures are the measures the core gives volume_measure and figures itself.
var reservedMeasures = map[string]bool{"logged": true, "best_available": true, "sampled": true,
	"interface_counter": true, "dns_lookups": true}

// Validate reports what is wrong with a declaration, "" when nothing is.
func (m DeclaredMeasure) Validate() string {
	switch {
	case !measureWord.MatchString(m.Name) || len(m.Name) > 48:
		return fmt.Sprintf("the measure name %q is not a snake_case word of at most 48 characters", m.Name)
	case reservedMeasures[m.Name]:
		return fmt.Sprintf("the measure name %q is the core's own", m.Name)
	case m.Label == "":
		return fmt.Sprintf("the measure %s has no label", m.Name)
	case m.Description == "":
		return fmt.Sprintf("the measure %s has no description", m.Name)
	case !measureWord.MatchString(m.ProviderKind):
		return fmt.Sprintf("the measure %s names no provider_kind", m.Name)
	case len(m.States) == 0 || len(m.States) > MaxMeasureStates:
		return fmt.Sprintf("the measure %s declares %d states, not 1 to %d", m.Name, len(m.States), MaxMeasureStates)
	case len(m.Markers) > MaxMeasureMarkers:
		return fmt.Sprintf("the measure %s declares %d markers, more than %d", m.Name, len(m.Markers), MaxMeasureMarkers)
	}
	states := map[string]bool{}
	for _, state := range m.States {
		if !measureWord.MatchString(state.Name) || states[state.Name] {
			return fmt.Sprintf("the measure %s's state %q is not a snake_case word declared once", m.Name, state.Name)
		}
		states[state.Name] = true
	}
	if !states[m.CompleteState] || !states[m.RefusedState] {
		return fmt.Sprintf("the measure %s's complete_state and refused_state must be among its states", m.Name)
	}
	markers := map[string]bool{}
	for _, marker := range m.Markers {
		if !measureWord.MatchString(marker) || markers[marker] {
			return fmt.Sprintf("the measure %s's marker %q is not a snake_case word declared once", m.Name, marker)
		}
		markers[marker] = true
	}
	classes := []string{m.Classes.Complete, m.Classes.Partial, m.Classes.Unmeasured, m.Classes.NotKnown}
	seen := map[string]bool{}
	for _, class := range classes {
		if !measureWord.MatchString(class) || seen[class] || class == "direction_states" || markers[class] {
			return fmt.Sprintf("the measure %s's coverage class %q is not a distinct snake_case word", m.Name, class)
		}
		seen[class] = true
	}
	window := m.WindowStates
	for _, state := range append([]string{window.Covered, window.Unrecorded, window.NoProvider, window.NotCovering,
		window.NotServed}, window.Own...) {
		if !measureWord.MatchString(state) {
			return fmt.Sprintf("the measure %s's window state %q is not a snake_case word", m.Name, state)
		}
	}
	return ""
}

// StateIndex is a direction state's position, -1 for one the measure does not declare.
func (m DeclaredMeasure) StateIndex(state string) int {
	for index, declared := range m.States {
		if declared.Name == state {
			return index
		}
	}
	return -1
}

// MarkerBits is the integer a direction's markers are held in: bit i for the i-th marker declared.
func (m DeclaredMeasure) MarkerBits(markers map[string]bool) int64 {
	var bits int64
	for index, marker := range m.Markers {
		if markers[marker] {
			bits |= 1 << index
		}
	}
	return bits
}

// PartWidth is how many figures one part of a measure's parts holds: its bytes, its packets, the four
// classes, one per state and one per marker.
func (m DeclaredMeasure) PartWidth() int { return 6 + len(m.States) + len(m.Markers) }

var measures = struct {
	mutex  sync.RWMutex
	byName map[string]DeclaredMeasure
}{byName: map[string]DeclaredMeasure{}}

// RegisterMeasure records a declared measure for the process: what decodes its figures and names its
// coverage. The same declaration twice is accepted -- the built-ins are read at start and again by the
// loader -- and another declaration under a taken name is refused.
func RegisterMeasure(measure DeclaredMeasure) error {
	if reason := measure.Validate(); reason != "" {
		return fmt.Errorf("vocab: %s", reason)
	}
	measures.mutex.Lock()
	defer measures.mutex.Unlock()
	if held, taken := measures.byName[measure.Name]; taken {
		if held.Plugin != "" && measure.Plugin != "" && held.Plugin != measure.Plugin {
			return fmt.Errorf("vocab: the measure %s is declared by the plugin %s", measure.Name, held.Plugin)
		}
		if measure.Plugin == "" {
			measure.Plugin = held.Plugin
		}
	}
	measures.byName[measure.Name] = measure
	return nil
}

// MeasureNamed is a registered measure.
func MeasureNamed(name string) (DeclaredMeasure, bool) {
	measures.mutex.RLock()
	defer measures.mutex.RUnlock()
	measure, found := measures.byName[name]
	return measure, found
}

// Measures are the registered measures, in name order: the order best_available tries them in
// (decided question 4 of the amendment).
func Measures() []DeclaredMeasure {
	measures.mutex.RLock()
	defer measures.mutex.RUnlock()
	all := make([]DeclaredMeasure, 0, len(measures.byName))
	for _, measure := range measures.byName {
		all = append(all, measure)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all
}

// MeasuresDeclaredIn reads the measures the plugins of a tree declare -- one directory each, holding its
// plugin.json -- each with its plugin, in the plugins' order. It validates nothing: the plugin loader
// does, and RegisterMeasure refuses an invalid one.
func MeasuresDeclaredIn(plugins fs.FS) ([]DeclaredMeasure, error) {
	entries, err := fs.ReadDir(plugins, ".")
	if err != nil {
		return nil, fmt.Errorf("vocab: reading the plugins: %w", err)
	}
	var declared []DeclaredMeasure
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := fs.ReadFile(plugins, path.Join(entry.Name(), "plugin.json"))
		if err != nil {
			return nil, fmt.Errorf("vocab: reading %s/plugin.json: %w", entry.Name(), err)
		}
		var manifest struct {
			Name     string            `json:"name"`
			Measures []DeclaredMeasure `json:"measures"`
		}
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return nil, fmt.Errorf("vocab: %s/plugin.json: %w", entry.Name(), err)
		}
		for _, measure := range manifest.Measures {
			measure.Plugin = manifest.Name
			declared = append(declared, measure)
		}
	}
	return declared, nil
}

// builtinMeasuresErr is what registering the built-ins' measures met at start.
var builtinMeasuresErr error

// BuiltinMeasuresError is what registering the built-ins' measures met at start: a built-in declaring an
// invalid measure is a defect of the build, which the store reports when it registers them.
func BuiltinMeasuresError() error { return builtinMeasuresErr }

// The built-ins' measures are known to the process from its start, before any plugin is loaded: a figure
// of a measure is decoded and named by its declaration, and a read in a process that loads no plugin --
// a store test, a tool -- reads them too. The loader registers the same declarations again, which is
// accepted.
func init() {
	declared, err := MeasuresDeclaredIn(opnview.BuiltinPlugins())
	if err != nil {
		builtinMeasuresErr = err
		return
	}
	for _, measure := range declared {
		if err := RegisterMeasure(measure); err != nil {
			builtinMeasuresErr = err
			return
		}
	}
}
