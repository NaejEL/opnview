package widget

import (
	"io/fs"
	"sync"
)

// The widgets plugins carry.
//
// A WIDGET PLUGIN IS A PLUGIN DIRECTORY (docs/plugins.md, "Widgets"): plugin.json names the widget
// directories it carries, each widgets/<type>/ holding the widget's manifest.json, its statement
// files and, for the logic a shape cannot express, its script. The plugin loader
// (internal/plugin) discovers the directories -- the built-ins embedded from plugins/<name>/ in the
// repository, a third party's from <data-dir>/plugins/<name>/ -- and hands each to this package as
// a Source; nothing here lists a widget. Adding a widget is adding a directory.

// Source is one widget a plugin carries.
type Source struct {
	// Plugin is the name of the plugin that carries it.
	Plugin string
	// Type is the widget directory's name, which its manifest's type must equal.
	Type string
	// Builtin says the plugin is embedded in the binary: a refusal is then a defect of the build,
	// and the service is not built.
	Builtin bool
	// Location is where the directory was read from, for a message.
	Location string
	// Files is the widget directory: manifest.json, the statement files and the script.
	Files fs.FS
	// BindableKinds are the provider kinds its manifest may bind: the core's and those of the plugins loaded
	// with it. Empty is the core's alone.
	BindableKinds []string
	// Datasets are the data-catalogue names its plugin's manifest declares under `datasets`: what its script may
	// read through host.datasets, beside its own plugin's tables (PA3 of the plugin-API amendment).
	Datasets []string
}

var builtinSources = struct {
	sync.Mutex
	list func() []Source
}{}

// RegisterBuiltinSources registers where the built-in plugins' widgets come from: the plugin
// loader registers it from its own init(), so a service built with no Options.Plugins serves the
// built-in widget plugins whatever builds it.
func RegisterBuiltinSources(list func() []Source) {
	builtinSources.Lock()
	defer builtinSources.Unlock()
	builtinSources.list = list
}

// defaultSources returns the built-in plugins' widgets, or none when no loader registered them.
func defaultSources() []Source {
	builtinSources.Lock()
	list := builtinSources.list
	builtinSources.Unlock()
	if list == nil {
		return nil
	}
	return list()
}
