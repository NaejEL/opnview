package widget

import (
	"github.com/NaejEL/opnview/internal/store"
)

// aggregate_freshness: one row per pre-computed period -- slot count, earliest and latest
// period covered, last computation and bytes held -- flagged stale when it holds no slot
// or a slot's computation is behind its flows by the refresh contract's recognition rule
// (docs/widget-catalogue.md, "Aggregate freshness"). It reads the slots a widget can read:
// those of the last 31 days -- the longest rolling window a widget asks for, rounded out
// to whole slots of each period -- so every read is an index search, never a scan of a
// growing table.

var aggregateFreshness = Preset{
	Type: "aggregate_freshness", Shape: "table",
	Params: []Param{
		{Key: "periods", Kind: KindEnumSet, Values: []string{"1h", "24h", "7d", "30d"},
			Default: []any{"1h", "24h", "7d", "30d"}, Data: true},
		{Key: "show_bytes", Kind: KindBoolean, Default: true, Data: true},
		{Key: "compact", Kind: KindBoolean, Default: false},
	},
	Serve: serveAggregateFreshness,
}

// freshnessLookbackSeconds is how far back the freshness is read: the 30-day window and
// one more day, so the oldest slot a 30-day window touches is included.
const freshnessLookbackSeconds = 31 * 86400

// PeriodFreshness is one period's row.
type PeriodFreshness struct {
	Period          string `json:"period"`
	Slots           int64  `json:"slots"`
	EarliestStartAt *int64 `json:"earliest_period_start_at"`
	LatestEndAt     *int64 `json:"latest_period_end_at"`
	LastComputedAt  *int64 `json:"last_computed_at"`
	StaleSlots      int64  `json:"stale_slots"`
	Stale           bool   `json:"stale"`
	Bytes           *Count `json:"bytes,omitempty"`
	ReadFrom        int64  `json:"read_from"`
}

// FreshnessData is the table.
type FreshnessData struct {
	Periods []PeriodFreshness `json:"periods"`
}

func serveAggregateFreshness(r *Request) (any, bool, error) {
	data := FreshnessData{Periods: []PeriodFreshness{}}
	hasRows := false
	for _, period := range store.Periods() {
		if !r.params.has("periods", period.Name) {
			continue
		}
		from := period.SlotStart(r.now - freshnessLookbackSeconds)
		parameters := map[string]any{"from": from, "to": r.now + 1}
		row := PeriodFreshness{Period: period.Name, ReadFrom: from}
		var (
			earliest, latest, computed, bytes store.NullInt64
		)
		err := r.db.WidgetRows(r.ctx, "widget_period_coverage", period, parameters,
			func(scanned store.RowScanner) error {
				return scanned.Scan(&row.Slots, &earliest, &latest, &computed, &bytes)
			})
		if err != nil {
			return nil, false, err
		}
		err = r.db.WidgetRows(r.ctx, "widget_period_stale_slots", period, parameters,
			func(scanned store.RowScanner) error { return scanned.Scan(&row.StaleSlots) })
		if err != nil {
			return nil, false, err
		}
		row.EarliestStartAt, row.LatestEndAt = nullableNumber(earliest), nullableNumber(latest)
		row.LastComputedAt = nullableNumber(computed)
		row.Stale = row.Slots == 0 || row.StaleSlots > 0
		if r.params.flag("show_bytes") {
			held := loggedCount(bytes.Int64)
			row.Bytes = &held
		}
		if row.Slots > 0 {
			hasRows = true
		}
		data.Periods = append(data.Periods, row)
	}
	return data, hasRows, nil
}
