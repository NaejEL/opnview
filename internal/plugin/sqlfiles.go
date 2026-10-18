package plugin

import (
	"context"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/store/plugintables"
	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// A plugin's SQL files (ADR 0018; docs/plugins.md, "Statements", "Relations", "Requires"): the statements
// its derivations run, parsed here and planned under the plugin's role once its tables exist; the relation
// files that publish its views and functions, checked here and applied by the store; and the plugins it
// requires, which must have loaded before it.

// sqlFilePattern is what a statement or relation file name matches: a plain .sql file of the plugin's
// own directory.
var sqlFilePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*\.sql$`)

// checkStatementFiles reads and parses every statement file the manifest lists, and returns the
// statements by name. Each is translated once here, so a malformed parameter refuses the plugin.
func checkStatementFiles(plugin *Plugin, directory fs.FS) (map[string]string, *contract.Refusal) {
	manifest := plugin.Manifest
	refuse := func(format string, arguments ...any) *contract.Refusal {
		return contract.Refuse(manifest.Name, contract.RefusalStatementRefused, format, arguments...)
	}
	statements := map[string]string{}
	for _, file := range manifest.Statements {
		if !sqlFilePattern.MatchString(file) {
			return nil, refuse("the statement file %q is not a plain .sql file name of the plugin's directory", file)
		}
		raw, err := fs.ReadFile(directory, file)
		if err != nil {
			return nil, refuse("the statement file %s cannot be read: %v", file, err)
		}
		parsed, err := sqldb.ParseStatements(string(raw))
		if err != nil {
			return nil, refuse("the statement file %s: %v", file, err)
		}
		for name, text := range parsed {
			if !contract.NamePattern.MatchString(name) {
				return nil, refuse("the statement %q of %s is not named by a snake_case word", name, file)
			}
			if _, duplicate := statements[name]; duplicate {
				return nil, refuse("the statement %q is named in two files", name)
			}
			if _, err := sqldb.Translate(text); err != nil {
				return nil, refuse("the statement %s of %s: %v", name, file, err)
			}
			statements[name] = text
		}
	}
	return statements, nil
}

// checkRelationFiles reads every relation file the manifest lists and checks its statements without a
// database: what they create, and where.
func checkRelationFiles(plugin *Plugin, directory fs.FS) ([]plugintables.RelationFile, *contract.Refusal) {
	manifest := plugin.Manifest
	refuse := func(format string, arguments ...any) *contract.Refusal {
		return contract.Refuse(manifest.Name, contract.RefusalRelationRefused, format, arguments...)
	}
	var files []plugintables.RelationFile
	for _, name := range manifest.Relations {
		if !sqlFilePattern.MatchString(name) {
			return nil, refuse("the relation file %q is not a plain .sql file name of the plugin's directory", name)
		}
		raw, err := fs.ReadFile(directory, name)
		if err != nil {
			return nil, refuse("the relation file %s cannot be read: %v", name, err)
		}
		file := plugintables.RelationFile{Name: name, Text: string(raw)}
		if err := plugintables.CheckRelationFile(manifest.Name, file); err != nil {
			return nil, refuse("%v", err)
		}
		files = append(files, file)
	}
	return files, nil
}

// checkRequires checks that every plugin the manifest requires has loaded: the loader orders the
// candidates so that a required plugin comes first, and one that is absent, refused, or in a cycle with
// this one is not in taken.
func checkRequires(manifest contract.Manifest, taken map[string]string) *contract.Refusal {
	seen := map[string]bool{}
	for _, required := range manifest.Requires {
		if !contract.NamePattern.MatchString(required) || required == manifest.Name || seen[required] {
			return contract.Refuse(manifest.Name, contract.RefusalRequiresMissing,
				"requires %q, which is not another plugin's name declared once", required)
		}
		seen[required] = true
		if _, loaded := taken[required]; !loaded {
			return contract.Refuse(manifest.Name, contract.RefusalRequiresMissing,
				"requires the plugin %s, which is not loaded: absent, refused, or requiring this one back", required)
		}
	}
	return nil
}

// applySQL is the side effect of the SQL files, once the plugin's tables are ensured on a store: the
// relations are applied, and every statement is planned under the plugin's role.
func applySQL(ctx context.Context, plugin *Plugin, options Options) *contract.Refusal {
	if options.Store == nil || (len(plugin.relations) == 0 && len(plugin.statements) == 0) {
		return nil
	}
	if len(plugin.OwnTables) == 0 {
		// The role and the schema the relations and the planning need, which a table would have created.
		if err := options.Store.EnsurePluginSchema(ctx, plugin.Name); err != nil {
			return contract.Refuse(plugin.Name, contract.RefusalRelationRefused, "%v", err)
		}
	}
	if err := options.Store.ApplyRelations(ctx, plugin.Name, plugin.relations); err != nil {
		return contract.Refuse(plugin.Name, contract.RefusalRelationRefused, "%v", err)
	}
	names := make([]string, 0, len(plugin.statements))
	for name := range plugin.statements {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := options.Store.CheckPluginStatement(ctx, plugin.Name, plugin.statements[name]); err != nil {
			return contract.Refuse(plugin.Name, contract.RefusalStatementRefused,
				"the statement %s does not plan under the plugin's role: %v", name, strings.TrimSpace(err.Error()))
		}
	}
	return nil
}

// orderByRequires orders candidate plugin names so that every plugin comes after the ones it requires,
// among the candidates and the names already taken; a name whose requirement is neither is left where it
// is, and its load refuses it. A cycle is broken by name order, and its members refuse each other.
func orderByRequires(names []string, requires map[string][]string, taken map[string]string) []string {
	remaining := append([]string(nil), names...)
	sort.Strings(remaining)
	loaded := map[string]bool{}
	for name := range taken {
		loaded[name] = true
	}
	pending := map[string]bool{}
	for _, name := range remaining {
		pending[name] = true
	}
	var ordered []string
	for len(remaining) > 0 {
		progressed := false
		for index := 0; index < len(remaining); index++ {
			name := remaining[index]
			ready := true
			for _, required := range requires[name] {
				if !loaded[required] && pending[required] {
					ready = false
					break
				}
			}
			if ready {
				ordered = append(ordered, name)
				loaded[name] = true
				delete(pending, name)
				remaining = append(remaining[:index], remaining[index+1:]...)
				progressed = true
				index--
			}
		}
		if !progressed {
			// A cycle: every member waits for another. They load in name order and refuse each other.
			ordered = append(ordered, remaining...)
			break
		}
	}
	return ordered
}

// manifestRequires reads just the requires of a directory's manifest, for the load order; a directory
// whose manifest cannot be read requires nothing, and its load reports why.
func manifestRequires(directory fs.FS) []string {
	raw, err := fs.ReadFile(directory, contract.ManifestFile)
	if err != nil {
		return nil
	}
	manifest, err := contract.DecodeManifest(raw)
	if err != nil {
		return nil
	}
	return manifest.Requires
}

// describeRequires is the list a refusal names.
