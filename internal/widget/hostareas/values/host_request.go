package values

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/script"
)

// host.request: the request being answered.
//
//	host.request.type            the widget's type
//	host.request.now             the instant the request is answered at, in epoch seconds
//	host.request.window          the requested window {from, to} of a windowed widget, null otherwise
//	host.request.aggregate_mode  the aggregate_mode setting in force: "full" or "no_domains"
//	host.request.no_domains      true when the aggregate mode withholds domain names

func init() {
	script.RegisterHostArea("request", func(call *script.ScriptCall, area *goja.Object) {
		r := call.Request
		_ = area.Set("type", r.Preset.Type)
		_ = area.Set("now", r.Now)
		if r.Preset.Windowed {
			_ = area.Set("window", call.Value(r.Window))
		} else {
			_ = area.Set("window", goja.Null())
		}
		_ = area.Set("aggregate_mode", r.Mode)
		_ = area.Set("no_domains", r.NoDomains())
	})
}
