package widget

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/config"
)

// host.settings: the installation's settings a widget may read -- how opnview is configured, never
// a credential. A setting row can hold the firewall's API key, a plugin's secret or a session's
// state, so a key is read only when it is one of those below; any other is refused by name.
//
//	host.settings.get(key)   the setting's stored value as a string, or null when it is not set
//	host.settings.keys       the keys a widget may read

// widgetSettingKeys are the settings a widget script may read.
var widgetSettingKeys = []string{
	config.KeyAggregateMode, config.KeyRetentionSeconds, config.KeyAttributionMaxDelay, config.KeyCacheAnswerDelay,
	config.KeyLegPairingWindow, config.KeyFirewallLogInterval, config.KeySecurityEventInterval,
	config.KeyMeasurementInterval, config.KeyDHCPLeaseInterval, config.KeyDNSLookupInterval,
	config.KeyResolverCacheInterval, config.KeyResolverLocalDataInterval, config.KeyDiscoveryInterval,
	config.KeyPurgeInterval, config.KeyHeartbeatInterval, config.KeyDowntimeThreshold,
	config.KeyFirewallLogPageSize, config.KeySecurityEventPageSize, config.KeyDHCPLeasePageSize,
}

func init() {
	registerHostArea("settings", func(call *scriptCall, area *goja.Object) {
		r := call.request
		call.function(area, "get", func(arguments goja.FunctionCall) goja.Value {
			key := call.textOf(arguments.Argument(0), "the setting key")
			if !Contains(widgetSettingKeys, key) {
				call.throw("the setting %q is not one a widget reads; the keys are %v", key, widgetSettingKeys)
			}
			var value string
			found, err := r.Db.WidgetRow(r.Ctx, "widget_setting", map[string]any{"key": key}, &value)
			if err != nil {
				return call.fail(err)
			}
			if !found {
				return goja.Null()
			}
			return call.vm.ToValue(value)
		})
		_ = area.Set("keys", call.value(widgetSettingKeys))
	})
}
