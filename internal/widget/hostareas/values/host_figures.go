package values

import (
	"strings"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/figures"
	"github.com/NaejEL/opnview/internal/widget/script"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// host.figures: the shapes of the figures every widget writes, so a script writes them exactly as
// the core does (docs/widget-api.md, "Figures"). opnview sees only what crosses the firewall, so
// every byte and connection figure is a lower bound and says so beside its measure.
//
// THE MEASURE IS THE CORE'S (internal/widget/request/measure.go). Summable figures carry the
// measure they were read in: the core's reads hand a widget whose manifest declares volumes the
// figures of the measure its request is answered under, and these functions build every Volume and
// Count in the figures' own measure, judging, under netflow, whether every connection counted was
// measured. A widget never chooses a measure, and two measures are never summed.
//
//	host.figures.volume(figures)             the Volume of summable figures, in their measure: {measure,
//	                                         lower_bound, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
//	                                         connections, allowed_connections, blocked_connections,
//	                                         unknown_connections}, and for a netflow Volume its packets and
//	                                         its coverage, whole and per part (packets, allowed_packets ...
//	                                         coverage, allowed_coverage ...; docs/widget-api.md)
//	host.figures.logged(figures)             the same: the name it had while every figure was logged
//	host.figures.count(value)                a count of the connections or records the answer's figures
//	                                         count: {measure, lower_bound: true, value}, in the measure the
//	                                         request is answered under
//	host.figures.byteCount(figures)          the Count of the figures' bytes, in their measure
//	host.figures.connectionBytes(figures)    the same for one connection's figures: a netflow figure of
//	                                         one connection measured in both directions is exact, and the
//	                                         only figure whose lower_bound is false
//	host.figures.cover(figures)              says the answer counts figures whose numbers the widget uses
//	                                         directly, so the core judges them as it judges a Volume
//	host.figures.loggedCount(value)          {measure: "logged", lower_bound: true, value}: a count of logged
//	                                         records, which an answer under netflow cannot carry
//	host.figures.withMeasure(figures, from)  figures carrying the measure of `from` (a row or figures read
//	                                         in it): its measure and its netflow parts
//	host.figures.zero()                      summable figures holding nothing, in the measure the request
//	                                         is answered under
//	host.figures.add(into, other)            the sum of two summable figures, as a new object
//	host.figures.value(measure, figures)     the figure a `measure` names: bytes, connections, allowed
//	                                         or blocked
//	host.figures.remainder(members, volume, count)  what a truncating parameter dropped: {members,
//	                                         volume?, count?}, a null volume or count left out; count is a
//	                                         Count built here, or a number read as loggedCount reads one
//	host.figures.compare(a, b)               -1, 0 or 1: two strings in Go's byte order, which is the
//	                                         order every key of the core is ranked in (JavaScript's <
//	                                         compares UTF-16 code units and differs outside ASCII)
//	host.figures.nameFreeCompare(a, b)       -1, 0 or 1: the tie-break between rows whose name is
//	                                         withheld, by a hash of the name that says nothing about it
//	host.figures.interfaceKey(id)            "interface:<id>", an interface's member key
//	host.figures.clientKey(id)               "client:<id>", a client's member key
//	host.figures.measures                    the measures, by name: logged, netflow, sampled,
//	                                         interface_counter
//	host.figures.measure                     the measure the request is answered under: logged or netflow
//
// Summable figures are {bytes, allowed_bytes, blocked_bytes, unknown_bytes, allowed_connections,
// blocked_connections, unknown_connections}; a member left out is 0. Netflow figures also carry
// measure: "netflow" and the fifty-four columns of netflow_parts (store.NetflowPartsColumns): for each
// of the allowed, blocked and unknown parts, its packets, its four coverage classes and the direction
// states it met. A row of a statement read under netflow carries them as columns of those names --
// (netflow_sum(n.parts)).* writes them all -- and a Volume handed back is read as its figures.

func init() {
	script.RegisterHostArea("figures", func(call *script.ScriptCall, area *goja.Object) {
		r := call.Request
		volume := func(arguments goja.FunctionCall) goja.Value {
			return call.Value(r.VolumeOf(FiguresOf(call, arguments.Argument(0), "the figures")))
		}
		call.Function(area, "volume", volume)
		call.Function(area, "logged", volume)
		call.Function(area, "count", func(arguments goja.FunctionCall) goja.Value {
			return call.Value(r.CountOf(call.IntegerOf(arguments.Argument(0), "the count")))
		})
		call.Function(area, "byteCount", func(arguments goja.FunctionCall) goja.Value {
			return call.Value(r.ByteCountOf(FiguresOf(call, arguments.Argument(0), "the figures"), false))
		})
		call.Function(area, "connectionBytes", func(arguments goja.FunctionCall) goja.Value {
			return call.Value(r.ByteCountOf(FiguresOf(call, arguments.Argument(0), "the figures"), true))
		})
		call.Function(area, "cover", func(arguments goja.FunctionCall) goja.Value {
			r.CoverFigures(FiguresOf(call, arguments.Argument(0), "the figures"))
			return goja.Undefined()
		})
		call.Function(area, "loggedCount", func(arguments goja.FunctionCall) goja.Value {
			return call.Value(r.LoggedCountOf(call.IntegerOf(arguments.Argument(0), "the count")))
		})
		call.Function(area, "withMeasure", func(arguments goja.FunctionCall) goja.Value {
			figures := FiguresOf(call, arguments.Argument(0), "the figures")
			from := FiguresOf(call, arguments.Argument(1), "the figures whose measure is taken")
			figures.Netflow, figures.Parts = from.Netflow, from.Parts
			return call.Value(traffic.Summable(figures))
		})
		call.Function(area, "zero", func(goja.FunctionCall) goja.Value {
			return call.Value(traffic.Summable(reads.VolumeFigures{Netflow: r.Netflow()}))
		})
		call.Function(area, "add", func(arguments goja.FunctionCall) goja.Value {
			sum := FiguresOf(call, arguments.Argument(0), "the figures added to")
			envelope.AddFigures(&sum, FiguresOf(call, arguments.Argument(1), "the figures added"))
			return call.Value(traffic.Summable(sum))
		})
		call.Function(area, "value", func(arguments goja.FunctionCall) goja.Value {
			measure := call.TextOf(arguments.Argument(0), "the measure")
			return call.VM.ToValue(traffic.FiguresValue(measure, FiguresOf(call, arguments.Argument(1), "the figures")))
		})
		call.Function(area, "remainder", func(arguments goja.FunctionCall) goja.Value {
			remainder := envelope.Remainder{Members: call.IntegerOf(arguments.Argument(0), "the members dropped")}
			if !script.Absent(arguments.Argument(1)) {
				volume := r.VolumeOf(FiguresOf(call, arguments.Argument(1), "the volume dropped"))
				remainder.Volume = &volume
			}
			if counted := arguments.Argument(2); !script.Absent(counted) {
				if _, isObject := counted.(*goja.Object); isObject {
					var count envelope.Count
					call.DecodeFlat(counted, "the count dropped", &count)
					remainder.Count = &count
				} else {
					count := r.LoggedCountOf(call.IntegerOf(counted, "the count dropped"))
					remainder.Count = &count
				}
			}
			return call.Value(remainder)
		})
		call.Function(area, "compare", func(arguments goja.FunctionCall) goja.Value {
			return call.VM.ToValue(strings.Compare(call.TextOf(arguments.Argument(0), "a"),
				call.TextOf(arguments.Argument(1), "b")))
		})
		call.Function(area, "nameFreeCompare", func(arguments goja.FunctionCall) goja.Value {
			left := figures.NameFreeOrder(call.TextOf(arguments.Argument(0), "a"))
			right := figures.NameFreeOrder(call.TextOf(arguments.Argument(1), "b"))
			switch {
			case left < right:
				return call.VM.ToValue(-1)
			case left > right:
				return call.VM.ToValue(1)
			}
			return call.VM.ToValue(0)
		})
		call.Function(area, "interfaceKey", func(arguments goja.FunctionCall) goja.Value {
			return call.VM.ToValue(traffic.InterfaceKey(call.IntegerOf(arguments.Argument(0), "the interface id")))
		})
		call.Function(area, "clientKey", func(arguments goja.FunctionCall) goja.Value {
			return call.VM.ToValue(traffic.ClientKey(call.IntegerOf(arguments.Argument(0), "the client id")))
		})
		_ = area.Set("measures", call.Value(map[string]string{
			"logged": envelope.MeasureLogged, "netflow": envelope.MeasureNetflow, "sampled": envelope.MeasureSampled,
			"interface_counter": envelope.MeasureInterfaceCounter,
		}))
		_ = area.Set("measure", r.MeasureInForce())
	})
}

// FiguresOf reads summable figures a script hands the host.
func FiguresOf(c *script.ScriptCall, value goja.Value, what string) reads.VolumeFigures {
	if script.Absent(value) {
		c.Throw("%s are %s, not summable figures", what, script.Describe(value))
	}
	var figures traffic.SummableFigures
	c.DecodeFlat(value, what, &figures)
	return FromScript(c, figures)
}

// FromScript is figures a script hands back, as the core reads them. Under a netflow run, figures
// holding anything that are not netflow were not read by the core in that measure: the run cannot be
// served as netflow (internal/widget/request/measure.go).
func FromScript(c *script.ScriptCall, figures traffic.SummableFigures) reads.VolumeFigures {
	read := figures.ToStore()
	if c.Request.Measure.Netflow && !read.Netflow && read != (reads.VolumeFigures{}) {
		c.Request.Measure.LoggedFigure = true
	}
	return read
}

// MeasuredOf is the netflow members of a row a script hands back, as a row's measured figures
// (internal/widget/figures/sites.go): judged as figuresOf judges figures holding bytes.
func MeasuredOf(c *script.ScriptCall, members traffic.NetflowMembers, bytes int64) reads.VolumeFigures {
	read := FromScript(c, traffic.SummableFigures{Bytes: bytes, NetflowMembers: members})
	read.Bytes = 0
	return read
}
