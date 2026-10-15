package values

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/script"
	"github.com/NaejEL/opnview/internal/widget/shapes"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// host.mapfilters: the allowed or the blocked half of summable figures
// (internal/widget/shapes/mapfilters.go and internal/widget/shapes/shape_map.go), which the maps
// and the destination tables keep apart.
//
//	host.mapfilters.allowedOnly(figures)  {bytes: allowed_bytes, allowed_bytes, allowed_connections}
//	                                      and every other member 0
//	host.mapfilters.blockedOnly(figures)  {bytes: blocked_bytes, blocked_bytes, blocked_connections}
//	                                      and every other member 0
//
// figures are summable figures (host.figures); a row of host.volume.window() is read as its own.

func init() {
	script.RegisterHostArea("mapfilters", func(call *script.ScriptCall, area *goja.Object) {
		call.Function(area, "allowedOnly", func(arguments goja.FunctionCall) goja.Value {
			return call.Value(traffic.Summable(shapes.AllowedOnly(FiguresOf(call, arguments.Argument(0), "the figures"))))
		})
		call.Function(area, "blockedOnly", func(arguments goja.FunctionCall) goja.Value {
			return call.Value(traffic.Summable(shapes.BlockedOnly(FiguresOf(call, arguments.Argument(0), "the figures"))))
		})
	})
}
