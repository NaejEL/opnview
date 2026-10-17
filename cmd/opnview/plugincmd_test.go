package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPluginTestRunsAPluginsOwnTestsWithoutGo is `opnview plugin test <dir>`: the built-in
// publicsuffix plugin's tests pass through the subcommand, and a third party's plugin -- written
// here as files only, with a failing test -- fails it, naming the test.
func TestPluginTestRunsAPluginsOwnTestsWithoutGo(t *testing.T) {
	var out bytes.Buffer
	if err := runPluginCommand(context.Background(), []string{"test", filepath.Join("..", "..", "plugins",
		"publicsuffix")}, &out); err != nil {
		t.Fatalf("the built-in plugin's tests failed through the subcommand: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), " 0 failed") {
		t.Errorf("the summary is %q", out.String())
	}

	directory := filepath.Join(t.TempDir(), "example_reader")
	for name, content := range map[string]string{
		"plugin.json": `{"api_version": 2, "name": "example_reader", "version": "0.1.0",
			"display_name": "Example reader", "description": "A lease source probed for its presence.",
			"script": "example_reader.js",
			"endpoints": [{"name": "status", "path": "/api/examplereader/service/status", "method": "GET"}],
			"providers": [{"kind": "dhcp_lease", "display_name": "Example reader", "read_strategy": "full_reread"}]}`,
		"example_reader.js": `exports.helpers = { double: function (n) { return n * 2; } };
			exports.dhcp_lease = {
				probe: function (host) {
					var response = host.call("status");
					return { result: { probe: "status", state: response.ok ? "reachable" : "unavailable",
						outcome: response.ok ? "healthy" : "plugin_not_installed" } };
				},
				leases: function () {
					return { result: { probe: "status", state: "unavailable", outcome: "read_not_supported" },
						unsupported: "an example reads nothing" };
				}
			};`,
		filepath.Join("tests", "example_reader.test.js"): `
			test("doubling doubles", function (t) { t.equal(t.helper("double", 2), 4, "double 2"); });
			test("a deliberately wrong expectation", function (t) { t.equal(t.helper("double", 2), 5, "double 2"); });`,
	} {
		path := filepath.Join(directory, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	out.Reset()
	err := runPluginCommand(context.Background(), []string{"test", directory}, &out)
	if !errors.Is(err, errPluginTestsFailed) {
		t.Fatalf("a failing test gave %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "ok   example_reader.test.js: doubling doubles") ||
		!strings.Contains(out.String(), "FAIL example_reader.test.js: a deliberately wrong expectation") {
		t.Errorf("the report does not name each test:\n%s", out.String())
	}

	if err := runPluginCommand(context.Background(), []string{"test"}, &out); err == nil {
		t.Error("a missing directory was accepted")
	}
}
