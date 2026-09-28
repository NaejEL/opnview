package opnsense

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// surveyPath is the document every endpoint has to be justified by. The tests run
// with the package directory as the working directory, so the repository root is
// two levels up.
const surveyPath = "../../docs/opnsense-api-survey.md"

// TestEveryRegistryEndpointPathAppearsVerbatimInTheSurvey is the guard that makes
// an invented endpoint unable to compile and pass.
//
// An endpoint path is a claim about a firewall, and the only admissible evidence
// for it in this project is docs/opnsense-api-survey.md. The check is a verbatim
// substring, not a fuzzy one: a path that is nearly right — the netflow/top that
// answered 404, say — must fail rather than nearly pass.
func TestEveryRegistryEndpointPathAppearsVerbatimInTheSurvey(t *testing.T) {
	survey := readSurvey(t)
	for _, endpoint := range Registry() {
		if !strings.Contains(survey, endpoint.Path) {
			t.Errorf("the registry names %s and the survey does not contain that path verbatim",
				endpoint.Path)
		}
	}
}

// TestEveryRegistryEndpointCitesItsSurveySectionAndUpstreamURL keeps the citation
// from rotting into a path with no justification beside it.
func TestEveryRegistryEndpointCitesItsSurveySectionAndUpstreamURL(t *testing.T) {
	survey := readSurvey(t)
	for _, endpoint := range Registry() {
		if endpoint.SurveySection == "" {
			t.Errorf("%s cites no survey section", endpoint.Path)
			continue
		}
		// The section title itself has to exist in the document, or the citation
		// points at a heading somebody renamed.
		if !strings.Contains(survey, endpoint.SurveySection) {
			t.Errorf("%s cites the survey section %q, which the document does not contain",
				endpoint.Path, endpoint.SurveySection)
		}
		if endpoint.UpstreamURL == "" {
			t.Errorf("%s cites no upstream URL", endpoint.Path)
			continue
		}
		if !strings.Contains(survey, endpoint.UpstreamURL) {
			t.Errorf("%s cites the upstream URL %s, which the survey does not carry",
				endpoint.Path, endpoint.UpstreamURL)
		}
		if endpoint.Note == "" {
			t.Errorf("%s carries no note saying what a caller has to know about it", endpoint.Path)
		}
	}
}

// TestNoRegistryEndpointIsAMutatingCommand is the read-only rule applied to the
// registry itself, so a mutating path cannot be added and then refused only at
// call time.
func TestNoRegistryEndpointIsAMutatingCommand(t *testing.T) {
	for _, endpoint := range Registry() {
		if command := MutatingCommand(endpoint.Path); command != "" {
			t.Errorf("the registry names %s, whose command %q is mutating", endpoint.Path, command)
		}
	}
}

// TestMutatingCommandRecognisesEveryForbiddenCommand checks the guard has teeth
// against each name the survey lists, and does not fire on the read-only commands
// the registry actually uses.
func TestMutatingCommandRecognisesEveryForbiddenCommand(t *testing.T) {
	for _, command := range mutatingCommands {
		path := "/api/example/controller/" + command
		if MutatingCommand(path) != command {
			t.Errorf("%s was not recognised as mutating", path)
		}
		// A mutating command with positional parameters after it is still mutating.
		if MutatingCommand(path+"/something") != command {
			t.Errorf("%s with a parameter was not recognised as mutating", path)
		}
	}
	for _, path := range []string{
		"/api/example/controller/get",
		"/api/example/controller/search",
		"/api/example/controller/status",
		"/api/example/controller/is_enabled",
		// A positional parameter that happens to spell a forbidden command is caller
		// data, not a command, and must not be refused.
		"/api/example/controller/top/stop",
	} {
		if command := MutatingCommand(path); command != "" {
			t.Errorf("%s was wrongly called mutating (%q)", path, command)
		}
	}
}

// TestOnlyTrafficTopTakesInterfaceNames pins the one endpoint whose argument kind
// matters, so the guard in internal/collect cannot be pointed at the wrong path.
func TestOnlyTrafficTopTakesInterfaceNames(t *testing.T) {
	for _, endpoint := range Registry() {
		wantsNames := endpoint.Argument == InterfaceNameArgument
		isTrafficTop := endpoint.Path == TrafficTop.Path
		if wantsNames != isTrafficTop {
			t.Errorf("%s declares argument kind %v, which disagrees with it being traffic/top (%v)",
				endpoint.Path, endpoint.Argument, isTrafficTop)
		}
	}
}

// TestOnlySearchQueriesUsesAJSONBody pins the one endpoint whose call form is
// load-bearing. Every other POST is a grid search, for which a form body is
// correct; this one silently degrades to the unwindowed branch unless the bounds
// arrive as JSON integers.
func TestOnlySearchQueriesUsesAJSONBody(t *testing.T) {
	for _, endpoint := range Registry() {
		usesJSON := endpoint.Encoding == JSONBody
		isSearchQueries := endpoint.Path == SearchQueries.Path
		if usesJSON != isSearchQueries {
			t.Errorf("%s declares encoding %v, which disagrees with it being search_queries (%v)",
				endpoint.Path, endpoint.Encoding, isSearchQueries)
		}
	}
}

// TestRegistryHasNoDuplicatePath keeps two entries from disagreeing about one path.
func TestRegistryHasNoDuplicatePath(t *testing.T) {
	seen := map[string]bool{}
	for _, endpoint := range Registry() {
		if seen[endpoint.Path] {
			t.Errorf("%s appears twice in the registry", endpoint.Path)
		}
		seen[endpoint.Path] = true
	}
}

// readSurvey returns the survey document.
func readSurvey(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(surveyPath))
	if err != nil {
		t.Fatalf("reading %s: %v", surveyPath, err)
	}
	return string(raw)
}
