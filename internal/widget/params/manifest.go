package params

import (
	"bytes"
	"encoding/json"
	"errors"
)

// errTrailingManifestData is a manifest followed by more than one JSON value.
var errTrailingManifestData = errors.New("data after the manifest object")

// The widget manifest: the declarative half of a widget (docs/widget-authoring.md).
//
// A widget is a directory holding manifest.json, its query files and, for a built-in whose
// behaviour is Go code, that code. The manifest is what the loader validates and what the
// service builds a Preset from; nothing about a widget's type, shape, bindings or parameters is
// written in Go any more.

// ManifestVersion is the manifest format this build reads. A manifest declaring another value is
// refused with RefusalUnsupportedManifestVersion, so a format change is never half understood.
const ManifestVersion = 1

// The values of a manifest's `no_domains` field.
const (
	// NoDomainsNone says the widget carries no domain name, so the aggregate mode withholds
	// nothing from it as a whole. (A script may still withhold a field of its own.)
	NoDomainsNone = "none"
	// NoDomainsWhole says the widget is built on domain names, so under aggregate_mode =
	// no_domains the whole widget is withheld (condition withheld_by_aggregate_mode).
	NoDomainsWhole = "whole"
)

// The values of a manifest parameter's `class`.
const (
	// ClassData says the API applies the key: two values give two different responses.
	ClassData = "data"
	// ClassRendering says the key is accepted and echoed and never applied.
	ClassRendering = "rendering"
)

// Manifest is the content of one manifest.json.
type Manifest struct {
	// ManifestVersion is the format version, ManifestVersion.
	ManifestVersion int `json:"manifest_version"`
	// Type is the widget's identifier: snake_case, and the last element of its API path.
	Type string `json:"type"`
	// Shape is one of the twelve catalogue shapes that draws the widget.
	Shape string `json:"shape"`
	// Providers are the provider kinds the widget binds. Each response carries the
	// availability of every registered provider of each.
	Providers []string `json:"providers"`
	// Primary is the provider kind whose condition is the widget's condition; empty for a
	// widget that reads only opnview's own records.
	Primary string `json:"primary"`
	// Windowed says the widget takes a `period` and answers over a rolling window.
	Windowed bool `json:"windowed"`
	// NoDomains is the withholding rule of the aggregate mode no_domains: "none" or "whole".
	NoDomains string `json:"no_domains"`
	// LowerBound says the widget's byte and connection figures are lower bounds: opnview sees
	// only what crosses the firewall. Every figure the declarative engine emits then carries
	// `lower_bound: true` beside its measure.
	LowerBound bool `json:"lower_bound"`
	// Measure names what a declarative widget's figures are (the `measure` of each figure it
	// emits). Optional; "query" when absent.
	Measure string `json:"measure,omitempty"`
	// Volumes says the widget serves Volume or byte Count figures. The core then gives it the
	// parameter volume_measure -- logged, netflow or best_available, best_available by default --
	// which it does not declare, and answers it under the measure that parameter chooses
	// (internal/widget/request/measure.go): the widget reads its figures through the core and
	// builds them through host.figures, and never names a measure. Only a widget with a script,
	// over a window, declares it.
	Volumes bool `json:"volumes,omitempty"`
	// Parameters are the keys the widget accepts.
	Parameters []ManifestParameter `json:"parameters"`
	// References are the reference kinds the widget accepts, validated against the reference
	// parameters it declares.
	References []string `json:"references"`
	// Queries are the statement files of the widget's directory, relative to it.
	Queries []string `json:"queries"`
	// Script is the JavaScript file, in the widget's own directory, that answers the widget: the
	// logic a shape cannot express, run by the embedded interpreter
	// (internal/widget/script/script.go). A widget with a script runs the statements its `queries`
	// files declare, by name, through its host, and is not served by the declarative engine.
	Script string `json:"script,omitempty"`
	// Renderer is a slot RESERVED for the interface work of step 7: declared, validated for
	// its shape, and not built. A widget with no renderer yet carries {"reserved": true}.
	Renderer ManifestRenderer `json:"renderer"`
}

// ManifestRenderer is the reserved renderer slot.
type ManifestRenderer struct {
	// Reserved is true while no renderer is built. It is the only value step 6 accepts.
	Reserved bool `json:"reserved"`
}

// ManifestParameter declares one key of a widget.
type ManifestParameter struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
	// Values is the vocabulary of an enum or enum_set.
	Values []string `json:"values,omitempty"`
	// Default is the value an absent or invalid key takes: a string, a boolean, a number, an
	// array, or null for "absent".
	Default any `json:"default"`
	// Min and Max bound an integer or number.
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
	// Reference is the reference kind of a reference or reference_list.
	Reference string `json:"reference,omitempty"`
	// Class is "data" or "rendering".
	Class string `json:"class"`
	// Required says the widget has nothing to answer without the key.
	Required bool `json:"required,omitempty"`
}

// param converts a validated manifest parameter to the engine's Param.
func (p ManifestParameter) Param() Param {
	result := Param{
		Key: p.Key, Kind: ParamKind(p.Kind), Values: p.Values, Default: p.Default,
		Reference: p.Reference, Data: p.Class == ClassData, Required: p.Required,
	}
	if p.Min != nil {
		result.Min = *p.Min
	}
	if p.Max != nil {
		result.Max = *p.Max
		result.HasMax = true
	}
	return result
}

// decodeManifest reads one manifest.json strictly: an unknown field is a refusal, because a
// misspelt key would otherwise be silently ignored.
func DecodeManifest(raw []byte) (Manifest, error) {
	// What a manifest may leave out takes its stated default: no domain names, and the renderer
	// slot reserved.
	manifest := Manifest{NoDomains: NoDomainsNone, Renderer: ManifestRenderer{Reserved: true}}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if decoder.More() {
		return Manifest{}, errTrailingManifestData
	}
	for index := range manifest.Parameters {
		if manifest.Parameters[index].Class == "" {
			manifest.Parameters[index].Class = ClassData
		}
	}
	return manifest, nil
}

// UnmarshalJSON reads a renderer slot that is PRESENT as exactly what it says: a slot that
// leaves out "reserved" is not reserved, so it is refused rather than silently taking the
// default an absent slot takes. Unknown fields are refused here too.
func (r *ManifestRenderer) UnmarshalJSON(raw []byte) error {
	type plain ManifestRenderer
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*r = ManifestRenderer(decoded)
	return nil
}
