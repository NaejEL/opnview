package collecttest

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"testing"

	"github.com/NaejEL/opnview/internal/opnsense"
)

// The arranged worlds: which fake firewall answers a coordinator's tests stand up, and what they
// expect it to activate. They are DATA, testdata/arrangements/<name>.json, because which product
// answers which endpoint is a fact about the built-in plugins and their fixtures, which no Go file
// of the core names (PA20 of specs/SPEC-backend-completion-plugins.md).
//
// An answer names its endpoint either by plugin and endpoint name, as the plugin's manifest declares
// it (Declared), or by the path of an endpoint of the compiled registry, which discovery and the
// clock read. Its fixture is a file name, found as every fixture is (providertest.FixturePath).

//go:embed testdata/arrangements/*.json
var arrangementFiles embed.FS

// Arrangement is one arranged world.
type Arrangement struct {
	// Description says what the world is, and why a test wants it.
	Description string `json:"description"`
	// Answers are the fake firewall's answers.
	Answers []ArrangedAnswer `json:"answers"`
	// Probe are the kinds probed once the answers are in place.
	Probe []string `json:"probe"`
	// Active are the providers each probed kind must have activated, in key order.
	Active map[string][]string `json:"active"`
	// Resolver is the provider key the world's arranged lookups name as their resolver.
	Resolver string `json:"resolver"`
	// Backend is the provider key the world's arranged leases name as their backend.
	Backend string `json:"backend"`
}

// ArrangedAnswer is one answer of the fake firewall.
type ArrangedAnswer struct {
	Plugin   string `json:"plugin"`
	Endpoint string `json:"endpoint"`
	Path     string `json:"path"`
	Fixture  string `json:"fixture"`
}

// ReadArrangement reads one arranged world, stopping the test on a file that is not one.
func ReadArrangement(t *testing.T, name string) Arrangement {
	t.Helper()
	raw, err := arrangementFiles.ReadFile(path.Join("testdata", "arrangements", name+".json"))
	if err != nil {
		t.Fatalf("collecttest: no arrangement %q: %v", name, err)
	}
	var arrangement Arrangement
	if err := json.Unmarshal(raw, &arrangement); err != nil {
		t.Fatalf("collecttest: the arrangement %q is not one: %v", name, err)
	}
	return arrangement
}

// EndpointOf is the endpoint an arranged answer names.
func (a ArrangedAnswer) EndpointOf() (opnsense.Endpoint, error) {
	if a.Plugin != "" {
		return Declared(a.Plugin, a.Endpoint), nil
	}
	for _, endpoint := range opnsense.Registry() {
		if endpoint.Path == a.Path {
			return endpoint, nil
		}
	}
	return opnsense.Endpoint{}, fmt.Errorf("collecttest: no registry endpoint has the path %q", a.Path)
}

// Arrange puts an arranged world's answers in place on a harness, probes its kinds and checks what
// each activated.
func (h *ProbeHarness) Arrange(t *testing.T, name string) Arrangement {
	t.Helper()
	arrangement := ReadArrangement(t, name)
	for _, answer := range arrangement.Answers {
		endpoint, err := answer.EndpointOf()
		if err != nil {
			t.Fatal(err)
		}
		h.Fake.AnswerFixture(endpoint, answer.Fixture)
	}
	for _, kind := range arrangement.Probe {
		if err := h.Collector.ProbeKind(context.Background(), kind); err != nil {
			t.Fatalf("arranging %s: probing the %s kind: %v", name, kind, err)
		}
		want := arrangement.Active[kind]
		got := h.ActiveKeysOf(t, kind)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("arranging %s: the %s kind activated %v, want %v", name, kind, got, want)
		}
	}
	return arrangement
}
