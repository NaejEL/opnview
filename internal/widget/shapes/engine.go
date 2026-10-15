package shapes

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// The declarative engine: it serves every widget whose manifest names no `script`.
//
// A declarative widget is a manifest and ONE read-only query. The engine binds the request's
// parameters to the query's named parameters, runs it on the read connections, and draws the
// rows into the payload of the widget's shape under a column contract that is the same for
// every widget of that shape (docs/widget-authoring.md, "Shape contracts"). What is common to a
// shape is therefore written once, here and in shape.go, and is not the query author's to get
// wrong:
//
//   - a `limit` parameter truncates, and the dropped members are an explicit remainder that
//     carries their count and figure, so the members shown plus the remainder are the whole;
//   - ties are broken by the member's KEY and never by its label, so an order never leaks a
//     name the aggregate mode withholds;
//   - a figure the query did not produce is absent (null) and never a zero: a time bucket with
//     no row is a gap bucket with a null value, a NULL value is counted as unmeasured, and the
//     only zeros are the ones a query measured;
//   - every figure names its measure and carries `lower_bound` as the manifest declares it;
//   - the envelope -- parameters, window, gaps, availability, conditions, withholding -- is the
//     one every widget answers with, produced by the service, not by the engine.

// Measured is one figure of a declarative widget: its value, what it measures, and whether it
// is a lower bound (opnview sees only what crosses the firewall).
type Measured struct {
	Measure    string  `json:"measure"`
	LowerBound bool    `json:"lower_bound"`
	Value      float64 `json:"value"`
}

// Rest is what a limit dropped: how many members, and their figure, so the members shown plus
// the rest make the whole.
type Rest struct {
	Members int64     `json:"members"`
	Value   *Measured `json:"value,omitempty"`
}

// engineRow is one result row.
type engineRow struct {
	cells map[string]any
	order int
}

// engine is the state of one declarative request.
type engine struct {
	r        *request.Request
	preset   request.Preset
	columns  []string
	rows     []engineRow
	limit    int
	measure  string
	hasLimit bool
}

// serveDeclarative answers a request of a declarative preset.
func ServeDeclarative(r *request.Request) (any, bool, error) {
	preset := r.Preset
	e := &engine{r: r, preset: preset, columns: preset.Columns, measure: preset.Measure}
	if e.measure == "" {
		e.measure = defaultMeasure
	}
	for _, param := range preset.Params {
		if param.Key == "limit" && param.Kind == params.KindInteger {
			e.limit = int(r.Params.Integer("limit"))
			e.hasLimit = e.limit > 0
		}
	}
	parameters, err := e.bind()
	if err != nil {
		return nil, false, err
	}
	err = r.Db.WidgetRows(r.Ctx, preset.Statement, vocab.Period{}, parameters, func(row reads.RowScanner) error {
		cells := make([]any, len(e.columns))
		pointers := make([]any, len(cells))
		for index := range cells {
			pointers[index] = &cells[index]
		}
		if err := row.Scan(pointers...); err != nil {
			return err
		}
		values := make(map[string]any, len(cells))
		for index, name := range e.columns {
			values[name] = normaliseCell(cells[index])
		}
		e.rows = append(e.rows, engineRow{cells: values, order: len(e.rows)})
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if err := e.coverWindow(); err != nil {
		return nil, false, err
	}
	return e.draw()
}

// defaultMeasure names the figures of a widget whose manifest names none.
const defaultMeasure = "query"

// normaliseCell turns a driver value into one JSON can carry as the query produced it.
func normaliseCell(value any) any {
	if raw, isBytes := value.([]byte); isBytes {
		return string(raw)
	}
	return value
}

// bind builds the named parameters of the query from the request.
func (e *engine) bind() (map[string]any, error) {
	r := e.r
	parameters := map[string]any{"now": r.Now}
	if e.preset.Windowed {
		parameters["from"] = r.Window.From
		parameters["to"] = r.Window.To
	}
	for _, param := range e.preset.Params {
		key := param.Key
		switch param.Kind {
		case params.KindEnum:
			parameters[key] = r.Params.Text(key)
		case params.KindBoolean:
			if r.Params.Flag(key) {
				parameters[key] = int64(1)
			} else {
				parameters[key] = int64(0)
			}
		case params.KindInteger:
			parameters[key] = r.Params.Integer(key)
		case params.KindNumber:
			parameters[key] = r.Params.Number(key)
		case params.KindEnumSet:
			encoded, _ := json.Marshal(r.Params.Set(key))
			parameters[key] = string(encoded)
		case params.KindStringList:
			members := r.Params.Set(key)
			if len(members) == 0 {
				parameters[key] = nil
			} else {
				encoded, _ := json.Marshal(members)
				parameters[key] = string(encoded)
			}
		case params.KindReference:
			parameters[key] = nil
			if resolved := r.Refs[key]; len(resolved) > 0 {
				parameters[key] = referenceBinding(param.Reference, resolved[0])
			}
		case params.KindReferenceList:
			parameters[key] = nil
			if resolved := r.Refs[key]; len(resolved) > 0 {
				members := make([]any, 0, len(resolved))
				for _, value := range resolved {
					members = append(members, referenceBinding(param.Reference, value))
				}
				encoded, _ := json.Marshal(members)
				parameters[key] = string(encoded)
			}
		}
	}
	return parameters, nil
}

// referenceBinding is what a resolved reference binds as: the text for the kinds that are
// named by text (a country code, a domain), the row id (the AS number for an operator) for the
// others.
func referenceBinding(kind string, value params.Resolved) any {
	if kind == "country" || kind == "site" {
		return value.Text
	}
	return value.Id
}

// draw maps the rows into the shape's payload.
func (e *engine) draw() (any, bool, error) {
	switch e.preset.Shape {
	case "figure":
		return e.drawFigure()
	case "ranked_bars":
		return e.drawRanked()
	case "time_series":
		return e.drawTimeSeries()
	case "table":
		return e.drawTable()
	case "matrix":
		return e.drawMatrix()
	case "map":
		return e.drawMap()
	case "donut":
		return e.drawDonut()
	case "tree":
		return e.drawTree()
	case "graph":
		return e.drawGraph()
	case "sankey":
		return e.drawSankey()
	case "feed":
		return e.drawFeed()
	case "cards":
		return e.drawCards()
	}
	return nil, false, fmt.Errorf("widget: the shape %q has no drawing", e.preset.Shape)
}

// measured wraps a number in the widget's measure and bound.
func (e *engine) measured(value float64) *Measured {
	return &Measured{Measure: e.measure, LowerBound: e.preset.LowerBound, Value: value}
}

// number reads a numeric cell. A NULL, a missing column and a non-numeric cell are all "no
// number", never a zero.
func number(cell any) (float64, bool) {
	switch typed := cell.(type) {
	case int64:
		return float64(typed), true
	case int:
		return float64(typed), true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return 0, false
		}
		return typed, true
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		if err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) {
			return parsed, true
		}
	}
	return 0, false
}

// text reads a text cell; NULL reads as "" and a number as its decimal text.
func text(cell any) string {
	switch typed := cell.(type) {
	case nil:
		return ""
	case string:
		return typed
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	}
	return fmt.Sprint(cell)
}

// optionalText reads a text cell that may be absent, as a pointer.
func cellText(cell any) *string {
	if cell == nil {
		return nil
	}
	value := text(cell)
	return &value
}

// optionalNumber reads a numeric cell that may be absent, as a pointer.
func optionalNumber(cell any) *float64 {
	value, ok := number(cell)
	if !ok {
		return nil
	}
	return &value
}

// member is one keyed, valued member of a ranked, donut, sankey or tree shape.
type member struct {
	key   string
	label *string
	value float64
}

// ranking splits the rows of a keyed shape into measured members and the unmeasured count.
// Two rows with one key are one member: their values add.
func (e *engine) members() (members []member, unmeasured int64) {
	index := map[string]int{}
	for _, row := range e.rows {
		key := text(row.cells["key"])
		value, ok := number(row.cells["value"])
		if !ok {
			unmeasured++
			continue
		}
		if at, seen := index[key]; seen {
			members[at].value += value
			continue
		}
		index[key] = len(members)
		members = append(members, member{key: key, label: cellText(row.cells["label"]), value: value})
	}
	return members, unmeasured
}

// sortedMembers orders members by value, largest first; equal values by key. The label is
// never consulted.
func sortedMembers(members []member) {
	sort.SliceStable(members, func(i, j int) bool {
		if members[i].value != members[j].value {
			return members[i].value > members[j].value
		}
		return members[i].key < members[j].key
	})
}

// truncation splits n ordered members at the limit: how many are shown.
func (e *engine) shown(n int) int {
	if e.hasLimit && e.limit < n {
		return e.limit
	}
	return n
}

// ---------------------------------------------------------------------------------------
// figure
// ---------------------------------------------------------------------------------------

// FigureData is the payload of the figure shape.
type FigureData struct {
	Label *string `json:"label"`
	Unit  *string `json:"unit"`
	// Figure is the number, or null when the query produced none.
	Figure *Measured `json:"figure"`
	// Previous is the figure of the preceding period when the query carries one.
	Previous *Measured `json:"previous"`
}

func (e *engine) drawFigure() (any, bool, error) {
	data := FigureData{}
	if len(e.rows) == 0 {
		return data, false, nil
	}
	row := e.rows[0].cells
	data.Label = cellText(row["label"])
	data.Unit = cellText(row["unit"])
	if value, ok := number(row["value"]); ok {
		data.Figure = e.measured(value)
	}
	if value, ok := number(row["previous"]); ok {
		data.Previous = e.measured(value)
	}
	return data, data.Figure != nil, nil
}

// ---------------------------------------------------------------------------------------
// ranked_bars
// ---------------------------------------------------------------------------------------

// Bar is one bar of a ranked_bars payload.
type Bar struct {
	Key   string   `json:"key"`
	Label *string  `json:"label"`
	Value Measured `json:"value"`
}

// RankedData is the payload of the ranked_bars shape.
type RankedData struct {
	Bars []Bar `json:"bars"`
	// Remainder is what the limit dropped; Total is every measured member, so the bars plus
	// the remainder equal it.
	Remainder Rest     `json:"remainder"`
	Total     Measured `json:"total"`
	// Unmeasured counts the rows whose value was NULL: they are in no bar and in no total.
	Unmeasured int64 `json:"unmeasured"`
}

func (e *engine) drawRanked() (any, bool, error) {
	members, unmeasured := e.members()
	sortedMembers(members)
	data := RankedData{Bars: []Bar{}, Unmeasured: unmeasured}
	var total, rest float64
	shown := e.shown(len(members))
	for index, entry := range members {
		total += entry.value
		if index < shown {
			data.Bars = append(data.Bars, Bar{Key: entry.key, Label: entry.label, Value: *e.measured(entry.value)})
			continue
		}
		data.Remainder.Members++
		rest += entry.value
	}
	data.Remainder.Value = e.measured(rest)
	data.Total = *e.measured(total)
	return data, len(members) > 0, nil
}

// ---------------------------------------------------------------------------------------
// donut
// ---------------------------------------------------------------------------------------

// Slice is one slice of a donut payload.
type DeclaredSlice struct {
	Key   string   `json:"key"`
	Label *string  `json:"label"`
	Value Measured `json:"value"`
	// Share is the slice's fraction of the total, or null when the total is zero.
	Share *float64 `json:"share"`
}

// DonutData is the payload of the donut shape.
type DonutData struct {
	Slices []DeclaredSlice `json:"slices"`
	// Other is the slice the limit folded the smallest members into.
	Other      Rest     `json:"other"`
	Total      Measured `json:"total"`
	Unmeasured int64    `json:"unmeasured"`
}

func (e *engine) drawDonut() (any, bool, error) {
	members, unmeasured := e.members()
	sortedMembers(members)
	var total float64
	for _, entry := range members {
		total += entry.value
	}
	share := func(value float64) *float64 {
		if total == 0 {
			return nil
		}
		fraction := value / total
		return &fraction
	}
	data := DonutData{Slices: []DeclaredSlice{}, Unmeasured: unmeasured, Total: *e.measured(total)}
	shown := e.shown(len(members))
	var rest float64
	for index, entry := range members {
		if index < shown {
			data.Slices = append(data.Slices, DeclaredSlice{Key: entry.key, Label: entry.label,
				Value: *e.measured(entry.value), Share: share(entry.value)})
			continue
		}
		data.Other.Members++
		rest += entry.value
	}
	data.Other.Value = e.measured(rest)
	return data, len(members) > 0, nil
}

// ---------------------------------------------------------------------------------------
// time_series
// ---------------------------------------------------------------------------------------

// DeclaredBucket is one bucket of a time series.
type DeclaredBucket struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
	// Gap says the bucket holds no row, or overlaps an interval a bound provider is known not
	// to have covered. A gap bucket with no row has no value.
	Gap bool `json:"gap"`
	// Value is the bucket's figure, null when the query produced no row for it.
	Value *Measured `json:"value"`
}

// DeclaredSeries is one line of a time series.
type DeclaredSeries struct {
	Key     string           `json:"key"`
	Buckets []DeclaredBucket `json:"buckets"`
}

// TimeSeriesPayload is the payload of the time_series shape.
type TimeSeriesPayload struct {
	BucketSeconds int64            `json:"bucket_seconds"`
	Series        []DeclaredSeries `json:"series"`
}

func (e *engine) drawTimeSeries() (any, bool, error) {
	r := e.r
	width := traffic.BucketWidth(r)
	intervals := traffic.BucketIntervals(r.Window, width)
	type cell struct {
		sum  float64
		seen bool
	}
	series := map[string]map[int64]*cell{}
	var first, last int64
	any_ := false
	for _, row := range e.rows {
		at, ok := number(row.cells["bucket"])
		if !ok {
			continue
		}
		value, ok := number(row.cells["value"])
		if !ok {
			continue
		}
		aligned := int64(at) - traffic.FloorMod(int64(at), width)
		key := text(row.cells["series"])
		if series[key] == nil {
			series[key] = map[int64]*cell{}
		}
		if series[key][aligned] == nil {
			series[key][aligned] = &cell{}
		}
		series[key][aligned].sum += value
		series[key][aligned].seen = true
		if !any_ || aligned < first {
			first = aligned
		}
		if !any_ || aligned+width > last {
			last = aligned + width
		}
		any_ = true
	}
	keys := make([]string, 0, len(series))
	for key := range series {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	data := TimeSeriesPayload{BucketSeconds: width, Series: []DeclaredSeries{}}
	for _, key := range keys {
		line := DeclaredSeries{Key: key, Buckets: []DeclaredBucket{}}
		for _, interval := range intervals {
			aligned := interval.From - traffic.FloorMod(interval.From, width)
			bucket := DeclaredBucket{From: interval.From, To: interval.To}
			if found := series[key][aligned]; found != nil && found.seen {
				bucket.Value = e.measured(found.sum)
				bucket.Gap = traffic.IsGapBucket(interval, &envelope.Interval{From: interval.From, To: interval.To}, r.Response.Gaps)
			} else {
				bucket.Gap = true
			}
			line.Buckets = append(line.Buckets, bucket)
		}
		data.Series = append(data.Series, line)
	}
	if any_ {
		covered := envelope.Interval{From: max(first, r.Window.From), To: min(last, r.Window.To)}
		r.Cover(&covered)
	} else {
		r.Cover(nil)
	}
	return data, any_, nil
}

// ---------------------------------------------------------------------------------------
// table
// ---------------------------------------------------------------------------------------

// TablePayload is the payload of the table shape.
type TablePayload struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
	// Remainder counts the rows the limit dropped.
	Remainder Rest `json:"remainder"`
}

func (e *engine) drawTable() (any, bool, error) {
	data := TablePayload{Columns: append([]string{}, e.columns...), Rows: [][]any{}}
	shown := e.shown(len(e.rows))
	for index, row := range e.rows {
		if index >= shown {
			data.Remainder.Members++
			continue
		}
		cells := make([]any, len(e.columns))
		for position, name := range e.columns {
			cells[position] = row.cells[name]
		}
		data.Rows = append(data.Rows, cells)
	}
	return data, len(e.rows) > 0, nil
}

// ---------------------------------------------------------------------------------------
// feed
// ---------------------------------------------------------------------------------------

// FeedPayload is the payload of the feed shape.
type FeedPayload struct {
	Entries   []map[string]any `json:"entries"`
	Remainder Rest             `json:"remainder"`
}

func (e *engine) drawFeed() (any, bool, error) {
	rows := append([]engineRow(nil), e.rows...)
	sort.SliceStable(rows, func(i, j int) bool {
		a, _ := number(rows[i].cells["at"])
		b, _ := number(rows[j].cells["at"])
		return a > b
	})
	data := FeedPayload{Entries: []map[string]any{}}
	shown := e.shown(len(rows))
	for index, row := range rows {
		if index >= shown {
			data.Remainder.Members++
			continue
		}
		data.Entries = append(data.Entries, row.cells)
	}
	return data, len(rows) > 0, nil
}

// ---------------------------------------------------------------------------------------
// cards
// ---------------------------------------------------------------------------------------

// Card is one card of a cards payload: its key, label and every other column of its row.
type DeclaredCard struct {
	Key    string         `json:"key"`
	Label  *string        `json:"label"`
	Fields map[string]any `json:"fields"`
}

// CardsPayload is the payload of the cards shape.
type CardsPayload struct {
	Cards     []DeclaredCard `json:"cards"`
	Remainder Rest           `json:"remainder"`
}

func (e *engine) drawCards() (any, bool, error) {
	rows := append([]engineRow(nil), e.rows...)
	sort.SliceStable(rows, func(i, j int) bool { return text(rows[i].cells["key"]) < text(rows[j].cells["key"]) })
	data := CardsPayload{Cards: []DeclaredCard{}}
	shown := e.shown(len(rows))
	for index, row := range rows {
		if index >= shown {
			data.Remainder.Members++
			continue
		}
		fields := map[string]any{}
		for name, value := range row.cells {
			if name != "key" && name != "label" {
				fields[name] = value
			}
		}
		data.Cards = append(data.Cards, DeclaredCard{Key: text(row.cells["key"]), Label: cellText(row.cells["label"]),
			Fields: fields})
	}
	return data, len(rows) > 0, nil
}

// ---------------------------------------------------------------------------------------
// matrix
// ---------------------------------------------------------------------------------------

// Axis is one row or column of a matrix.
type Axis struct {
	Key   string  `json:"key"`
	Label *string `json:"label"`
}

// DeclaredCell is one measured cell of a matrix. A cell the query did not produce is absent
// from the list, never a zero.
type DeclaredCell struct {
	Row    string   `json:"row"`
	Column string   `json:"column"`
	Value  Measured `json:"value"`
}

// MatrixPayload is the payload of the matrix shape.
type MatrixPayload struct {
	Rows       []Axis         `json:"rows"`
	Columns    []Axis         `json:"columns"`
	Cells      []DeclaredCell `json:"cells"`
	Unmeasured int64          `json:"unmeasured"`
}

func (e *engine) drawMatrix() (any, bool, error) {
	data := MatrixPayload{Rows: []Axis{}, Columns: []Axis{}, Cells: []DeclaredCell{}}
	rowLabels, columnLabels := map[string]*string{}, map[string]*string{}
	type coordinate struct{ row, column string }
	sums := map[coordinate]float64{}
	for _, row := range e.rows {
		rowKey, columnKey := text(row.cells["row"]), text(row.cells["column"])
		if _, seen := rowLabels[rowKey]; !seen || rowLabels[rowKey] == nil {
			rowLabels[rowKey] = cellText(row.cells["row_label"])
		}
		if _, seen := columnLabels[columnKey]; !seen || columnLabels[columnKey] == nil {
			columnLabels[columnKey] = cellText(row.cells["column_label"])
		}
		value, ok := number(row.cells["value"])
		if !ok {
			data.Unmeasured++
			continue
		}
		sums[coordinate{rowKey, columnKey}] += value
	}
	for key, label := range rowLabels {
		data.Rows = append(data.Rows, Axis{Key: key, Label: label})
	}
	for key, label := range columnLabels {
		data.Columns = append(data.Columns, Axis{Key: key, Label: label})
	}
	sort.Slice(data.Rows, func(i, j int) bool { return data.Rows[i].Key < data.Rows[j].Key })
	sort.Slice(data.Columns, func(i, j int) bool { return data.Columns[i].Key < data.Columns[j].Key })
	for _, rowAxis := range data.Rows {
		for _, columnAxis := range data.Columns {
			if value, present := sums[coordinate{rowAxis.Key, columnAxis.Key}]; present {
				data.Cells = append(data.Cells, DeclaredCell{Row: rowAxis.Key, Column: columnAxis.Key, Value: *e.measured(value)})
			}
		}
	}
	return data, len(e.rows) > 0, nil
}

// ---------------------------------------------------------------------------------------
// map
// ---------------------------------------------------------------------------------------

// DeclaredPoint is one country of a map payload.
type DeclaredPoint struct {
	Country   string   `json:"country"`
	Label     *string  `json:"label"`
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
	Value     Measured `json:"value"`
}

// MapPayload is the payload of the map shape.
type MapPayload struct {
	Points []DeclaredPoint `json:"points"`
	// Unplaced is the rows with no country: counted, with their figure, never dropped.
	Unplaced Rest `json:"unplaced"`
	// Remainder is what the limit dropped from the placed points.
	Remainder  Rest  `json:"remainder"`
	Unmeasured int64 `json:"unmeasured"`
}

func (e *engine) drawMap() (any, bool, error) {
	data := MapPayload{Points: []DeclaredPoint{}}
	var placed []DeclaredPoint
	index := map[string]int{}
	var unplaced float64
	for _, row := range e.rows {
		value, ok := number(row.cells["value"])
		if !ok {
			data.Unmeasured++
			continue
		}
		country := text(row.cells["country"])
		if country == "" {
			data.Unplaced.Members++
			unplaced += value
			continue
		}
		if at, seen := index[country]; seen {
			placed[at].Value.Value += value
			continue
		}
		index[country] = len(placed)
		placed = append(placed, DeclaredPoint{Country: country, Label: cellText(row.cells["label"]),
			Latitude: optionalNumber(row.cells["latitude"]), Longitude: optionalNumber(row.cells["longitude"]),
			Value: *e.measured(value)})
	}
	data.Unplaced.Value = e.measured(unplaced)
	sort.SliceStable(placed, func(i, j int) bool {
		if placed[i].Value.Value != placed[j].Value.Value {
			return placed[i].Value.Value > placed[j].Value.Value
		}
		return placed[i].Country < placed[j].Country
	})
	shown := e.shown(len(placed))
	var rest float64
	for at, point := range placed {
		if at < shown {
			data.Points = append(data.Points, point)
			continue
		}
		data.Remainder.Members++
		rest += point.Value.Value
	}
	data.Remainder.Value = e.measured(rest)
	return data, len(placed) > 0 || data.Unplaced.Members > 0, nil
}

// ---------------------------------------------------------------------------------------
// tree
// ---------------------------------------------------------------------------------------

// DeclaredNode is one node of a tree payload.
type DeclaredNode struct {
	ID       string          `json:"id"`
	Label    *string         `json:"label"`
	Value    *Measured       `json:"value"`
	Children []*DeclaredNode `json:"children"`
	// Remainder is what the limit dropped from this node's children.
	Remainder Rest `json:"remainder"`
}

// TreePayload is the payload of the tree shape.
type TreePayload struct {
	Roots     []*DeclaredNode `json:"roots"`
	Remainder Rest            `json:"remainder"`
	// Orphans counts the nodes whose parent the query did not return: they are drawn as roots.
	Orphans int64 `json:"orphans"`
	// Cyclic counts the nodes unreachable from any root because their parents form a cycle.
	Cyclic int64 `json:"cyclic"`
}

func (e *engine) drawTree() (any, bool, error) {
	nodes := map[string]*DeclaredNode{}
	parents := map[string]string{}
	hasParent := map[string]bool{}
	var order []string
	for _, row := range e.rows {
		id := text(row.cells["id"])
		node := nodes[id]
		if node == nil {
			node = &DeclaredNode{ID: id, Children: []*DeclaredNode{}}
			nodes[id] = node
			order = append(order, id)
		}
		if label := cellText(row.cells["label"]); label != nil {
			node.Label = label
		}
		if value, ok := number(row.cells["value"]); ok {
			if node.Value == nil {
				node.Value = e.measured(0)
			}
			node.Value.Value += value
		}
		if parent := row.cells["parent"]; parent != nil && text(parent) != "" {
			parents[id] = text(parent)
			hasParent[id] = true
		}
	}
	data := TreePayload{Roots: []*DeclaredNode{}}
	attached := map[string]bool{}
	var roots []*DeclaredNode
	for _, id := range order {
		parent, has := parents[id]
		switch {
		case !has:
			roots = append(roots, nodes[id])
			attached[id] = true
		case nodes[parent] == nil:
			roots = append(roots, nodes[id])
			attached[id] = true
			data.Orphans++
		default:
			nodes[parent].Children = append(nodes[parent].Children, nodes[id])
		}
	}
	// A node is reachable when walking its parents ends at a root.
	for _, id := range order {
		seen := map[string]bool{}
		current := id
		for !attached[current] {
			if seen[current] {
				data.Cyclic++
				break
			}
			seen[current] = true
			current = parents[current]
		}
	}
	var finish func(list []*DeclaredNode, depth int) []*DeclaredNode
	finish = func(list []*DeclaredNode, depth int) []*DeclaredNode {
		sort.SliceStable(list, func(i, j int) bool {
			a, b := 0.0, 0.0
			if list[i].Value != nil {
				a = list[i].Value.Value
			}
			if list[j].Value != nil {
				b = list[j].Value.Value
			}
			if a != b {
				return a > b
			}
			return list[i].ID < list[j].ID
		})
		return list
	}
	var visit func(node *DeclaredNode, depth int)
	visit = func(node *DeclaredNode, depth int) {
		if depth > len(nodes) {
			return
		}
		node.Children = finish(node.Children, depth)
		shown := e.shown(len(node.Children))
		var rest float64
		for at := shown; at < len(node.Children); at++ {
			node.Remainder.Members++
			if node.Children[at].Value != nil {
				rest += node.Children[at].Value.Value
			}
		}
		node.Remainder.Value = e.measured(rest)
		node.Children = node.Children[:shown]
		for _, child := range node.Children {
			visit(child, depth+1)
		}
	}
	roots = finish(roots, 0)
	shown := e.shown(len(roots))
	var rest float64
	for at := shown; at < len(roots); at++ {
		data.Remainder.Members++
		if roots[at].Value != nil {
			rest += roots[at].Value.Value
		}
	}
	data.Remainder.Value = e.measured(rest)
	data.Roots = roots[:shown]
	for _, root := range data.Roots {
		visit(root, 0)
	}
	return data, len(nodes) > 0, nil
}

// ---------------------------------------------------------------------------------------
// sankey
// ---------------------------------------------------------------------------------------

// DeclaredRibbon is one flow of a sankey payload.
type DeclaredRibbon struct {
	Source string   `json:"source"`
	Target string   `json:"target"`
	Value  Measured `json:"value"`
}

// SankeyPayload is the payload of the sankey shape.
type SankeyPayload struct {
	Nodes   []Axis           `json:"nodes"`
	Ribbons []DeclaredRibbon `json:"ribbons"`
	// Other is the band the limit folded the smallest ribbons into.
	Other      Rest     `json:"other"`
	Total      Measured `json:"total"`
	Unmeasured int64    `json:"unmeasured"`
}

func (e *engine) drawSankey() (any, bool, error) {
	data := SankeyPayload{Nodes: []Axis{}, Ribbons: []DeclaredRibbon{}}
	type edge struct{ source, target string }
	sums := map[edge]float64{}
	labels := map[string]*string{}
	var order []edge
	for _, row := range e.rows {
		source, target := text(row.cells["source"]), text(row.cells["target"])
		for key, label := range map[string]any{source: row.cells["source_label"], target: row.cells["target_label"]} {
			if labels[key] == nil {
				labels[key] = cellText(label)
			}
		}
		value, ok := number(row.cells["value"])
		if !ok {
			data.Unmeasured++
			continue
		}
		key := edge{source, target}
		if _, seen := sums[key]; !seen {
			order = append(order, key)
		}
		sums[key] += value
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := sums[order[i]], sums[order[j]]
		if a != b {
			return a > b
		}
		if order[i].source != order[j].source {
			return order[i].source < order[j].source
		}
		return order[i].target < order[j].target
	})
	shown := e.shown(len(order))
	var total, rest float64
	used := map[string]bool{}
	for at, key := range order {
		total += sums[key]
		if at >= shown {
			data.Other.Members++
			rest += sums[key]
			continue
		}
		used[key.source], used[key.target] = true, true
		data.Ribbons = append(data.Ribbons, DeclaredRibbon{Source: key.source, Target: key.target, Value: *e.measured(sums[key])})
	}
	data.Other.Value = e.measured(rest)
	data.Total = *e.measured(total)
	for key := range used {
		data.Nodes = append(data.Nodes, Axis{Key: key, Label: labels[key]})
	}
	sort.Slice(data.Nodes, func(i, j int) bool { return data.Nodes[i].Key < data.Nodes[j].Key })
	return data, len(order) > 0, nil
}

// ---------------------------------------------------------------------------------------
// graph
// ---------------------------------------------------------------------------------------

// DeclaredEdge is one link of a graph payload.
type DeclaredEdge struct {
	Source string    `json:"source"`
	Target string    `json:"target"`
	Value  *Measured `json:"value"`
}

// GraphPayload is the payload of the graph shape.
type GraphPayload struct {
	Nodes     []Axis         `json:"nodes"`
	Edges     []DeclaredEdge `json:"edges"`
	Remainder Rest           `json:"remainder"`
}

func (e *engine) drawGraph() (any, bool, error) {
	data := GraphPayload{Nodes: []Axis{}, Edges: []DeclaredEdge{}}
	labels := map[string]*string{}
	shown := e.shown(len(e.rows))
	used := map[string]bool{}
	for at, row := range e.rows {
		source, target := text(row.cells["source"]), text(row.cells["target"])
		for key, label := range map[string]any{source: row.cells["source_label"], target: row.cells["target_label"]} {
			if labels[key] == nil {
				labels[key] = cellText(label)
			}
		}
		if at >= shown {
			data.Remainder.Members++
			continue
		}
		used[source], used[target] = true, true
		edge := DeclaredEdge{Source: source, Target: target}
		if value, ok := number(row.cells["value"]); ok {
			edge.Value = e.measured(value)
		}
		data.Edges = append(data.Edges, edge)
	}
	for key := range used {
		data.Nodes = append(data.Nodes, Axis{Key: key, Label: labels[key]})
	}
	sort.Slice(data.Nodes, func(i, j int) bool { return data.Nodes[i].Key < data.Nodes[j].Key })
	return data, len(e.rows) > 0, nil
}

// shapeColumns are the columns each shape's contract requires of a query; the loader refuses
// a query that lacks one (RefusalMissingColumn).
var ShapeColumns = map[string][]string{
	"figure":      {"value"},
	"ranked_bars": {"key", "value"},
	"time_series": {"bucket", "value"},
	"table":       {},
	"matrix":      {"row", "column", "value"},
	"map":         {"country", "value"},
	"donut":       {"key", "value"},
	"tree":        {"id", "parent", "value"},
	"graph":       {"source", "target"},
	"sankey":      {"source", "target", "value"},
	"feed":        {"at"},
	"cards":       {"key"},
}

// coverageStatements are the statements that say where a provider kind's records begin, for the
// kinds opnview stores records of. They are what the built-ins read to report the covered part of
// a window, so a declarative widget over the same kind reports it the same way.
var coverageStatements = map[string]string{
	"firewall_log":       "widget_earliest_flow",
	"dns_lookup":         "widget_earliest_lookup",
	"security_event":     "widget_earliest_security_event",
	"measurement_sample": "widget_earliest_sample",
}

// coverWindow reports the covered part of a windowed declarative widget's window from where its
// primary provider's records begin (or, with no primary, the first bound kind that has records).
// A widget bound to no such kind reports no coverage, which is the honest value: it is not
// established.
func (e *engine) coverWindow() error {
	if !e.preset.Windowed || e.preset.Shape == "time_series" {
		return nil
	}
	kinds := append([]string{e.preset.Primary}, e.preset.Providers...)
	for _, kind := range kinds {
		statement, known := coverageStatements[kind]
		if !known {
			continue
		}
		earliest, has, err := e.r.Earliest(statement)
		if err != nil {
			return err
		}
		e.r.CoverFrom(earliest, has)
		return nil
	}
	return nil
}
