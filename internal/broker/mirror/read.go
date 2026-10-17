package mirror

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/broker/answer"
)

// Serving an answer from the mirror (A11): the datum's last answer rebuilt from its rows -- or,
// for a record stream read after a cursor, its stored rows past the cursor -- with each row
// stating which fields it holds, so a reader asking for one it does not hold is answered from a
// firewall call instead (A10, "No missing field because of the mirror").

// Selection chooses what a load serves.
type Selection struct {
	// After, when set, selects a record stream's rows whose cursor lies past it, in the order the
	// answers list them, in place of the last answer's rows.
	After *float64
	// Before, when set with After, bounds the rows to a window's end.
	Before *float64
	// Limit, when positive, keeps at most that many of the rows: a request with a smaller page
	// size than the answer stored is served its first rows, as the firewall would answer it.
	Limit int
}

// holding is what one stored row, and everything below it, holds.
type holding struct {
	generation int64
	stored     map[string]int64
	whole      map[string]int64
}

func (h holding) Field(path string) bool {
	if since, held := h.stored[path]; held && h.generation >= since {
		return true
	}
	for prefix, since := range h.whole {
		if h.generation >= since && answer.Under(path, prefix) {
			return true
		}
	}
	if strings.HasSuffix(path, answer.AnyItem) || strings.HasSuffix(path, answer.AnyMember) {
		// A membership is held where something below it is.
		for stored, since := range h.stored {
			if h.generation >= since && answer.Under(stored, path) {
				return true
			}
		}
	}
	return false
}

func (h holding) Whole(path string) bool {
	for prefix, since := range h.whole {
		if h.generation >= since && answer.Under(path, prefix) {
			return true
		}
	}
	return false
}

// HoldingOf is what a datum's last answer holds at its root.
func HoldingOf(datum *Datum) answer.Holding {
	return holding{generation: datum.Generation, stored: datum.storedPaths, whole: datum.wholePaths}
}

// storedTable is one table of an endpoint, with its row set and columns.
type storedTable struct {
	info   *tableInfo
	parent string
}

// tablesOf reads the tables of an endpoint.
func (m *Mirror) tablesOf(ctx context.Context, endpointID int64) (map[string]*storedTable, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT name, row_set, coalesce(parent_row_set, '') FROM opnsense.mirror_table
		WHERE endpoint_id = $1`, endpointID)
	if err != nil {
		return nil, err
	}
	type row struct{ name, rowSet, parent string }
	var listed []row
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.name, &item.rowSet, &item.parent); err != nil {
			_ = rows.Close()
			return nil, err
		}
		listed = append(listed, item)
	}
	_ = rows.Close()
	tables := map[string]*storedTable{}
	for _, item := range listed {
		info, err := m.loadTable(ctx, item.name)
		if err != nil || info == nil {
			return nil, err
		}
		if err := m.bringUpToDate(ctx, item.name); err != nil {
			return nil, err
		}
		tables[item.rowSet] = &storedTable{info: info, parent: item.parent}
	}
	return tables, nil
}

// loadedRow is one row read back.
type loadedRow struct {
	id         int64
	parent     int64
	key        string
	generation int64
	value      *answer.Value
}

// Load rebuilds a datum's answer from its rows. A datum read whole is served its stored body.
func (m *Mirror) Load(ctx context.Context, datum *Datum, selection Selection) (*answer.Value, error) {
	if datum.Generation == 0 {
		return nil, ErrNotMirrored
	}
	if datum.ReadWhole && datum.Body != "" && selection.After == nil {
		value, err := answer.Parse([]byte(datum.Body))
		if err != nil {
			return nil, err
		}
		limitRows(value, datum.RowSet, selection.Limit)
		return value, nil
	}
	tables, err := m.tablesOf(ctx, datum.EndpointID)
	if err != nil {
		return nil, err
	}
	rootHolding := HoldingOf(datum)
	var root *answer.Value
	parents := map[string][]*loadedRow{}
	if top, present := tables[""]; present && datum.RootKind != answer.Array {
		loaded, err := m.loadRows(ctx, datum, top.info, "", nil, selection, false)
		if err != nil {
			return nil, err
		}
		if len(loaded) > 0 {
			root = loaded[0].value
			parents[""] = loaded
		}
	}
	if root == nil {
		switch datum.RootKind {
		case answer.Array:
			root = answer.NewArray()
		case answer.Object:
			root = answer.NewObject()
		default:
			root = answer.NewNull()
		}
	}
	root.Holding = rootHolding

	// The row sets in order of depth, so a row set's parents are loaded before it.
	rowSets := make([]string, 0, len(tables))
	for rowSet := range tables {
		if rowSet != "" {
			rowSets = append(rowSets, rowSet)
		}
	}
	sort.Slice(rowSets, func(i, j int) bool {
		di, dj := len(answer.Steps(rowSets[i])), len(answer.Steps(rowSets[j]))
		if di != dj {
			return di < dj
		}
		return rowSets[i] < rowSets[j]
	})
	for _, rowSet := range rowSets {
		table := tables[rowSet]
		parentRows := parents[table.parent]
		main := rowSet == datum.RowSet
		var parentIDs []int64
		if !main || table.parent != "" || datum.RootKind != answer.Array {
			if len(parentRows) == 0 && !(datum.RootKind == answer.Array && table.parent == "") {
				continue
			}
			for _, parent := range parentRows {
				parentIDs = append(parentIDs, parent.id)
			}
		}
		if datum.RootKind == answer.Array && table.parent == "" {
			parentIDs = []int64{0}
		}
		loaded, err := m.loadRows(ctx, datum, table.info, rowSet, parentIDs, selection, main)
		if err != nil {
			return nil, err
		}
		parents[rowSet] = loaded
		attach(root, table.parent, rowSet, parentRows, loaded, datum.RootKind)
	}
	return root, nil
}

// limitRows keeps the first rows of an answer's main row set.
func limitRows(value *answer.Value, rowSet string, limit int) {
	if limit <= 0 || !strings.HasSuffix(rowSet, answer.AnyItem) {
		return
	}
	container := value
	steps := answer.Steps(rowSet)
	for _, step := range steps[:len(steps)-1] {
		if step == answer.AnyItem || step == answer.AnyMember {
			return
		}
		container = container.Field(step)
	}
	if container != nil && container.Kind == answer.Array && len(container.Items) > limit {
		container.Items = container.Items[:limit]
	}
}

// loadRows reads the rows of a table: the root row, the main row set's rows of the last answer
// (or past a cursor), or a nested row set's rows under their parents.
func (m *Mirror) loadRows(ctx context.Context, datum *Datum, table *tableInfo, rowSet string, parents []int64,
	selection Selection, main bool) ([]*loadedRow, error) {
	columns := make([]*columnInfo, 0, len(table.columns))
	for _, column := range table.columns {
		columns = append(columns, column)
	}
	sort.Slice(columns, func(i, j int) bool { return columns[i].path < columns[j].path })
	selected := []string{`"#id"`, `"#parent"`, `"#key"`, `"#generation"`, `CAST("#nulls" AS text)`,
		`CAST("#lexical" AS text)`, `CAST("#shape" AS text)`}
	for _, column := range columns {
		selected = append(selected, "CAST("+quote(column.name)+" AS text)")
	}
	query := "SELECT " + strings.Join(selected, ", ") + " FROM " + qualified(table.name) + ` WHERE "#datum" = $1`
	arguments := []any{datum.ID}
	limit := ""
	switch {
	case rowSet == "":
		query += ` AND "#parent" = 0 AND "#key" = ''`
	case main && selection.After != nil:
		query += ` AND "#cursor" > $2`
		arguments = append(arguments, *selection.After)
		if selection.Before != nil {
			query += ` AND "#cursor" <= $3`
			arguments = append(arguments, *selection.Before)
		}
		if datum.CursorDescending {
			query += ` ORDER BY "#cursor" DESC, "#id" DESC`
		} else {
			query += ` ORDER BY "#cursor", "#id"`
		}
	case main:
		// The rows the datum's newest answer holds: those it wrote, and those it held unchanged.
		query += ` AND "#held" AND "#parent" = ANY(CAST($2 AS bigint[])) ORDER BY "#ordinal"`
		arguments = append(arguments, int64ArrayLiteral(parents))
	default:
		query += ` AND "#parent" = ANY(CAST($2 AS bigint[])) ORDER BY "#parent", "#ordinal"`
		arguments = append(arguments, int64ArrayLiteral(parents))
	}
	if main && selection.Limit > 0 {
		limit = " LIMIT " + strconv.Itoa(selection.Limit)
	}
	rows, err := m.db.QueryContext(ctx, query+limit, arguments...)
	if err != nil {
		return nil, fmt.Errorf("mirror: reading %s: %w", table.name, err)
	}
	defer func() { _ = rows.Close() }()
	var loaded []*loadedRow
	for rows.Next() {
		row := &loadedRow{}
		var nulls, lexical, shape *string
		cells := make([]*string, len(columns))
		targets := []any{&row.id, &row.parent, &row.key, &row.generation, &nulls, &lexical, &shape}
		for index := range cells {
			targets = append(targets, &cells[index])
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		row.value = rebuild(columns, cells, nulls, lexical, shape)
		row.value.Holding = holding{generation: row.generation, stored: datum.storedPaths, whole: datum.wholePaths}
		loaded = append(loaded, row)
	}
	return loaded, rows.Err()
}

// rebuild turns one stored row into the value it was cut from.
func rebuild(columns []*columnInfo, cells []*string, nulls, lexical, shape *string) *answer.Value {
	values := map[string]*answer.Value{}
	for index, column := range columns {
		if cells[index] == nil {
			continue
		}
		stored := *cells[index]
		switch sqlType(column.kind) {
		case "numeric":
			values[column.path] = answer.NewNumber(stored)
		case "boolean":
			values[column.path] = &answer.Value{Kind: answer.Bool, Text: booleanText(stored)}
		case "jsonb":
			if parsed, err := answer.Parse([]byte(stored)); err == nil {
				values[column.path] = parsed
			}
		default:
			values[column.path] = answer.NewString(stored)
		}
	}
	if nulls != nil {
		var list []string
		_ = json.Unmarshal([]byte(*nulls), &list)
		for _, path := range list {
			values[path] = answer.NewNull()
		}
	}
	if lexical != nil {
		var exact map[string]string
		_ = json.Unmarshal([]byte(*lexical), &exact)
		for path, raw := range exact {
			if parsed, err := answer.Parse([]byte(raw)); err == nil {
				values[path] = parsed
			} else {
				values[path] = answer.NewNumber(raw)
			}
		}
	}
	if scalar, isScalar := values[""]; isScalar {
		return scalar
	}
	object := answer.NewObject()
	var containers map[string]string
	if shape != nil {
		_ = json.Unmarshal([]byte(*shape), &containers)
	}
	paths := make([]string, 0, len(containers))
	for path := range containers {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool { return len(answer.Steps(paths[i])) < len(answer.Steps(paths[j])) })
	for _, path := range paths {
		container := answer.NewObject()
		if containers[path] == "array" {
			container = answer.NewArray()
		}
		place(object, answer.Steps(path), container)
	}
	keys := make([]string, 0, len(values))
	for path := range values {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	for _, path := range keys {
		place(object, answer.Steps(path), values[path])
	}
	return object
}

// place sets a value at a path of field names below an object, creating the objects between.
func place(object *answer.Value, steps []string, value *answer.Value) {
	current := object
	for index, step := range steps {
		if index == len(steps)-1 {
			if existing := current.Field(step); existing != nil && !existing.Scalar() && !value.Scalar() &&
				existing.Kind == value.Kind && len(existing.Keys)+len(existing.Items) > 0 {
				return
			}
			current.Set(step, value)
			return
		}
		next := current.Field(step)
		if next == nil || next.Kind != answer.Object {
			next = answer.NewObject()
			current.Set(step, next)
		}
		current = next
	}
}

// attach hangs a row set's rows under their parents: the items of an array, or the members of a
// value-keyed object keyed by their key.
func attach(root *answer.Value, parentRowSet, rowSet string, parents []*loadedRow, rows []*loadedRow,
	rootKind answer.Kind) {
	steps := answer.Steps(rowSet)
	last := steps[len(steps)-1]
	inner := answer.Steps(strings.TrimPrefix(rowSet, parentRowSet))
	if parentRowSet == "" {
		inner = steps
	}
	containerPath := inner[:len(inner)-1]
	if len(containerPath) > 0 && (containerPath[0] == "") {
		containerPath = containerPath[1:]
	}
	byParent := map[int64][]*loadedRow{}
	for _, row := range rows {
		byParent[row.parent] = append(byParent[row.parent], row)
	}
	fill := func(holder *answer.Value, children []*loadedRow) {
		if len(children) == 0 {
			// An empty container is in the row's shape, and an absent one is not: neither is
			// created here.
			return
		}
		var container *answer.Value
		if len(containerPath) == 0 {
			container = holder
		} else {
			container = holder
			for index, step := range containerPath {
				next := container.Field(step)
				if next == nil {
					if index == len(containerPath)-1 {
						if last == answer.AnyItem {
							next = answer.NewArray()
						} else {
							next = answer.NewObject()
						}
					} else {
						next = answer.NewObject()
					}
					container.Set(step, next)
				}
				container = next
			}
		}
		if last == answer.AnyItem {
			container.Kind = answer.Array
			for _, child := range children {
				container.Items = append(container.Items, child.value)
			}
			return
		}
		container.Kind = answer.Object
		container.ValueKeyed = true
		for _, child := range children {
			container.Set(child.key, child.value)
		}
	}
	if parentRowSet == "" && rootKind == answer.Array {
		fill(root, byParent[0])
		return
	}
	if parentRowSet == "" && len(parents) == 0 {
		fill(root, byParent[0])
		return
	}
	for _, parent := range parents {
		fill(parent.value, byParent[parent.id])
	}
}
