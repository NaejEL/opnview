package widget

import (
	"fmt"
)

// interface_throughput: per interface, bandwidth in and out over time, and the packet and
// error counters, from the interface's OWN counters (docs/widget-catalogue.md, "Interface
// throughput"). They include traffic no logging rule matched, which the filter log never
// recorded, so their measure is interface_counter and never logged; traffic between two
// clients behind one interface still never reaches them. Interfaces are named by the
// user's label or the discovered description, never by the device alone.

var interfaceThroughput = Preset{
	Type: "interface_throughput", Shape: "time_series", Windowed: true,
	Providers: []string{"measurement_sample"}, Primary: "measurement_sample",
	Params: []Param{
		interfacesParam(),
		periodParam(),
		{Key: "direction", Kind: KindEnum, Values: []string{"both", "in", "out"}, Default: "both", Data: true},
		{Key: "measure", Kind: KindEnum, Values: []string{"bits_per_second", "bytes", "packets", "errors"},
			Default: "bits_per_second", Data: true},
		{Key: "stacked", Kind: KindBoolean, Default: false},
		{Key: "per_interface_axis", Kind: KindBoolean, Default: false},
	},
	Serve: serveInterfaceThroughput,
}

// ThroughputSeries is one interface's one direction, at the resolution its samples have.
type ThroughputSeries struct {
	Key       string        `json:"key"`
	Interface InterfaceInfo `json:"interface"`
	Direction string        `json:"direction"`
	Measure   string        `json:"measure"`
	Unit      string        `json:"unit"`
	// LowerBound is true: intra-interface traffic never reaches the counters either.
	LowerBound bool    `json:"lower_bound"`
	Points     []Point `json:"points"`
	// Resets counts the counter resets in the window, each a hole and never a rate.
	Resets int `json:"resets"`
	// Current is the latest point, brought forward.
	Current *Point `json:"current"`
}

// ThroughputData is the chart.
type ThroughputData struct {
	Series []ThroughputSeries `json:"series"`
}

func serveInterfaceThroughput(r *Request) (any, bool, error) {
	if err := r.coverSamples(); err != nil {
		return nil, false, err
	}
	cache, err := r.install()
	if err != nil {
		return nil, false, err
	}
	selected := r.idSet("interfaces")
	measure := r.params.text("measure")
	mapping := throughputMeasures[measure]
	unit := map[string]string{"bits_per_second": "bits_per_second", "bytes": "bytes", "packets": "packets",
		"errors": "errors"}[measure]
	data := ThroughputData{Series: []ThroughputSeries{}}
	hasRows := false
	for _, info := range cache.interfaces {
		if selected != nil && !selected[info.ID] {
			continue
		}
		for _, direction := range []string{"in", "out"} {
			if wanted := r.params.text("direction"); wanted != "both" && wanted != direction {
				continue
			}
			counter := mapping.in
			if direction == "out" {
				counter = mapping.out
			}
			points, resets, err := r.throughputPoints(info.Device, counter, mapping.perSecond, mapping.factor)
			if err != nil {
				return nil, false, err
			}
			series := ThroughputSeries{Key: fmt.Sprintf("%s:%s", interfaceKey(info.ID), direction),
				Interface: info, Direction: direction, Measure: MeasureInterfaceCounter, Unit: unit,
				LowerBound: true, Points: []Point{}, Resets: resets}
			if len(points) > 0 {
				hasRows = true
				series.Points = points
				latest := points[len(points)-1]
				series.Current = &latest
			}
			data.Series = append(data.Series, series)
		}
	}
	return data, hasRows, nil
}
