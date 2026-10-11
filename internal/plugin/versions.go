package plugin

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/broker/infer"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/dop251/goja"
)

// One plugin, several OPNsense versions (A19 of the broker amendment to
// specs/SPEC-backend-completion-plugins.md, and the maintainer's decision on Q8).
//
// EVERYTHING HERE IS OPTIONAL. A plugin that declares no version range has no version handling at
// all: it runs as it is on every version. A plugin may map a range of OPNsense versions to an
// endpoint's route -- its path, method, body, corrections, and a field-name mapping that presents
// the answer under the names the script reads, with an adapter function where a rename is not
// enough -- and may map a range to a whole script file. The engine selects what matches the
// firewall's version, read at every discovery pass, so an upgrade switches the route with no
// restart; the script never tests a version. A version no declared range covers -- or one not
// known yet -- is the closed outcome firewall_version_unsupported: the closest range is never
// guessed.
//
// A range naming an OPNsense plugin's package is refused at load: the one answer that carries
// package versions makes the firewall refresh its package catalogue (docs/opnsense-api-survey.md,
// "Firewall versions, read from source for A19"), so no read-only source exists for it yet.

// The load refusals of version ranges.
const (
	// RefusalVersionRangeInvalid is a range with neither bound, a bound that is not a version,
	// bounds in the wrong order, or variants declared on an endpoint, or scripts on a manifest,
	// whose own range is missing.
	RefusalVersionRangeInvalid = "version_range_invalid"
	// RefusalVersionRangesOverlap is two ranges of one endpoint, or of the scripts, that share a
	// version: the engine would have to guess which applies.
	RefusalVersionRangesOverlap = "version_ranges_overlap"
	// RefusalPackageVersionUnavailable is a range naming an OPNsense plugin's package, whose version
	// opnview has no read-only way to learn.
	RefusalPackageVersionUnavailable = "package_version_unavailable"
)

// failureVersionUnsupported is the outcome of a call on a firewall whose version no range covers.
const failureVersionUnsupported = store.OutcomeFirewallVersionUnsupported

// VersionRange is a range of OPNsense versions: from From, included, to Before, excluded. Either
// bound may be absent, never both.
type VersionRange struct {
	From   string `json:"from,omitempty"`
	Before string `json:"before,omitempty"`
	// Package names an OPNsense plugin's package whose version the range is about, instead of the
	// core's. Refused at load for now: see package_version_unavailable.
	Package string `json:"package,omitempty"`
}

// String renders a range for a refusal or an outcome's detail.
func (r VersionRange) String() string {
	switch {
	case r.From != "" && r.Before != "":
		return fmt.Sprintf("[%s, %s)", r.From, r.Before)
	case r.From != "":
		return fmt.Sprintf("[%s, ...)", r.From)
	default:
		return fmt.Sprintf("[..., %s)", r.Before)
	}
}

// EndpointVariant is one route of an endpoint for a range of versions.
type EndpointVariant struct {
	Versions VersionRange `json:"versions"`
	// Path, Method and Body are the route on those versions, as an endpoint declares them.
	Path   string `json:"path"`
	Method string `json:"method,omitempty"`
	Body   string `json:"body,omitempty"`
	// SurveySection and UpstreamURL cite what establishes the route.
	SurveySection string `json:"survey_section,omitempty"`
	UpstreamURL   string `json:"upstream_url,omitempty"`
	// Fields maps a name the script reads to the name the firewall writes on those versions, in
	// every object of the answer.
	Fields map[string]string `json:"fields,omitempty"`
	// Adapter names a function the script exports under `adapters`: it is handed the answer's value,
	// already renamed, and returns the value the script reads, where a rename is not enough.
	Adapter string `json:"adapter,omitempty"`
	// Corrections correct what the broker infers of the route's answers.
	Corrections *infer.Corrections `json:"corrections,omitempty"`
}

// ScriptVariant maps a range of versions to a whole script file.
type ScriptVariant struct {
	Versions VersionRange `json:"versions"`
	Script   string       `json:"script"`
}

// versionParts reads a version as numbers: 26.7.3 is 26, 7, 3; a hotfix suffix, 26.7.3_1, is one
// more part. It answers false for text that is not a version.
func versionParts(version string) ([]int, bool) {
	version = strings.TrimSpace(version)
	if version == "" {
		return nil, false
	}
	var parts []int
	for _, part := range strings.FieldsFunc(version, func(r rune) bool { return r == '.' || r == '_' }) {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return nil, false
		}
		parts = append(parts, number)
	}
	return parts, len(parts) > 0
}

// compareVersions orders two versions; a missing part is zero, so 26.7 is 26.7.0.
func compareVersions(a, b []int) int {
	for index := 0; index < len(a) || index < len(b); index++ {
		left, right := 0, 0
		if index < len(a) {
			left = a[index]
		}
		if index < len(b) {
			right = b[index]
		}
		if left != right {
			if left < right {
				return -1
			}
			return 1
		}
	}
	return 0
}

// valid reports what is wrong with a range, empty when nothing is.
func (r VersionRange) valid() string {
	if r.From == "" && r.Before == "" {
		return "a range needs from, before, or both"
	}
	var from, before []int
	if r.From != "" {
		parts, ok := versionParts(r.From)
		if !ok {
			return fmt.Sprintf("%q is not a version", r.From)
		}
		from = parts
	}
	if r.Before != "" {
		parts, ok := versionParts(r.Before)
		if !ok {
			return fmt.Sprintf("%q is not a version", r.Before)
		}
		before = parts
	}
	if from != nil && before != nil && compareVersions(from, before) >= 0 {
		return fmt.Sprintf("from %s is not before %s", r.From, r.Before)
	}
	return ""
}

// contains reports whether a version lies in the range.
func (r VersionRange) contains(version string) bool {
	parts, ok := versionParts(version)
	if !ok {
		return false
	}
	if r.From != "" {
		from, _ := versionParts(r.From)
		if compareVersions(parts, from) < 0 {
			return false
		}
	}
	if r.Before != "" {
		before, _ := versionParts(r.Before)
		if compareVersions(parts, before) >= 0 {
			return false
		}
	}
	return true
}

// overlaps reports whether two valid ranges share a version.
func (r VersionRange) overlaps(other VersionRange) bool {
	// [a, b) and [c, d) overlap when a < d and c < b, an absent bound being infinite.
	below := func(from, before string) bool {
		if from == "" || before == "" {
			return true
		}
		low, _ := versionParts(from)
		high, _ := versionParts(before)
		return compareVersions(low, high) < 0
	}
	return below(r.From, other.Before) && below(other.From, r.Before)
}

// checkRanges checks a set of ranges of one endpoint, or of the scripts.
func checkRanges(plugin, what string, ranges []VersionRange) *Refusal {
	for _, held := range ranges {
		if held.Package != "" {
			return refuse(plugin, RefusalPackageVersionUnavailable, "%s: the range %s names the package %q, whose "+
				"version opnview cannot read without making the firewall refresh its package catalogue", what,
				held, held.Package)
		}
		if reason := held.valid(); reason != "" {
			return refuse(plugin, RefusalVersionRangeInvalid, "%s: %s", what, reason)
		}
	}
	for first := 0; first < len(ranges); first++ {
		for second := first + 1; second < len(ranges); second++ {
			if ranges[first].overlaps(ranges[second]) {
				return refuse(plugin, RefusalVersionRangesOverlap, "%s: the ranges %s and %s overlap", what,
					ranges[first], ranges[second])
			}
		}
	}
	return nil
}

// route is one way of calling an endpoint: for every version when versions is nil.
type route struct {
	versions *VersionRange
	endpoint opnsense.Endpoint
	// fields maps a name the script reads to the firewall's; adapter is an exported adapter's name.
	fields  map[string]string
	adapter string
}

// declaredRoutes checks an endpoint's versioned routes and returns them, the endpoint's own first.
// An endpoint with no range has one route, for every version.
func declaredRoutes(manifest Manifest, declaration EndpointDeclaration, own opnsense.Endpoint) ([]route, *Refusal) {
	if len(declaration.Variants) == 0 {
		if declaration.Versions != nil {
			if refusal := checkRanges(manifest.Name, "the endpoint "+declaration.Name,
				[]VersionRange{*declaration.Versions}); refusal != nil {
				return nil, refusal
			}
			held := *declaration.Versions
			return []route{{versions: &held, endpoint: own}}, nil
		}
		return []route{{endpoint: own}}, nil
	}
	if declaration.Versions == nil {
		return nil, refuse(manifest.Name, RefusalVersionRangeInvalid, "the endpoint %q declares variants and no "+
			"range of its own: every route of a versioned endpoint names its versions", declaration.Name)
	}
	ranges := []VersionRange{*declaration.Versions}
	for _, variant := range declaration.Variants {
		ranges = append(ranges, variant.Versions)
	}
	if refusal := checkRanges(manifest.Name, "the endpoint "+declaration.Name, ranges); refusal != nil {
		return nil, refusal
	}
	ownRange := *declaration.Versions
	routes := []route{{versions: &ownRange, endpoint: own}}
	for index, variant := range declaration.Variants {
		endpoint := opnsense.Endpoint{Path: variant.Path, Method: variant.Method, Stream: own.Stream,
			Argument: own.Argument, SurveySection: variant.SurveySection, UpstreamURL: variant.UpstreamURL,
			Note: "a variant of " + declaration.Name}
		if endpoint.Method == "" {
			endpoint.Method = http.MethodGet
		}
		encoding, ok := bodyEncoding(variant.Body)
		if !ok {
			return nil, refuse(manifest.Name, RefusalEndpointInvalid, "the variant %d of %q has the body %q, not "+
				"none, form or json", index+1, declaration.Name, variant.Body)
		}
		endpoint.Encoding = encoding
		if refusal := opnsense.CheckDeclaredEndpoint(endpoint); refusal != nil {
			return nil, refuse(manifest.Name, refusal.Code, "the variant %d of %q: %s", index+1, declaration.Name,
				refusal.Detail)
		}
		if reason := broker.CheckRead(endpoint.Method, endpoint.Path); reason != "" {
			return nil, refuse(manifest.Name, RefusalEndpointNotReadAction, "the variant %d of %q: %s", index+1,
				declaration.Name, reason)
		}
		for stable, firewall := range variant.Fields {
			if strings.TrimSpace(stable) == "" || strings.TrimSpace(firewall) == "" {
				return nil, refuse(manifest.Name, RefusalVersionRangeInvalid, "the variant %d of %q maps an empty "+
					"field name", index+1, declaration.Name)
			}
		}
		if variant.Adapter != "" && !namePattern.MatchString(variant.Adapter) {
			return nil, refuse(manifest.Name, RefusalVersionRangeInvalid, "the variant %d of %q names the adapter "+
				"%q, not a snake_case word", index+1, declaration.Name, variant.Adapter)
		}
		held := variant.Versions
		routes = append(routes, route{versions: &held, endpoint: endpoint, fields: variant.Fields,
			adapter: variant.Adapter})
	}
	return routes, nil
}

// bodyEncoding reads a declared body encoding.
func bodyEncoding(body string) (opnsense.BodyEncoding, bool) {
	switch body {
	case "", "none":
		return opnsense.NoBody, true
	case "form":
		return opnsense.FormBody, true
	case "json":
		return opnsense.JSONBody, true
	}
	return opnsense.NoBody, false
}

// versionUnsupported is the detail of a call on a version no range covers.
func versionUnsupported(version string, known bool, ranges []VersionRange) string {
	described := make([]string, 0, len(ranges))
	for _, held := range ranges {
		described = append(described, held.String())
	}
	sort.Strings(described)
	if !known {
		return "the firewall's OPNsense version is not known yet; the plugin declares the ranges " +
			strings.Join(described, ", ")
	}
	return fmt.Sprintf("the firewall runs OPNsense %s, which none of the plugin's ranges %s covers", version,
		strings.Join(described, ", "))
}

// select picks the route of an endpoint for a version: the one whose range covers it, or the one
// with no range. It answers the detail of firewall_version_unsupported when none applies.
func selectRoute(routes []route, version string, known bool) (route, string) {
	var ranges []VersionRange
	for _, candidate := range routes {
		if candidate.versions == nil {
			return candidate, ""
		}
		ranges = append(ranges, *candidate.versions)
		if known && candidate.versions.contains(version) {
			return candidate, ""
		}
	}
	return route{}, versionUnsupported(version, known, ranges)
}

// versionSignal ends a call whose endpoint has no route for the firewall's version. It is not an
// exception: a script's try cannot catch it.
type versionSignal struct{ detail string }

func (v *versionSignal) Error() string { return v.detail }

// scriptVariant is one compiled script file and the range it runs on.
type scriptVariant struct {
	versions VersionRange
	program  *goja.Program
}

// loadScriptVariants compiles the scripts a manifest maps to ranges of versions, beside the main
// script, whose own range script_versions then names.
func loadScriptVariants(plugin *Plugin, directory fs.FS) *Refusal {
	manifest := plugin.manifest
	if len(manifest.Scripts) == 0 {
		if manifest.ScriptVersions != nil {
			return checkRanges(manifest.Name, "the script", []VersionRange{*manifest.ScriptVersions})
		}
		return nil
	}
	if manifest.Script == "" || manifest.ScriptVersions == nil {
		return refuse(manifest.Name, RefusalVersionRangeInvalid, "scripts are mapped to versions, so the main "+
			"script needs script and script_versions too")
	}
	ranges := []VersionRange{*manifest.ScriptVersions}
	for _, variant := range manifest.Scripts {
		ranges = append(ranges, variant.Versions)
	}
	if refusal := checkRanges(manifest.Name, "the scripts", ranges); refusal != nil {
		return refusal
	}
	for _, variant := range manifest.Scripts {
		if !fs.ValidPath(variant.Script) {
			return refuse(manifest.Name, RefusalScriptMissing, "the script %q of the range %s is not a file of the "+
				"plugin's directory", variant.Script, variant.Versions)
		}
		source, err := fs.ReadFile(directory, variant.Script)
		if err != nil {
			return refuse(manifest.Name, RefusalScriptMissing, "the script %q of the range %s: %v", variant.Script,
				variant.Versions, err)
		}
		program, err := goja.Compile(variant.Script, string(source), false)
		if err != nil {
			return refuse(manifest.Name, RefusalScriptDoesNotCompile, "%s: %v", variant.Script, err)
		}
		if refusal := sameExports(plugin, variant.Script, program); refusal != nil {
			return refusal
		}
		plugin.script.variants = append(plugin.script.variants, scriptVariant{versions: variant.Versions,
			program: program})
	}
	own := *manifest.ScriptVersions
	plugin.script.versions = &own
	return nil
}

// forVersion is the script that runs on a version: this one when it maps no range or its range
// covers the version, else the variant whose range does. It answers the detail of
// firewall_version_unsupported when none applies.
func (s *script) forVersion(version string, known bool) (*goja.Program, string) {
	if s.versions == nil {
		return s.program, ""
	}
	ranges := []VersionRange{*s.versions}
	if known && s.versions.contains(version) {
		return s.program, ""
	}
	for _, variant := range s.variants {
		ranges = append(ranges, variant.versions)
		if known && variant.versions.contains(version) {
			return variant.program, ""
		}
	}
	return nil, versionUnsupported(version, known, ranges)
}

// sameExports checks that a script mapped to a range exports what the main script exports: the
// kinds' contracts were checked against the main script, and every range runs the same contracts.
func sameExports(plugin *Plugin, file string, program *goja.Program) *Refusal {
	main, err := plugin.script.exportsOf()
	if err != nil {
		return refuse(plugin.Name, RefusalScriptDoesNotCompile, "%v", err)
	}
	variant := *plugin.script
	variant.program = program
	exported, err := variant.exportsOf()
	if err != nil {
		return refuse(plugin.Name, RefusalScriptDoesNotCompile, "running %s to read its exports: %v", file, err)
	}
	for namespace, members := range main {
		for member, kind := range members {
			if kind == "function" && exported[namespace][member] != "function" {
				return refuse(plugin.Name, RefusalScriptMissingFunction, "%s does not export %s.%s, which the main "+
					"script does", file, namespace, member)
			}
		}
	}
	return nil
}

// routeOf is the route of an endpoint on the firewall's version, recording its field mapping and
// its adapter for the answers it gives. A version no range covers ends the call with
// firewall_version_unsupported, before anything is sent.
func (h *hostCalls) routeOf(s *script, name string, declared opnsense.Endpoint) route {
	routes := s.routes[name]
	if len(routes) == 0 {
		return route{endpoint: declared}
	}
	version, known := h.gate.FirewallVersion()
	chosen, unsupported := selectRoute(routes, version, known)
	if unsupported != "" {
		panic(&versionSignal{detail: fmt.Sprintf("the endpoint %s: %s", name, unsupported)})
	}
	h.mutex.Lock()
	if len(chosen.fields) > 0 {
		if h.renames == nil {
			h.renames = map[string]map[string]string{}
		}
		h.renames[chosen.endpoint.Path] = chosen.fields
	}
	if chosen.adapter != "" {
		if h.adapters == nil {
			h.adapters = map[string]string{}
		}
		h.adapters[name] = chosen.adapter
	} else {
		delete(h.adapters, name)
	}
	h.mutex.Unlock()
	return chosen
}

// renamed is the firewall's name of a field the script reads under a stable name, on the route
// an observed answer came from.
func (h *hostCalls) renamed(endpoint, key string) string {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	if firewall, mapped := h.renames[endpoint][key]; mapped {
		return firewall
	}
	return key
}

// stableName is the name the script reads a firewall field under.
func (h *hostCalls) stableName(endpoint, key string) string {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	for stable, firewall := range h.renames[endpoint] {
		if firewall == key {
			return stable
		}
	}
	return key
}

// adapt hands an answer's value to the route's adapter, which returns the value the script reads.
func (h *hostCalls) adapt(vm *goja.Runtime, name string, answer goja.Value) goja.Value {
	h.mutex.Lock()
	adapter := h.adapters[name]
	h.mutex.Unlock()
	object, isObject := answer.(*goja.Object)
	if adapter == "" || !isObject || h.exports == nil {
		return answer
	}
	adapters, _ := h.exports.Get("adapters").(*goja.Object)
	if adapters == nil {
		throw(vm, "the route of %s names the adapter %s, and the script exports no adapters", name, adapter)
	}
	function, isFunction := goja.AssertFunction(adapters.Get(adapter))
	if !isFunction {
		throw(vm, "the route of %s names the adapter %s, which the script does not export", name, adapter)
	}
	adapted, err := function(goja.Undefined(), object.Get("value"))
	if err != nil {
		var exception *goja.Exception
		if errors.As(err, &exception) {
			panic(exception.Value())
		}
		throw(vm, "the adapter %s of %s failed: %v", adapter, name, err)
	}
	_ = object.Set("value", adapted)
	return object
}

// firewall sets host.firewall: the firewall's OPNsense version, read-only, null until read.
func (h *hostCalls) firewall(vm *goja.Runtime, host *goja.Object) {
	described := vm.NewObject()
	if version, known := h.gate.FirewallVersion(); known {
		_ = described.DefineDataProperty("version", vm.ToValue(version), goja.FLAG_FALSE, goja.FLAG_FALSE,
			goja.FLAG_TRUE)
	} else {
		_ = described.DefineDataProperty("version", goja.Null(), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
	}
	_ = host.DefineDataProperty("firewall", described, goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE)
}

// renamePlain gives a value read whole the names the script reads: every object's firewall field
// names mapped back to the stable names of the route it came from.
func (h *hostCalls) renamePlain(endpoint string, value any) any {
	h.mutex.Lock()
	fields := h.renames[endpoint]
	h.mutex.Unlock()
	if len(fields) == 0 {
		return value
	}
	stable := make(map[string]string, len(fields))
	for name, firewall := range fields {
		stable[firewall] = name
	}
	var rename func(any) any
	rename = func(value any) any {
		switch typed := value.(type) {
		case map[string]any:
			renamed := make(map[string]any, len(typed))
			for key, member := range typed {
				if name, mapped := stable[key]; mapped {
					key = name
				}
				renamed[key] = rename(member)
			}
			return renamed
		case []any:
			for index, item := range typed {
				typed[index] = rename(item)
			}
			return typed
		}
		return value
	}
	return rename(value)
}
