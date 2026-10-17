package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/plugin/contract"
)

// The tests of A19 (BR24 of the broker amendment): one plugin, several OPNsense versions. The
// example plugin reads one lease endpoint that was renamed between two versions -- one field
// renamed, the answer restructured, which its adapter undoes -- and maps a later range of versions
// to a script file of its own.

const versionedManifest = `{"api_version": 2, "name": "example_versions", "version": "0.1.0",
	"display_name": "Example versions", "script": "example_versions.js",
	"script_versions": {"from": "26.1", "before": "27.1"},
	"scripts": [{"versions": {"from": "27.1"}, "script": "example_versions_next.js"}],
	"endpoints": [
		{"name": "leases", "path": "/api/examplelease/leases/search", "method": "GET",
			"versions": {"from": "26.1", "before": "26.7"},
			"variants": [{"versions": {"from": "26.7"}, "path": "/api/examplelease/leases4/search",
				"method": "GET", "fields": {"hwaddr": "hw_address", "hostname": "host_name"},
				"adapter": "unwrap"}]}],
	"providers": [{"kind": "dhcp_lease", "display_name": "Example leases", "read_strategy": "full_reread"}]}`

// versionedScript is the script of both ranges, which say which one ran in the probe's detail.
func versionedScript(which string) string {
	return `var d = opnview.decode;
exports.dhcp_lease = {
	probe: function (host) {
		return { result: { probe: "leases", state: "reachable", outcome: "healthy", separable: true,
			detail: "` + which + ` on " + host.firewall.version } };
	},
	leases: function (host, pageSize) {
		var response = host.call("leases", {});
		var result = { probe: "leases", state: "unavailable", outcome: "request_not_made" };
		if (!response.ok) {
			result.outcome = response.availability_outcome;
			return { result: result, error: "collect: the leases answered " + response.outcome };
		}
		var rows = d.rows(response.value);
		var leases = [];
		for (var i = 0; i < rows.length; i++) {
			var lease = opnview.dhcp_lease.decodeLease(rows[i]);
			if (lease !== null) {
				leases.push(lease);
			}
		}
		result.state = "reachable";
		result.outcome = leases.length > 0 ? "healthy" : "healthy_empty";
		result.separable = true;
		return { result: result, leases: leases };
	}
};
exports.adapters = { unwrap: function (value) { return value.data; } };
`
}

const versionedTests = `
test("each route serves the same leases from its own recorded answer", function (t) {
	t.firewallVersion("26.1.4");
	t.answer("leases", { fixture: "leases_26_1.json" });
	var before = t.call("dhcp_lease", "leases", 100);
	t.firewallVersion("26.7.3");
	t.answer("leases", { fixture: "leases_26_7.json" });
	var after = t.call("dhcp_lease", "leases", 100);
	if (before.error !== null || after.error !== null || before.leases.length !== 2) {
		t.fatal("the reads gave " + JSON.stringify(before) + " and " + JSON.stringify(after));
	}
	t.equal(after.leases, before.leases, "the two routes' leases");
	var paths = t.requests("leases").map(function (request) { return request.path; });
	t.equal(paths, ["/api/examplelease/leases/search", "/api/examplelease/leases4/search"],
		"the version change switched the route, with no restart");
});

test("a version outside every range is firewall_version_unsupported, and nothing is called", function (t) {
	t.firewallVersion("25.7.9");
	t.answer("leases", { fixture: "leases_26_1.json" });
	var read = t.call("dhcp_lease", "leases", 100);
	t.equal(read.failure, "firewall_version_unsupported", "the outcome");
	t.equal(t.requests("leases").length, 0, "the calls made");
});

test("a version not known yet is firewall_version_unsupported", function (t) {
	var read = t.call("dhcp_lease", "probe");
	t.equal(read.failure, "firewall_version_unsupported", "the outcome");
});

test("a range mapped to a script runs that script", function (t) {
	t.firewallVersion("26.1.4");
	var main = t.call("dhcp_lease", "probe");
	t.firewallVersion("27.1");
	var next = t.call("dhcp_lease", "probe");
	t.equal(main.result.detail, "the main script on 26.1.4", "the main script's probe");
	t.equal(next.result.detail, "the next script on 27.1", "the mapped script's probe");
});
`

const leases261 = `{"body": {"total": 2, "rowCount": 2, "current": 1, "rows": [
	{"address": "192.0.2.10", "hwaddr": "00:00:5e:00:53:01", "hostname": "example-one", "expire": 1790003600,
		"state": "0"},
	{"address": "192.0.2.11", "hwaddr": "00:00:5e:00:53:02", "hostname": "example-two", "expire": 1790007200,
		"state": "0"}]}}`

const leases267 = `{"body": {"data": {"total": 2, "rowCount": 2, "current": 1, "rows": [
	{"address": "192.0.2.10", "hw_address": "00:00:5e:00:53:01", "host_name": "example-one", "expire": 1790003600,
		"state": "0"},
	{"address": "192.0.2.11", "hw_address": "00:00:5e:00:53:02", "host_name": "example-two", "expire": 1790007200,
		"state": "0"}]}}}`

// writeVersionedPlugin writes the example plugin, with a manifest the caller may replace.
func writeVersionedPlugin(t *testing.T, manifest string) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "example_versions")
	for name, content := range map[string]string{
		"plugin.json":               manifest,
		"example_versions.js":       versionedScript("the main script"),
		"example_versions_next.js":  versionedScript("the next script"),
		"tests/versions.test.js":    versionedTests,
		"testdata/leases_26_1.json": leases261,
		"testdata/leases_26_7.json": leases267,
	} {
		path := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

// TestAVersionedPluginServesTheSameRecordsOnEveryRange is BR24: the two routes' recorded answers
// give the same records, a version change switches the route with no restart, a version outside
// every range -- or not known yet -- is the closed outcome, and a range mapped to a script runs it.
func TestAVersionedPluginServesTheSameRecordsOnEveryRange(t *testing.T) {
	t.Parallel()
	results, err := RunTests(context.Background(), writeVersionedPlugin(t, versionedManifest))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 {
		t.Fatalf("%d tests ran", len(results))
	}
	for _, result := range results {
		if !result.Passed {
			t.Errorf("%s: %s", result.Name, strings.Join(result.Failures, "; "))
		}
	}
}

// TestVersionRangesThatOverlapOrNameAPackageAreRefused is BR24's load half.
func TestVersionRangesThatOverlapOrNameAPackageAreRefused(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]struct {
		from, to, code string
	}{
		"overlapping routes": {`"versions": {"from": "26.1", "before": "26.7"}`,
			`"versions": {"from": "26.1", "before": "26.8"}`, plugin.RefusalVersionRangesOverlap},
		"overlapping scripts": {`"script_versions": {"from": "26.1", "before": "27.1"}`,
			`"script_versions": {"from": "26.1", "before": "27.2"}`, plugin.RefusalVersionRangesOverlap},
		"a range naming a package": {`"versions": {"from": "26.7"}`,
			`"versions": {"from": "1.0", "package": "os-example"}`, plugin.RefusalPackageVersionUnavailable},
		"a range with no bound": {`"versions": {"from": "26.7"}`, `"versions": {}`, plugin.RefusalVersionRangeInvalid},
		"bounds in the wrong order": {`"versions": {"from": "26.1", "before": "26.7"}`,
			`"versions": {"from": "26.7", "before": "26.1"}`, plugin.RefusalVersionRangeInvalid},
		"variants without a range of their own": {`"versions": {"from": "26.1", "before": "26.7"},`, ``,
			plugin.RefusalVersionRangeInvalid},
	} {
		manifest := strings.Replace(versionedManifest, change.from, change.to, 1)
		if manifest == versionedManifest {
			t.Fatalf("%s: the manifest was not changed", name)
		}
		_, err := RunTests(context.Background(), writeVersionedPlugin(t, manifest))
		var refusal *contract.Refusal
		if !errors.As(err, &refusal) || refusal.Code != change.code {
			t.Errorf("%s was answered %v, not %s", name, err, change.code)
		}
	}
}
