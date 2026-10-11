package widget

// firewall_health_overview: one tile per reading -- uptime, processor, memory, swap, disk
// per discovered filesystem, temperature per discovered sensor, latency and loss per
// gateway -- each with its recent history and its last-sampled stamp
// (docs/widget-catalogue.md, "Firewall health overview"). The tiles repeat over the
// subjects discovered at runtime; a metric no subject reported is a tile whose state is
// absent, never a 0. No threshold is assumed: a tile is coloured only by thresholds the
// reader set.

var firewallHealthOverview = Preset{
	Type: "firewall_health_overview", Shape: "figure", Windowed: true,
	Providers: []string{"measurement_sample"}, Primary: "measurement_sample",
	Params: []Param{
		{Key: "metrics", Kind: KindEnumSet, Values: healthMetrics, Default: stringsAsAny(healthMetrics), Data: true},
		periodParam(),
		{Key: "thresholds", Kind: KindObject, Data: true},
		{Key: "layout", Kind: KindEnum, Values: []string{"tiles", "rows"}, Default: "tiles"},
		{Key: "show_last_sampled", Kind: KindBoolean, Default: true, Data: true},
	},
	Serve: serveFirewallHealthOverview,
}

// Tile is one reading of one subject.
type Tile struct {
	Metric      string `json:"metric"`
	SubjectKind string `json:"subject_kind"`
	SubjectKey  string `json:"subject_key"`
	Measure     string `json:"measure"`
	Unit        string `json:"unit"`
	// State is measured, or absent where no sample of the window answers.
	State string   `json:"state"`
	Value *float64 `json:"value"`
	// LastSampledAt is the latest sample's instant, when show_last_sampled is set.
	LastSampledAt *int64  `json:"last_sampled_at,omitempty"`
	Series        []Point `json:"series"`
	// ThresholdState is ok, warning or critical against the reader's thresholds for the
	// metric, and absent without thresholds.
	ThresholdState *string `json:"threshold_state,omitempty"`
}

// HealthData is the tiles.
type HealthData struct {
	Tiles []Tile `json:"tiles"`
}

func serveFirewallHealthOverview(r *Request) (any, bool, error) {
	if err := r.coverSamples(); err != nil {
		return nil, false, err
	}
	thresholds, _ := r.params.effective["thresholds"].(map[string]any)
	data := HealthData{Tiles: []Tile{}}
	hasRows := false
	for _, metric := range r.params.set("metrics") {
		subjects, err := r.measuredSubjects(metric)
		if err != nil {
			return nil, false, err
		}
		if len(subjects) == 0 {
			data.Tiles = append(data.Tiles, Tile{Metric: metric, State: "absent", Series: []Point{},
				SubjectKind: string(metricMeasures[metric].kind)})
			continue
		}
		for _, item := range subjects {
			hasRows = true
			tile := Tile{Metric: metric, SubjectKind: item.kind, SubjectKey: item.key, Measure: item.measure,
				Unit: item.unit, State: "absent", Series: []Point{}}
			series, err := r.samplesOf(item)
			if err != nil {
				return nil, false, err
			}
			if series != nil {
				tile.Series = series
			}
			var (
				at    int64
				value float64
				unit  string
			)
			found, err := r.db.WidgetRow(r.ctx, "widget_measurement_latest", map[string]any{
				"subject_kind": item.kind, "subject_key": item.key, "measure": item.measure, "to": r.now,
			}, &at, &value, &unit)
			if err != nil {
				return nil, false, err
			}
			if found && at >= r.window.From {
				tile.State, tile.Value = "measured", &value
				if r.params.flag("show_last_sampled") {
					tile.LastSampledAt = &at
				}
				tile.ThresholdState = thresholdState(thresholds, metric, value)
			}
			data.Tiles = append(data.Tiles, tile)
		}
	}
	return data, hasRows, nil
}

// thresholdState compares a value with the reader's warning and critical thresholds for a
// metric, both optional; nil when the reader set none.
func thresholdState(thresholds map[string]any, metric string, value float64) *string {
	limits, _ := thresholds[metric].(map[string]any)
	if limits == nil {
		return nil
	}
	state := "ok"
	if warning, ok := asFloat(limits["warning"]); ok && value >= warning {
		state = "warning"
	}
	if critical, ok := asFloat(limits["critical"]); ok && value >= critical {
		state = "critical"
	}
	return &state
}

// asFloat reads a decoded JSON number.
func asFloat(value any) (float64, bool) {
	number, ok := value.(float64)
	return number, ok
}
