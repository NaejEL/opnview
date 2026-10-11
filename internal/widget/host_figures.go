package widget

import (
	"strings"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/store"
)

// host.figures: the shapes of the figures every widget writes, so a script writes them exactly as
// the core does (docs/widget-api.md, "Figures"). opnview sees only what crosses the firewall, so
// every byte and connection figure is a lower bound and says so beside its measure.
//
//	host.figures.loggedCount(value)          {measure: "logged", lower_bound: true, value}
//	host.figures.logged(figures)             the logged volume of summable figures: {measure, lower_bound,
//	                                         bytes, allowed_bytes, blocked_bytes, unknown_bytes, connections,
//	                                         allowed_connections, blocked_connections, unknown_connections}
//	host.figures.zero()                      summable figures holding nothing
//	host.figures.add(into, other)            the sum of two summable figures, as a new object
//	host.figures.value(measure, figures)     the figure a `measure` names: bytes, connections, allowed
//	                                         or blocked
//	host.figures.remainder(members, volume, count)  what a truncating parameter dropped: {members,
//	                                         volume?, count?}, a null volume or count left out
//	host.figures.compare(a, b)               -1, 0 or 1: two strings in Go's byte order, which is the
//	                                         order every key of the core is ranked in (JavaScript's <
//	                                         compares UTF-16 code units and differs outside ASCII)
//	host.figures.nameFreeCompare(a, b)       -1, 0 or 1: the tie-break between rows whose name is
//	                                         withheld, by a hash of the name that says nothing about it
//	host.figures.interfaceKey(id)            "interface:<id>", an interface's member key
//	host.figures.clientKey(id)               "client:<id>", a client's member key
//	host.figures.measures                    the measures, by name: logged, sampled, interface_counter
//
// Summable figures are {bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
// blocked_connections, unknown_connections}; a member left out is 0.

func init() {
	registerHostArea("figures", func(call *scriptCall, area *goja.Object) {
		call.function(area, "loggedCount", func(arguments goja.FunctionCall) goja.Value {
			return call.value(LoggedCount(call.integerOf(arguments.Argument(0), "the count")))
		})
		call.function(area, "logged", func(arguments goja.FunctionCall) goja.Value {
			return call.value(Logged(call.figuresOf(arguments.Argument(0), "the figures")))
		})
		call.function(area, "zero", func(goja.FunctionCall) goja.Value {
			return call.value(summable(store.VolumeFigures{}))
		})
		call.function(area, "add", func(arguments goja.FunctionCall) goja.Value {
			sum := call.figuresOf(arguments.Argument(0), "the figures added to")
			AddFigures(&sum, call.figuresOf(arguments.Argument(1), "the figures added"))
			return call.value(summable(sum))
		})
		call.function(area, "value", func(arguments goja.FunctionCall) goja.Value {
			measure := call.textOf(arguments.Argument(0), "the measure")
			return call.vm.ToValue(FiguresValue(measure, call.figuresOf(arguments.Argument(1), "the figures")))
		})
		call.function(area, "remainder", func(arguments goja.FunctionCall) goja.Value {
			remainder := Remainder{Members: call.integerOf(arguments.Argument(0), "the members dropped")}
			if !absent(arguments.Argument(1)) {
				volume := Logged(call.figuresOf(arguments.Argument(1), "the volume dropped"))
				remainder.Volume = &volume
			}
			if !absent(arguments.Argument(2)) {
				remainder.Count = CountPointer(call.integerOf(arguments.Argument(2), "the count dropped"))
			}
			return call.value(remainder)
		})
		call.function(area, "compare", func(arguments goja.FunctionCall) goja.Value {
			return call.vm.ToValue(strings.Compare(call.textOf(arguments.Argument(0), "a"),
				call.textOf(arguments.Argument(1), "b")))
		})
		call.function(area, "nameFreeCompare", func(arguments goja.FunctionCall) goja.Value {
			left := NameFreeOrder(call.textOf(arguments.Argument(0), "a"))
			right := NameFreeOrder(call.textOf(arguments.Argument(1), "b"))
			switch {
			case left < right:
				return call.vm.ToValue(-1)
			case left > right:
				return call.vm.ToValue(1)
			}
			return call.vm.ToValue(0)
		})
		call.function(area, "interfaceKey", func(arguments goja.FunctionCall) goja.Value {
			return call.vm.ToValue(InterfaceKey(call.integerOf(arguments.Argument(0), "the interface id")))
		})
		call.function(area, "clientKey", func(arguments goja.FunctionCall) goja.Value {
			return call.vm.ToValue(ClientKey(call.integerOf(arguments.Argument(0), "the client id")))
		})
		_ = area.Set("measures", call.value(map[string]string{
			"logged": MeasureLogged, "sampled": MeasureSampled, "interface_counter": MeasureInterfaceCounter,
		}))
	})
}

// summableFigures is summable figures as a script reads and writes them.
type summableFigures struct {
	Bytes              int64 `json:"bytes"`
	AllowedBytes       int64 `json:"allowed_bytes"`
	BlockedBytes       int64 `json:"blocked_bytes"`
	UnknownBytes       int64 `json:"unknown_bytes"`
	AllowedConnections int64 `json:"allowed_connections"`
	BlockedConnections int64 `json:"blocked_connections"`
	UnknownConnections int64 `json:"unknown_connections"`
}

// summable is store figures as a script reads them.
func summable(figures store.VolumeFigures) summableFigures {
	return summableFigures{Bytes: figures.Bytes, AllowedBytes: figures.AllowedBytes, BlockedBytes: figures.BlockedBytes,
		UnknownBytes: figures.UnknownBytes, AllowedConnections: figures.AllowedConnections,
		BlockedConnections: figures.BlockedConnections, UnknownConnections: figures.UnknownConnections}
}

// figuresOf reads summable figures a script hands the host.
func (c *scriptCall) figuresOf(value goja.Value, what string) store.VolumeFigures {
	if absent(value) {
		c.throw("%s are %s, not summable figures", what, describe(value))
	}
	var figures summableFigures
	c.decodeArgument(value, what, &figures)
	return store.VolumeFigures{Bytes: figures.Bytes, AllowedBytes: figures.AllowedBytes,
		BlockedBytes: figures.BlockedBytes, UnknownBytes: figures.UnknownBytes,
		AllowedConnections: figures.AllowedConnections, BlockedConnections: figures.BlockedConnections,
		UnknownConnections: figures.UnknownConnections}
}
