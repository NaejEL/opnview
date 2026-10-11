package widget

import (
	"github.com/dop251/goja"
)

// host.matrixplacement: the keys of the members that are not an interface, which the interface
// matrix and the Sankey name their outside and this-firewall ends by (matrixplacement.go).
//
//	host.matrixplacement.keys  {outside, north_south, this_firewall}: the row of traffic arriving from
//	                           outside, the column of traffic leaving to it, and this firewall's own
//	                           row and column

func init() {
	registerHostArea("matrixplacement", func(call *scriptCall, area *goja.Object) {
		_ = area.Set("keys", call.value(map[string]string{
			"outside": MatrixOutside, "north_south": MatrixNorthSouth, "this_firewall": MatrixThisFirewall,
		}))
	})
}
