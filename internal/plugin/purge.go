package plugin

import (
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/store/plugintables"
)

// checkPurge reads what the manifest names for the retention purge (ADR 0021, step 6;
// docs/plugins.md, "The retention purge"): the statements run before any row is purged, and those
// purging the plugin's own rows, each a statement its statement files hold, named once.
func checkPurge(plugin *Plugin) (plugintables.PurgeHook, *contract.Refusal) {
	hook := plugintables.PurgeHook{Plugin: plugin.Name}
	seen := map[string]bool{}
	named := func(key string, names []string) ([]plugintables.NamedStatement, *contract.Refusal) {
		statements := make([]plugintables.NamedStatement, 0, len(names))
		for _, name := range names {
			text, held := plugin.statements[name]
			if !held {
				return nil, contract.Refuse(plugin.Name, contract.RefusalPurgeInvalid,
					"%s names the statement %q, which no statement file of the plugin holds", key, name)
			}
			if seen[name] {
				return nil, contract.Refuse(plugin.Name, contract.RefusalPurgeInvalid,
					"%s names the statement %q, which the purge already runs", key, name)
			}
			seen[name] = true
			statements = append(statements, plugintables.NamedStatement{Name: name, Text: text})
		}
		return statements, nil
	}
	var refusal *contract.Refusal
	if hook.Before, refusal = named("before_purge", plugin.Manifest.BeforePurge); refusal != nil {
		return plugintables.PurgeHook{}, refusal
	}
	if hook.Purge, refusal = named("purge", plugin.Manifest.Purge); refusal != nil {
		return plugintables.PurgeHook{}, refusal
	}
	return hook, nil
}

// purges reports whether a hook takes part in the retention purge at all.
func purges(hook plugintables.PurgeHook) bool { return len(hook.Before)+len(hook.Purge) > 0 }
