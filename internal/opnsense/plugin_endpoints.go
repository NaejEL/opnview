package opnsense

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// Endpoints a plugin declares, admitted at load.
//
// THE REGISTRY ABOVE IS COMPILED; A PLUGIN'S ENDPOINTS ARE NOT. A plugin is a directory dropped
// into <data-dir>/plugins/ and read at start (docs/plugins.md), so the paths it reads cannot be
// variables of this package. They are declared in its manifest, checked here once when the
// plugin loads, and from then on the client issues them exactly as it issues a registry entry,
// with the mutating-command refusal applied on every request as before. Every request reaches the
// client through the broker (internal/broker), which adds the read-only rule of the broker
// amendment on top: a POST only to a read action.
//
// What the check refuses, each with a closed code a plugin listing reports:
//
//   - a path that is not a plain absolute path on the firewall: a scheme, a host, a query, a
//     fragment, a backslash or a character outside the path set (EndpointNotRelative);
//   - a path outside /api/ (EndpointOutsideAPI);
//   - a path with a `..` or `.` element (EndpointParentReference);
//   - a path naming a mutating command, MutatingCommand's prefix rule included
//     (EndpointMutating);
//   - a method other than GET and POST: OPNsense reads with GET and its search and query
//     helpers with POST (survey, "GET versus POST, and the read-only guarantee"), and nothing
//     else is a read (EndpointMethodNotAllowed);
//   - a path the compiled registry already holds with other call fields, which would make one
//     path two endpoints (EndpointConflict).
//
// No allowlist is added here: the maintainer's decision of 10 October 2026 (specs/
// SPEC-backend-completion-plugins.md, Decisions) keeps the client's refusal of known mutating
// commands as it is; the read-action rule of 11 October lives in the broker. The host is never the plugin's to choose: the
// client builds every URL from the one configured base URL, so a declared endpoint is a path
// on that firewall and nothing more.

// The refusal codes of a declared endpoint.
const (
	EndpointNotRelative      = "endpoint_not_relative"
	EndpointOutsideAPI       = "endpoint_outside_api"
	EndpointParentReference  = "endpoint_parent_reference"
	EndpointMutating         = "endpoint_mutating"
	EndpointMethodNotAllowed = "endpoint_method_not_allowed"
	EndpointConflict         = "endpoint_conflict"
)

// EndpointRefusal is a declared endpoint the client will not issue, with its closed code.
type EndpointRefusal struct {
	Code   string
	Path   string
	Detail string
}

// Error implements error.
func (r *EndpointRefusal) Error() string {
	return fmt.Sprintf("opnsense: the endpoint %q is refused (%s): %s", r.Path, r.Code, r.Detail)
}

// pluginEndpoints are the endpoints admitted at load, keyed by the fields that decide what
// reaches the firewall.
var pluginEndpoints = struct {
	sync.RWMutex
	byKey map[endpointKey]Endpoint
}{byKey: map[endpointKey]Endpoint{}}

// endpointKey is what makes two declarations the same endpoint.
type endpointKey struct {
	path     string
	method   string
	encoding BodyEncoding
	argument ArgumentKind
	stream   bool
}

func keyOf(ep Endpoint) endpointKey {
	return endpointKey{path: ep.Path, method: ep.Method, encoding: ep.Encoding, argument: ep.Argument,
		stream: ep.Stream}
}

// CheckDeclaredEndpoint checks one endpoint a plugin declares, without admitting it. It
// returns nil when the client would issue it.
func CheckDeclaredEndpoint(ep Endpoint) *EndpointRefusal {
	refuse := func(code, format string, arguments ...any) *EndpointRefusal {
		return &EndpointRefusal{Code: code, Path: ep.Path, Detail: fmt.Sprintf(format, arguments...)}
	}
	path := ep.Path
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return refuse(EndpointNotRelative, "a declared path begins with a single slash and carries "+
			"no scheme and no host: the firewall's base URL is opnview's, never a plugin's")
	}
	for index := 0; index < len(path); index++ {
		character := path[index]
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case character == '/' || character == '_' || character == '-' || character == '.':
		default:
			return refuse(EndpointNotRelative, "the character %q is outside the set a path is "+
				"written in (letters, digits, '/', '_', '-', '.'): no scheme, query, fragment "+
				"or escape", string(character))
		}
	}
	elements := strings.Split(strings.Trim(path, "/"), "/")
	for _, element := range elements {
		if element == ".." || element == "." {
			return refuse(EndpointParentReference, "the path holds a %q element", element)
		}
		if element == "" {
			return refuse(EndpointNotRelative, "the path holds an empty element")
		}
	}
	if len(elements) < 4 || elements[0] != "api" {
		return refuse(EndpointOutsideAPI, "a firewall endpoint is /api/<module>/<controller>/<command>")
	}
	if command := MutatingCommand(path); command != "" {
		return refuse(EndpointMutating, "the command %q is one of the commands opnview never issues "+
			"(internal/opnsense, MutatingCommand)", command)
	}
	if ep.Method != http.MethodGet && ep.Method != http.MethodPost {
		return refuse(EndpointMethodNotAllowed, "the method %q is not GET or POST, the two methods "+
			"OPNsense's read actions answer", ep.Method)
	}
	if ep.Stream && ep.Method != http.MethodGet {
		return refuse(EndpointMethodNotAllowed, "a stream is read with GET")
	}
	for _, known := range Registry() {
		if known.Path == path && keyOf(known) != keyOf(ep) {
			return refuse(EndpointConflict, "the path is in opnview's own registry with other call "+
				"fields (%s, encoding %d, argument %d, stream %t)", known.Method, known.Encoding,
				known.Argument, known.Stream)
		}
	}
	return nil
}

// AdmitDeclaredEndpoint checks one endpoint a plugin declares and, when the client would
// issue it, admits it: Call and Stream accept it from then on. Admitting an endpoint twice,
// or one the compiled registry already holds, is harmless.
func AdmitDeclaredEndpoint(ep Endpoint) error {
	if refusal := CheckDeclaredEndpoint(ep); refusal != nil {
		return refusal
	}
	pluginEndpoints.Lock()
	defer pluginEndpoints.Unlock()
	if existing, found := pluginEndpoints.byKey[keyOf(ep)]; found && existing.SurveySection != "" &&
		ep.SurveySection == "" {
		// The first declaration's citation is kept: two plugins reading one path share it.
		return nil
	}
	pluginEndpoints.byKey[keyOf(ep)] = ep
	return nil
}

// Endpoints returns every endpoint the client will issue: the compiled registry and the
// endpoints plugins declared, ordered by path. A fake firewall answers these.
func Endpoints() []Endpoint {
	all := Registry()
	pluginEndpoints.RLock()
	for _, ep := range pluginEndpoints.byKey {
		all = append(all, ep)
	}
	pluginEndpoints.RUnlock()
	sort.SliceStable(all, func(i, j int) bool { return all[i].Path < all[j].Path })
	return all
}

// admitted reports whether a plugin declared ep and it was admitted.
func admitted(ep Endpoint) bool {
	pluginEndpoints.RLock()
	defer pluginEndpoints.RUnlock()
	_, found := pluginEndpoints.byKey[keyOf(ep)]
	return found
}
