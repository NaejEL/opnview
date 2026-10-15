// Package plugin is the plugin host. It discovers the built-in plugins and those of
// <data-dir>/plugins, and validates and loads each through one loader (loader.go); adapts each
// provider to its kind's contract (adapters.go, serviceadapters.go, declarative.go, live.go,
// push.go, pushkind.go, pushsink.go); declares a plugin's endpoints to the broker (brokered.go,
// versions.go); keeps its settings (settings.go); and runs a plugin's own tests (testrun.go,
// testpush.go, testwidgets.go). The manifest is internal/plugin/contract's and the interpreter
// bindings are internal/plugin/runtime's. docs/plugins.md is the reference.
package plugin
