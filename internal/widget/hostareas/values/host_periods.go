package values

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/script"
)

// host.periods: the pre-computed periods of the aggregates, finest first, and how each cuts time
// into slots (docs/data-model.md, "Aggregates").
//
//	host.periods.names()              ["1h", "24h", "7d", "30d"]
//	host.periods.slotStart(name, t)   the start of the slot of a period holding the instant t
//	host.periods.slotEnd(name, start) the end of the slot of a period starting at start

func init() {
	script.RegisterHostArea("periods", func(call *script.ScriptCall, area *goja.Object) {
		period := func(value goja.Value) vocab.Period {
			named, err := vocab.PeriodNamed(call.TextOf(value, "the period"))
			if err != nil {
				call.Throw("%v", err)
			}
			return named
		}
		call.Function(area, "names", func(goja.FunctionCall) goja.Value {
			names := []string{}
			for _, held := range vocab.Periods() {
				names = append(names, held.Name)
			}
			return call.Value(names)
		})
		call.Function(area, "slotStart", func(arguments goja.FunctionCall) goja.Value {
			held := period(arguments.Argument(0))
			return call.VM.ToValue(held.SlotStart(call.IntegerOf(arguments.Argument(1), "the instant")))
		})
		call.Function(area, "slotEnd", func(arguments goja.FunctionCall) goja.Value {
			held := period(arguments.Argument(0))
			return call.VM.ToValue(held.SlotEnd(call.IntegerOf(arguments.Argument(1), "the slot start")))
		})
	})
}
