// Package widgets links the built-in widgets into whatever imports it: the binary, the tests and
// the budget tool.
//
// EVERY BUILT-IN WIDGET IS A WIDGET PLUGIN: a directory plugins/<plugin>/widgets/<type>/ in the
// repository, holding its manifest.json, its statement files and, for the logic a shape cannot
// express, its script. The plugin loader (internal/plugin) embeds that tree and registers each
// widget with the widget core; this package holds no widget of its own and no list of them.
// Adding a built-in widget is adding a plugin directory, and nothing else (docs/plugins.md,
// "Widgets").
//
// The package stays as the one import that makes the built-in widgets exist, and as the home of
// the tests every built-in widget answers together: the golden responses, the cross-widget
// conditions and the data checks.
package widgets

import (
	// The plugin loader registers the built-in plugins' widgets with the widget core.
	_ "github.com/NaejEL/opnview/internal/plugin"
)
