// Package plugintest is what the built-in plugins' own tests share: the built-in plugins as the
// plugin host loads them, an implementation of one kind by its key, and a call of a function a
// plugin's script exports for its tests.
//
// A built-in plugin's tests live in its own directory under plugins/, beside its manifest, its
// script and its fixtures, and run against the plugin exactly as the binary loads it: through
// plugin.Load, the loader every third-party plugin goes through.
package plugintest

import (
	"context"
	"testing"

	"github.com/NaejEL/opnview/internal/collect/sources"
	"github.com/NaejEL/opnview/internal/plugin"
)

// Builtins is the source set of the built-in plugins.
func Builtins(t *testing.T) *sources.Set {
	t.Helper()
	set := plugin.Builtins()
	if failures := set.Failures(); len(failures) > 0 {
		t.Fatalf("the built-in plugins did not all load: %v", failures)
	}
	return set
}

// Source returns the implementation registered under a key in one kind's registry, failing the
// test when there is none.
func Source[S any](t *testing.T, registry *sources.Registry[S], key string) S {
	t.Helper()
	source, found := registry.Lookup(key)
	if !found {
		t.Fatalf("the %s implementation %q is not registered", registry.Kind(), key)
	}
	return source
}

// Plugin returns a built-in plugin by name.
func Plugin(t *testing.T, name string) *plugin.Plugin {
	t.Helper()
	Builtins(t)
	loaded, found := plugin.BuiltinsLoaded().Plugin(name)
	if !found {
		t.Fatalf("the built-in plugin %s is not loaded", name)
	}
	return loaded
}

// Helper calls a function a built-in plugin's script exports under `helpers`, for its tests.
func Helper(t *testing.T, name, function string, arguments ...any) any {
	t.Helper()
	answer, err := Plugin(t, name).Call(context.Background(), nil, "helpers", function, arguments...)
	if err != nil {
		t.Fatalf("calling %s helpers.%s: %v", name, function, err)
	}
	return answer
}
