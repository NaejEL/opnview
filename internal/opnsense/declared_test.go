package opnsense

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The endpoints the built-in plugins declare, read from their manifests on disk -- this package cannot
// import the plugin loader -- and admitted as the loader admits them, so this package's tests call,
// and audit, what the binary calls: the compiled registry and every built-in plugin's endpoints.

// declaredByBuiltins reads every endpoint the manifests under plugins/ declare.
func declaredByBuiltins(t *testing.T) []Endpoint {
	t.Helper()
	manifests, err := filepath.Glob(filepath.Join("..", "..", "plugins", "*", "plugin.json"))
	if err != nil || len(manifests) == 0 {
		t.Fatalf("no built-in manifest was found: %v", err)
	}
	var endpoints []Endpoint
	for _, manifest := range manifests {
		raw, err := os.ReadFile(manifest)
		if err != nil {
			t.Fatal(err)
		}
		var declared struct {
			Endpoints []struct {
				Path     string `json:"path"`
				Method   string `json:"method"`
				Body     string `json:"body"`
				Argument string `json:"argument"`
				Stream   bool   `json:"stream"`
				Upstream string `json:"upstream_url"`
				Note     string `json:"note"`
			} `json:"endpoints"`
		}
		if err := json.Unmarshal(raw, &declared); err != nil {
			t.Fatalf("%s: %v", manifest, err)
		}
		for _, one := range declared.Endpoints {
			endpoint := Endpoint{Path: one.Path, Method: strings.ToUpper(one.Method), Stream: one.Stream,
				UpstreamURL: one.Upstream, Note: one.Note}
			if endpoint.Method == "" {
				endpoint.Method = http.MethodGet
			}
			switch one.Body {
			case "form":
				endpoint.Encoding = FormBody
			case "json":
				endpoint.Encoding = JSONBody
			}
			if one.Argument == "interface_names" {
				endpoint.Argument = InterfaceNameArgument
			}
			endpoints = append(endpoints, endpoint)
		}
	}
	return endpoints
}

// callable is every endpoint the binary calls: the compiled registry and the built-ins' declarations.
func callable(t *testing.T) []Endpoint {
	t.Helper()
	return append(Registry(), declaredByBuiltins(t)...)
}

// TestMain admits the endpoints the client tests call, as the plugin loader admits a built-in's
// declaration of them, before any test runs.
func TestMain(m *testing.M) {
	for _, endpoint := range []Endpoint{interfaceNamesEndpoint, statusEndpoint, jsonBodyEndpoint} {
		if err := AdmitDeclaredEndpoint(endpoint); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}
