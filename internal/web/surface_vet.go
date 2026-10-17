package web

import (
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"
)

// vetted is a surface that passed every check, with what the server needs of it.
type vetted struct {
	surface Surface
	// strings maps a language to the surface's keys in it.
	strings map[string]map[string]string
}

// vetSurfaces checks every candidate in name order and returns the accepted ones, the
// refusal of each other one, and the routes each accepted surface contributes. Nothing here
// panics: a malformed surface is a refusal with a reason, and the server starts without it.
//
// THE CHECKS, IN ORDER, each with its own reason: a name, and a name not taken; its catalogue
// files, which must be well formed and must not define a key another file defines; and its
// routes, which must carry a method, a path, an access level and a handler, must not repeat a
// row already in the table, and must be accepted by the router.
func vetSurfaces(host Host, core []Route, candidates []Surface) ([]vetted, []SurfaceRefusal, map[string][]Route) {
	ordered := append([]Surface(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })

	var accepted []vetted
	var refused []SurfaceRefusal
	contributed := map[string][]Route{}
	names := map[string]struct{}{}
	keysSeen := map[string]map[string]string{} // language -> key -> surface
	table := map[string]string{}               // "METHOD pattern" -> owner
	for _, route := range core {
		table[route.Method+" "+route.Pattern] = "the core"
	}

	for _, surface := range ordered {
		if strings.TrimSpace(surface.Name) == "" {
			refused = append(refused, SurfaceRefusal{Reason: "it declares no name"})
			continue
		}
		if _, duplicate := names[surface.Name]; duplicate {
			refused = append(refused, SurfaceRefusal{Surface: surface.Name,
				Reason: fmt.Sprintf("the name %q is already taken by another surface", surface.Name)})
			continue
		}
		candidate, routes, reason := vetOne(host, surface, keysSeen, table)
		if reason != "" {
			refused = append(refused, SurfaceRefusal{Surface: surface.Name, Reason: reason})
			continue
		}
		accepted = append(accepted, candidate)
		contributed[surface.Name] = routes
		names[surface.Name] = struct{}{}
		for language, entries := range candidate.strings {
			if keysSeen[language] == nil {
				keysSeen[language] = map[string]string{}
			}
			for key := range entries {
				keysSeen[language][key] = surface.Name
			}
		}
		for _, route := range routes {
			table[route.Method+" "+route.Pattern] = surface.Name
		}
	}
	return accepted, refused, contributed
}

// vetOne checks one surface against what the accepted ones already hold. It returns the
// reason it is refused, or an empty reason.
func vetOne(host Host, surface Surface, keysSeen map[string]map[string]string,
	table map[string]string) (vetted, []Route, string) {
	candidate := vetted{surface: surface}

	localised, err := surfaceStrings(surface.Files)
	if err != nil {
		return candidate, nil, err.Error()
	}
	for language, entries := range localised {
		core := coreCatalogueKeys(language)
		for key := range entries {
			if owner, duplicate := keysSeen[language][key]; duplicate {
				return candidate, nil, fmt.Sprintf("the %s catalogue key %q is already defined by %s",
					language, key, owner)
			}
			if _, defined := core[key]; defined {
				return candidate, nil, fmt.Sprintf("the %s catalogue key %q is already defined by the core",
					language, key)
			}
		}
	}
	candidate.strings = localised

	routes, reason := surfaceRoutes(host, surface, table)
	if reason != "" {
		return candidate, nil, reason
	}
	return candidate, routes, ""
}

// surfaceRoutes asks a surface for its rows and checks each one.
func surfaceRoutes(host Host, surface Surface, table map[string]string) (routes []Route, reason string) {
	if surface.Routes == nil {
		return nil, ""
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			routes, reason = nil, fmt.Sprintf("building its routes failed: %v", recovered)
		}
	}()
	routes = surface.Routes(host)
	seen := map[string]struct{}{}
	for _, route := range routes {
		row := route.Method + " " + route.Pattern
		switch {
		case route.Method == "" || route.Pattern == "" || route.Pattern[0] != '/':
			return nil, fmt.Sprintf("the route %q needs a method and a path starting with a slash", row)
		case route.Access != AccessPublic && route.Access != AccessAuthenticated && route.Access != AccessKeyFile:
			return nil, fmt.Sprintf("the route %q declares no access level", row)
		case route.Handler == nil:
			return nil, fmt.Sprintf("the route %q has no handler", row)
		}
		if owner, taken := table[row]; taken {
			return nil, fmt.Sprintf("the route %q is already served by %s", row, owner)
		}
		if _, repeated := seen[row]; repeated {
			return nil, fmt.Sprintf("the route %q is declared twice", row)
		}
		seen[row] = struct{}{}
	}
	// The router refuses some overlapping patterns with a panic; find out now, on a router
	// of its own, rather than when the table is built.
	probe := http.NewServeMux()
	for _, route := range routes {
		if failure := tryMount(probe, route); failure != "" {
			return nil, failure
		}
	}
	return routes, ""
}

// tryMount registers a route on a throwaway router and reports why the router refused it.
func tryMount(mux *http.ServeMux, route Route) (failure string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			failure = fmt.Sprintf("the router refuses the route %q: %v", route.Method+" "+route.Pattern, recovered)
		}
	}()
	mux.HandleFunc(route.Method+" "+route.Pattern, func(http.ResponseWriter, *http.Request) {})
	return ""
}

// surfaceStrings reads every language file of a surface: locale/<language>.json.
func surfaceStrings(files fs.FS) (map[string]map[string]string, error) {
	found := map[string]map[string]string{}
	if files == nil {
		return found, nil
	}
	entries, err := fs.ReadDir(files, "locale")
	if err != nil {
		if isMissing(err) {
			return found, nil
		}
		return nil, fmt.Errorf("its locale directory cannot be read: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		language := strings.TrimSuffix(name, ".json")
		raw, err := fs.ReadFile(files, "locale/"+name)
		if err != nil {
			return nil, fmt.Errorf("the catalogue file %s cannot be read: %w", name, err)
		}
		catalogue, err := decodeCatalogue(language, raw)
		if err != nil {
			return nil, fmt.Errorf("the catalogue file %s is malformed: %w", name, err)
		}
		found[language] = catalogue.Strings
	}
	return found, nil
}
