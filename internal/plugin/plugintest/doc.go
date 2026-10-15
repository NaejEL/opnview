// Package plugintest is what the built-in plugins' own tests share: the built-in plugins as the
// plugin host loads them, an implementation of one kind by its key, and a call of a function a
// plugin's script exports for its tests.
//
// A built-in plugin's tests live in its own directory under plugins/, beside its manifest, its
// script and its fixtures, and run against the plugin exactly as the binary loads it: through
// plugin.Load, the loader every third-party plugin goes through.
package plugintest
