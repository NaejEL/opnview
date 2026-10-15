package plugin

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/plugin/runtime"
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

// checkRanges checks a set of ranges of one endpoint, or of the scripts.
func checkRanges(plugin, what string, ranges []contract.VersionRange) *contract.Refusal {
	for _, held := range ranges {
		if held.Package != "" {
			return contract.Refuse(plugin, RefusalPackageVersionUnavailable, "%s: the range %s names the package %q, whose "+
				"version opnview cannot read without making the firewall refresh its package catalogue", what,
				held, held.Package)
		}
		if reason := held.Valid(); reason != "" {
			return contract.Refuse(plugin, RefusalVersionRangeInvalid, "%s: %s", what, reason)
		}
	}
	for first := 0; first < len(ranges); first++ {
		for second := first + 1; second < len(ranges); second++ {
			if ranges[first].Overlaps(ranges[second]) {
				return contract.Refuse(plugin, RefusalVersionRangesOverlap, "%s: the ranges %s and %s overlap", what,
					ranges[first], ranges[second])
			}
		}
	}
	return nil
}

// declaredRoutes checks an endpoint's versioned routes and returns them, the endpoint's own first.
// An endpoint with no range has one route, for every version.
func declaredRoutes(manifest contract.Manifest, declaration contract.EndpointDeclaration, own opnsense.Endpoint) ([]runtime.Route, *contract.Refusal) {
	if len(declaration.Variants) == 0 {
		if declaration.Versions != nil {
			if refusal := checkRanges(manifest.Name, "the endpoint "+declaration.Name,
				[]contract.VersionRange{*declaration.Versions}); refusal != nil {
				return nil, refusal
			}
			held := *declaration.Versions
			return []runtime.Route{{Versions: &held, Endpoint: own}}, nil
		}
		return []runtime.Route{{Endpoint: own}}, nil
	}
	if declaration.Versions == nil {
		return nil, contract.Refuse(manifest.Name, RefusalVersionRangeInvalid, "the endpoint %q declares variants and no "+
			"range of its own: every route of a versioned endpoint names its versions", declaration.Name)
	}
	ranges := []contract.VersionRange{*declaration.Versions}
	for _, variant := range declaration.Variants {
		ranges = append(ranges, variant.Versions)
	}
	if refusal := checkRanges(manifest.Name, "the endpoint "+declaration.Name, ranges); refusal != nil {
		return nil, refusal
	}
	ownRange := *declaration.Versions
	routes := []runtime.Route{{Versions: &ownRange, Endpoint: own}}
	for index, variant := range declaration.Variants {
		endpoint := opnsense.Endpoint{Path: variant.Path, Method: variant.Method, Stream: own.Stream,
			Argument: own.Argument, SurveySection: variant.SurveySection, UpstreamURL: variant.UpstreamURL,
			Note: "a variant of " + declaration.Name}
		if endpoint.Method == "" {
			endpoint.Method = http.MethodGet
		}
		encoding, ok := bodyEncoding(variant.Body)
		if !ok {
			return nil, contract.Refuse(manifest.Name, contract.RefusalEndpointInvalid, "the variant %d of %q has the body %q, not "+
				"none, form or json", index+1, declaration.Name, variant.Body)
		}
		endpoint.Encoding = encoding
		if refusal := opnsense.CheckDeclaredEndpoint(endpoint); refusal != nil {
			return nil, contract.Refuse(manifest.Name, refusal.Code, "the variant %d of %q: %s", index+1, declaration.Name,
				refusal.Detail)
		}
		if reason := broker.CheckRead(endpoint.Method, endpoint.Path); reason != "" {
			return nil, contract.Refuse(manifest.Name, contract.RefusalEndpointNotReadAction, "the variant %d of %q: %s", index+1,
				declaration.Name, reason)
		}
		for stable, firewall := range variant.Fields {
			if strings.TrimSpace(stable) == "" || strings.TrimSpace(firewall) == "" {
				return nil, contract.Refuse(manifest.Name, RefusalVersionRangeInvalid, "the variant %d of %q maps an empty "+
					"field name", index+1, declaration.Name)
			}
		}
		if variant.Adapter != "" && !contract.NamePattern.MatchString(variant.Adapter) {
			return nil, contract.Refuse(manifest.Name, RefusalVersionRangeInvalid, "the variant %d of %q names the adapter "+
				"%q, not a snake_case word", index+1, declaration.Name, variant.Adapter)
		}
		held := variant.Versions
		routes = append(routes, runtime.Route{Versions: &held, Endpoint: endpoint, Fields: variant.Fields,
			Adapter: variant.Adapter})
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

// loadScriptVariants compiles the scripts a manifest maps to ranges of versions, beside the main
// script, whose own range script_versions then names.
func loadScriptVariants(plugin *Plugin, directory fs.FS) *contract.Refusal {
	manifest := plugin.Manifest
	if len(manifest.Scripts) == 0 {
		if manifest.ScriptVersions != nil {
			return checkRanges(manifest.Name, "the script", []contract.VersionRange{*manifest.ScriptVersions})
		}
		return nil
	}
	if manifest.Script == "" || manifest.ScriptVersions == nil {
		return contract.Refuse(manifest.Name, RefusalVersionRangeInvalid, "scripts are mapped to versions, so the main "+
			"script needs script and script_versions too")
	}
	ranges := []contract.VersionRange{*manifest.ScriptVersions}
	for _, variant := range manifest.Scripts {
		ranges = append(ranges, variant.Versions)
	}
	if refusal := checkRanges(manifest.Name, "the scripts", ranges); refusal != nil {
		return refusal
	}
	for _, variant := range manifest.Scripts {
		if !fs.ValidPath(variant.Script) {
			return contract.Refuse(manifest.Name, contract.RefusalScriptMissing, "the script %q of the range %s is not a file of the "+
				"plugin's directory", variant.Script, variant.Versions)
		}
		source, err := fs.ReadFile(directory, variant.Script)
		if err != nil {
			return contract.Refuse(manifest.Name, contract.RefusalScriptMissing, "the script %q of the range %s: %v", variant.Script,
				variant.Versions, err)
		}
		program, err := goja.Compile(variant.Script, string(source), false)
		if err != nil {
			return contract.Refuse(manifest.Name, contract.RefusalScriptDoesNotCompile, "%s: %v", variant.Script, err)
		}
		if refusal := sameExports(plugin, variant.Script, program); refusal != nil {
			return refusal
		}
		plugin.script.Variants = append(plugin.script.Variants, runtime.ScriptVariant{Versions: variant.Versions,
			Program: program})
	}
	own := *manifest.ScriptVersions
	plugin.script.Versions = &own
	return nil
}

// sameExports checks that a script mapped to a range exports what the main script exports: the
// kinds' contracts were checked against the main script, and every range runs the same contracts.
func sameExports(plugin *Plugin, file string, program *goja.Program) *contract.Refusal {
	main, err := plugin.script.ExportsOf()
	if err != nil {
		return contract.Refuse(plugin.Name, contract.RefusalScriptDoesNotCompile, "%v", err)
	}
	variant := *plugin.script
	variant.Program = program
	exported, err := variant.ExportsOf()
	if err != nil {
		return contract.Refuse(plugin.Name, contract.RefusalScriptDoesNotCompile, "running %s to read its exports: %v", file, err)
	}
	for namespace, members := range main {
		for member, kind := range members {
			if kind == "function" && exported[namespace][member] != "function" {
				return contract.Refuse(plugin.Name, contract.RefusalScriptMissingFunction, "%s does not export %s.%s, which the main "+
					"script does", file, namespace, member)
			}
		}
	}
	return nil
}
