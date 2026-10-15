package values

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/script"
	"github.com/NaejEL/opnview/internal/widget/shapes"
)

// host.matrixplacement: the keys of the members that are not an interface, which the interface
// matrix and the Sankey name their outside and this-firewall ends by
// (internal/widget/shapes/matrixplacement.go).
//
//	host.matrixplacement.keys  {outside, north_south, this_firewall}: the row of traffic arriving from
//	                           outside, the column of traffic leaving to it, and this firewall's own
//	                           row and column

func init() {
	script.RegisterHostArea("matrixplacement", func(call *script.ScriptCall, area *goja.Object) {
		_ = area.Set("keys", call.Value(map[string]string{
			"outside": shapes.MatrixOutside, "north_south": shapes.MatrixNorthSouth, "this_firewall": shapes.MatrixThisFirewall,
		}))
	})
}
