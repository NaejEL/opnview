package widget

import (
	"github.com/dop251/goja"
)

// host.mapfilters: the allowed or the blocked half of summable figures (mapfilters.go and
// shape_map.go), which the maps and the destination tables keep apart.
//
//	host.mapfilters.allowedOnly(figures)  {bytes: allowed_bytes, allowed_bytes, allowed_connections}
//	                                      and every other member 0
//	host.mapfilters.blockedOnly(figures)  {bytes: blocked_bytes, blocked_bytes, blocked_connections}
//	                                      and every other member 0
//
// figures are summable figures (host.figures); a row of host.volume.window() is read as its own.

func init() {
	registerHostArea("mapfilters", func(call *scriptCall, area *goja.Object) {
		call.function(area, "allowedOnly", func(arguments goja.FunctionCall) goja.Value {
			return call.value(summable(AllowedOnly(call.figuresOf(arguments.Argument(0), "the figures"))))
		})
		call.function(area, "blockedOnly", func(arguments goja.FunctionCall) goja.Value {
			return call.value(summable(BlockedOnly(call.figuresOf(arguments.Argument(0), "the figures"))))
		})
	})
}
