// Package opnview embeds the built-in plugins: every source and service opnview reads, each a
// directory of plugins/ in the very format a third party drops into <data-dir>/plugins/
// (docs/plugins.md).
//
// THIS FILE IS AT THE ROOT SO THAT plugins/ HOLDS NO GO AT ALL. A plugin is a manifest, its scripts,
// its catalogue strings, its recorded firewall answers and its tests -- nobody writing one writes,
// builds or runs Go -- and Go's embed directive cannot reach outside the directory of the file that
// carries it, so the one Go file the built-in tree needs sits above it. No line here names a plugin:
// adding a built-in is adding a directory, and the patterns below pick it up.
package opnview

import (
	"embed"
	"io/fs"
)

// embedded is every built-in plugin's manifest, scripts, statement and relation files and catalogue
// strings, and every widget a plugin carries: widgets/<type>/ with its manifest, its statement files
// and its script. Its recorded answers and its tests stay out of the binary.
//
//go:embed plugins/*/plugin.json plugins/*/*.js plugins/*/*.sql plugins/*/locale/*.json
//go:embed plugins/*/widgets/*/manifest.json plugins/*/widgets/*/*.sql plugins/*/widgets/*/*.js
var embedded embed.FS

// BuiltinPlugins is the built-in plugins, one directory each.
func BuiltinPlugins() fs.FS {
	tree, err := fs.Sub(embedded, "plugins")
	if err != nil {
		// fs.Sub fails only on an invalid path, and "plugins" is a valid one.
		return embedded
	}
	return tree
}
