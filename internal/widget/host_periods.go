package widget

import (
	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/store"
)

// host.periods: the pre-computed periods of the aggregates, finest first, and how each cuts time
// into slots (docs/data-model.md, "Aggregates").
//
//	host.periods.names()              ["1h", "24h", "7d", "30d"]
//	host.periods.slotStart(name, t)   the start of the slot of a period holding the instant t
//	host.periods.slotEnd(name, start) the end of the slot of a period starting at start

func init() {
	registerHostArea("periods", func(call *scriptCall, area *goja.Object) {
		period := func(value goja.Value) store.Period {
			named, err := store.PeriodNamed(call.textOf(value, "the period"))
			if err != nil {
				call.throw("%v", err)
			}
			return named
		}
		call.function(area, "names", func(goja.FunctionCall) goja.Value {
			names := []string{}
			for _, held := range store.Periods() {
				names = append(names, held.Name)
			}
			return call.value(names)
		})
		call.function(area, "slotStart", func(arguments goja.FunctionCall) goja.Value {
			held := period(arguments.Argument(0))
			return call.vm.ToValue(held.SlotStart(call.integerOf(arguments.Argument(1), "the instant")))
		})
		call.function(area, "slotEnd", func(arguments goja.FunctionCall) goja.Value {
			held := period(arguments.Argument(0))
			return call.vm.ToValue(held.SlotEnd(call.integerOf(arguments.Argument(1), "the slot start")))
		})
	})
}
