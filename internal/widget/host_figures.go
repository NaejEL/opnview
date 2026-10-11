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
// THE MEASURE IS THE CORE'S (measure.go). Summable figures carry the measure they were read in: the
// core's reads hand a widget whose manifest declares volumes the figures of the measure its request
// is answered under, and these functions build every Volume and Count in the figures' own measure,
// judging, under netflow, whether every connection counted was measured. A widget never chooses a
// measure, and two measures are never summed.
//
//	host.figures.volume(figures)             the Volume of summable figures, in their measure: {measure,
//	                                         lower_bound, bytes, allowed_bytes, blocked_bytes, unknown_bytes,
//	                                         connections, allowed_connections, blocked_connections,
//	                                         unknown_connections}, and for a netflow Volume its packets and
//	                                         its coverage {measured, partial, unmeasured, not_joinable}
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
//	                                         in it): its measure, packets and coverage
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
// measure: "netflow", packets, measured_connections, partial_connections, unmeasured_connections and
// not_joinable_connections (coverage_unknown: true, and no packets, for the allowed or the blocked
// part of figures whose connections were not all measured). A row of a statement read under netflow
// carries them as columns of those names, and a Volume handed back is read as its figures.

func init() {
	registerHostArea("figures", func(call *scriptCall, area *goja.Object) {
		r := call.request
		volume := func(arguments goja.FunctionCall) goja.Value {
			return call.value(r.VolumeOf(call.figuresOf(arguments.Argument(0), "the figures")))
		}
		call.function(area, "volume", volume)
		call.function(area, "logged", volume)
		call.function(area, "count", func(arguments goja.FunctionCall) goja.Value {
			return call.value(r.CountOf(call.integerOf(arguments.Argument(0), "the count")))
		})
		call.function(area, "byteCount", func(arguments goja.FunctionCall) goja.Value {
			return call.value(r.ByteCountOf(call.figuresOf(arguments.Argument(0), "the figures"), false))
		})
		call.function(area, "connectionBytes", func(arguments goja.FunctionCall) goja.Value {
			return call.value(r.ByteCountOf(call.figuresOf(arguments.Argument(0), "the figures"), true))
		})
		call.function(area, "cover", func(arguments goja.FunctionCall) goja.Value {
			r.CoverFigures(call.figuresOf(arguments.Argument(0), "the figures"))
			return goja.Undefined()
		})
		call.function(area, "loggedCount", func(arguments goja.FunctionCall) goja.Value {
			return call.value(r.LoggedCountOf(call.integerOf(arguments.Argument(0), "the count")))
		})
		call.function(area, "withMeasure", func(arguments goja.FunctionCall) goja.Value {
			figures := call.figuresOf(arguments.Argument(0), "the figures")
			from := call.figuresOf(arguments.Argument(1), "the figures whose measure is taken")
			figures.Netflow, figures.Packets, figures.PacketsUnknown, figures.Coverage = from.Netflow, from.Packets,
				from.PacketsUnknown, from.Coverage
			return call.value(summable(figures))
		})
		call.function(area, "zero", func(goja.FunctionCall) goja.Value {
			return call.value(summable(store.VolumeFigures{Netflow: r.Netflow()}))
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
				volume := r.VolumeOf(call.figuresOf(arguments.Argument(1), "the volume dropped"))
				remainder.Volume = &volume
			}
			if counted := arguments.Argument(2); !absent(counted) {
				if _, isObject := counted.(*goja.Object); isObject {
					var count Count
					call.decodeArgument(counted, "the count dropped", &count)
					remainder.Count = &count
				} else {
					count := r.LoggedCountOf(call.integerOf(counted, "the count dropped"))
					remainder.Count = &count
				}
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
			"logged": MeasureLogged, "netflow": MeasureNetflow, "sampled": MeasureSampled,
			"interface_counter": MeasureInterfaceCounter,
		}))
		_ = area.Set("measure", r.MeasureInForce())
	})
}

// summableFigures is summable figures as a script reads and writes them. The members past the seven
// are a netflow figure's, and are absent from a logged one, which is written as it always was.
type summableFigures struct {
	Bytes              int64 `json:"bytes"`
	AllowedBytes       int64 `json:"allowed_bytes"`
	BlockedBytes       int64 `json:"blocked_bytes"`
	UnknownBytes       int64 `json:"unknown_bytes"`
	AllowedConnections int64 `json:"allowed_connections"`
	BlockedConnections int64 `json:"blocked_connections"`
	UnknownConnections int64 `json:"unknown_connections"`
	netflowMembers
}

// netflowMembers are what a netflow figure carries beyond the seven summable members.
type netflowMembers struct {
	Measure                string `json:"measure,omitempty"`
	Packets                *int64 `json:"packets,omitempty"`
	MeasuredConnections    *int64 `json:"measured_connections,omitempty"`
	PartialConnections     *int64 `json:"partial_connections,omitempty"`
	UnmeasuredConnections  *int64 `json:"unmeasured_connections,omitempty"`
	NotJoinableConnections *int64 `json:"not_joinable_connections,omitempty"`
	CoverageUnknown        bool   `json:"coverage_unknown,omitempty"`
	// Coverage is read from a netflow Volume handed back as its figures, which carries its coverage as
	// an object; summable figures are never written with it.
	Coverage *VolumeCoverage `json:"coverage,omitempty"`
}

// summable is store figures as a script reads them.
func summable(figures store.VolumeFigures) summableFigures {
	read := summableFigures{Bytes: figures.Bytes, AllowedBytes: figures.AllowedBytes, BlockedBytes: figures.BlockedBytes,
		UnknownBytes: figures.UnknownBytes, AllowedConnections: figures.AllowedConnections,
		BlockedConnections: figures.BlockedConnections, UnknownConnections: figures.UnknownConnections}
	if !figures.Netflow {
		return read
	}
	read.Measure = MeasureNetflow
	if !figures.PacketsUnknown {
		packets := figures.Packets
		read.Packets = &packets
	}
	if figures.Coverage.Unknown {
		read.CoverageUnknown = true
		return read
	}
	coverage := figures.Coverage
	read.MeasuredConnections, read.PartialConnections = &coverage.Measured, &coverage.Partial
	read.UnmeasuredConnections, read.NotJoinableConnections = &coverage.Unmeasured, &coverage.NotJoinable
	return read
}

// toStore is summable figures as the core reads them.
func (f summableFigures) toStore() store.VolumeFigures {
	figures := store.VolumeFigures{Bytes: f.Bytes, AllowedBytes: f.AllowedBytes, BlockedBytes: f.BlockedBytes,
		UnknownBytes: f.UnknownBytes, AllowedConnections: f.AllowedConnections,
		BlockedConnections: f.BlockedConnections, UnknownConnections: f.UnknownConnections}
	if f.Measure != MeasureNetflow {
		return figures
	}
	figures.Netflow = true
	if f.Packets != nil {
		figures.Packets = *f.Packets
	} else {
		figures.PacketsUnknown = true
	}
	value := func(member *int64) int64 {
		if member == nil {
			return 0
		}
		return *member
	}
	if coverage := f.Coverage; coverage != nil && f.MeasuredConnections == nil {
		f.MeasuredConnections, f.PartialConnections = &coverage.Measured, &coverage.Partial
		f.UnmeasuredConnections, f.NotJoinableConnections = &coverage.Unmeasured, &coverage.NotJoinable
	}
	figures.Coverage = store.NetflowCoverage{Measured: value(f.MeasuredConnections),
		Partial: value(f.PartialConnections), Unmeasured: value(f.UnmeasuredConnections),
		NotJoinable: value(f.NotJoinableConnections), Unknown: f.CoverageUnknown}
	return figures
}

// figuresOf reads summable figures a script hands the host.
func (c *scriptCall) figuresOf(value goja.Value, what string) store.VolumeFigures {
	if absent(value) {
		c.throw("%s are %s, not summable figures", what, describe(value))
	}
	var figures summableFigures
	c.decodeArgument(value, what, &figures)
	return c.fromScript(figures)
}

// fromScript is figures a script hands back, as the core reads them. Under a netflow run, figures
// holding anything that are not netflow were not read by the core in that measure: the run cannot be
// served as netflow (measure.go).
func (c *scriptCall) fromScript(figures summableFigures) store.VolumeFigures {
	read := figures.toStore()
	if c.request.measure.netflow && !read.Netflow && read != (store.VolumeFigures{}) {
		c.request.measure.loggedFigure = true
	}
	return read
}

// measuredOf is the netflow members of a row a script hands back, as a row's measured figures
// (sites.go): judged as figuresOf judges figures holding bytes.
func (c *scriptCall) measuredOf(members netflowMembers, bytes int64) store.VolumeFigures {
	read := c.fromScript(summableFigures{Bytes: bytes, netflowMembers: members})
	read.Bytes = 0
	return read
}
