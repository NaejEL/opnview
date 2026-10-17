package values

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/script"
)

// host.settings: the installation's settings a widget may read -- how opnview is configured, never
// a credential. A setting row can hold the firewall's API key, a plugin's secret or a session's
// state, so a key is read only when it is one of those below; any other is refused by name.
//
//	host.settings.get(key)   the setting's stored value as a string, or null when it is not set
//	host.settings.keys       the keys a widget may read

// widgetSettingKeys are the settings a widget script may read: the core's own, then every core kind's
// cadences and page size, under the keys its kind file declares.
var widgetSettingKeys = append([]string{
	config.KeyAggregateMode, config.KeyRetentionSeconds, config.KeyAttributionMaxDelay, config.KeyCacheAnswerDelay,
	config.KeyLegPairingWindow, config.KeyDiscoveryInterval, config.KeyPurgeInterval, config.KeyHeartbeatInterval,
	config.KeyDowntimeThreshold,
}, config.KindSettingKeys()...)

func init() {
	script.RegisterHostArea("settings", func(call *script.ScriptCall, area *goja.Object) {
		r := call.Request
		call.Function(area, "get", func(arguments goja.FunctionCall) goja.Value {
			key := call.TextOf(arguments.Argument(0), "the setting key")
			if !params.Contains(widgetSettingKeys, key) {
				call.Throw("the setting %q is not one a widget reads; the keys are %v", key, widgetSettingKeys)
			}
			var value string
			found, err := r.Db.WidgetRow(r.Ctx, "widget_setting", map[string]any{"key": key}, &value)
			if err != nil {
				return call.Fail(err)
			}
			if !found {
				return goja.Null()
			}
			return call.VM.ToValue(value)
		})
		_ = area.Set("keys", call.Value(widgetSettingKeys))
	})
}
