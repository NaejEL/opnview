package widgets

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaejEL/opnview/internal/plugin"
)

// peerPlacingTypes are the served types that place the window's outside peers by their
// geolocation, one host.services.geolocation call a peer.
var peerPlacingTypes = []string{"destination_countries", "destination_operators", "passed_traffic_world_map",
	"traffic_sankey", "blocked_traffic_world_map"}

// TestAWidgetPlacingPeersReadsTheirGeolocationsInStatementsThatDoNotGrowWithThePeers is E12 of
// Phase E: each type that places outside peers answers the same request over two fixtures that
// differ only in how many distinct outside peers the flows reach -- four times as many in the
// second -- in the same number of statements. The geolocations of every peer the request placed are
// read together, at the first one a script asks for (internal/widget/request/installation.go,
// Geolocation), and no longer one statement a peer.
func TestAWidgetPlacingPeersReadsTheirGeolocationsInStatementsThatDoNotGrowWithThePeers(t *testing.T) {
	t.Parallel()
	few := newFixture(t, fixtureSpec{interfaces: 3, clients: 8, flows: 600, span: 3 * 86400, peers: 12})
	many := newFixture(t, fixtureSpec{interfaces: 3, clients: 8, flows: 600, span: 3 * 86400, peers: 48})
	if distinctPeers(t, many) < 2*distinctPeers(t, few) {
		t.Fatalf("the fixtures reach %d and %d distinct peers, so the comparison is vacuous",
			distinctPeers(t, few), distinctPeers(t, many))
	}
	for _, widgetType := range peerPlacingTypes {
		for _, period := range []string{"24h", "30d"} {
			parameters := map[string]any{"period": period}
			fewStatements := statementsOf(t, few, widgetType, parameters)
			manyStatements := statementsOf(t, many, widgetType, parameters)
			if fewStatements != manyStatements {
				t.Errorf("%s %s: %d statements over %d peers, %d over %d", widgetType, period, fewStatements,
					distinctPeers(t, few), manyStatements, distinctPeers(t, many))
			}
		}
	}
}

// statementsOf answers one request and counts the statements it ran on the read connections.
func statementsOf(t *testing.T, f *fixture, widgetType string, parameters any) int64 {
	t.Helper()
	before, _ := f.db.Reader.Executed()
	f.respond(t, widgetType, parameters)
	after, _ := f.db.Reader.Executed()
	return after - before
}

// distinctPeers counts the outside peers the fixture's flows reach.
func distinctPeers(t *testing.T, f *fixture) int64 {
	t.Helper()
	var count int64
	if err := f.db.DB().QueryRowContext(context.Background(),
		"SELECT count(DISTINCT peer_address) FROM volume_aggregate_30d WHERE peer_address IS NOT NULL").
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestAFixturePluginPlacingPeersIsServedByTheGeolocationBatch is E29's geolocation half: the fixture
// plugin of testdata/plugins, copied into a temporary plugin tree as a third party adds one, holds a
// widget whose own statement returns a peer_address column and asks for the geolocation of each.
// Nothing of the core names it, and its answers over two fixtures that differ only in how many
// distinct outside peers the flows reach take the same number of statements: the core placed its
// peers and read their geolocations together.
func TestAFixturePluginPlacingPeersIsServedByTheGeolocationBatch(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	copyFixturePlugin(t, dataDir, "example_peers")
	few := newFixture(t, fixtureSpec{interfaces: 3, clients: 8, flows: 600, span: 3 * 86400, peers: 12})
	many := newFixture(t, fixtureSpec{interfaces: 3, clients: 8, flows: 600, span: 3 * 86400, peers: 48})
	few.service = pluginService(t, few, dataDir)
	many.service = pluginService(t, many, dataDir)
	for _, period := range []string{"24h", "30d"} {
		parameters := map[string]any{"period": period}
		fewStatements := statementsOf(t, few, "example_peer_places", parameters)
		manyStatements := statementsOf(t, many, "example_peer_places", parameters)
		if fewStatements != manyStatements {
			t.Errorf("example_peer_places %s: %d statements over %d peers, %d over %d", period, fewStatements,
				distinctPeers(t, few), manyStatements, distinctPeers(t, many))
		}
		// The comparison is not vacuous: the widget placed destinations by their geolocation.
		answer := data(t, many.respond(t, "example_peer_places", parameters))
		if placed, _ := answer["placed"].(float64); placed == 0 {
			t.Errorf("example_peer_places %s placed no destination: %v", period, answer)
		}
	}
}

// copyFixturePlugin copies a fixture plugin of the repository's testdata/plugins into a data
// directory's plugin tree, file by file, as a third party adds one: no file the product ships is
// touched.
func copyFixturePlugin(t *testing.T, dataDir, name string) {
	t.Helper()
	source := filepath.Join("..", "..", "..", "testdata", "plugins", name)
	target := filepath.Join(dataDir, plugin.PluginsDirectory, name)
	check(t, filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(target, relative), 0o750)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(target, relative), content, 0o640)
	}))
}
