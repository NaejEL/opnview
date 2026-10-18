package collecttest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"sync"
	"testing"

	opnview "github.com/NaejEL/opnview"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// A test store with the built-in plugins already loaded into it: their tables created, their
// registry rows, measures and values written. It is cloned from a derived template
// (store.NewTestDatabasePrepared) built once, so a test world that loads the built-ins over its
// store pays a load that finds everything in place, rather than creating every table again in
// every database (carried item 1 of Lot 4 of the plugin-API amendment). The world still calls
// plugin.Load itself, exactly as the service does at start, so what it tests is unchanged.

var (
	pluginRevisionOnce sync.Once
	pluginRevision     string
	pluginRevisionErr  error
)

// builtinPluginsRevision is a digest of every file of the built-in plugins: what the preparation reads.
func builtinPluginsRevision() (string, error) {
	pluginRevisionOnce.Do(func() {
		digest := sha256.New()
		tree := opnview.BuiltinPlugins()
		pluginRevisionErr = fs.WalkDir(tree, ".", func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			raw, err := fs.ReadFile(tree, path)
			if err != nil {
				return err
			}
			digest.Write([]byte(path))
			digest.Write([]byte{0})
			digest.Write(raw)
			digest.Write([]byte{0})
			return nil
		})
		pluginRevision = "builtin-plugins:" + hex.EncodeToString(digest.Sum(nil))
	})
	return pluginRevision, pluginRevisionErr
}

// loadBuiltinPlugins is the preparation: the built-ins loaded over the database, as the service loads
// them at start.
func loadBuiltinPlugins(ctx context.Context, dsn string) error {
	database, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	if _, err := plugin.Load(ctx, plugin.Options{Store: database}); err != nil {
		_ = database.Close()
		return err
	}
	return database.Close()
}

// NewPluginStore is NewTestStore with the built-in plugins already loaded into the database.
func NewPluginStore(t *testing.T) *store.Store {
	t.Helper()
	revision, err := builtinPluginsRevision()
	if err != nil {
		t.Fatalf("reading the built-in plugins: %v", err)
	}
	database, err := store.Open(context.Background(), storetest.DatabasePrepared(t, revision, loadBuiltinPlugins))
	if err != nil {
		t.Fatalf("opening the test database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("closing the test database: %v", err)
		}
	})
	return database
}
