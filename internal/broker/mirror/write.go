package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/broker/infer"
	"github.com/NaejEL/opnview/internal/decode"
)

// Fetch is one answer the firewall gave OK.
type Fetch struct {
	At         int64
	StatusCode int
	// PageSize is the page size the call asked for, 0 for none.
	PageSize int64
	// Body is the answer's body, kept while a reader reads the datum whole.
	Body       []byte
	Normaliser decode.Normaliser
}

// StoreResult is what a write found.
type StoreResult struct {
	// Gap is the interval a record stream's answer did not reach back to: its oldest row is newer
	// than the newest row stored.
	Gap *Gap
	// Changed says the stored content changed.
	Changed bool
}

// paths is the stored set of a datum: the fields and the paths read whole.
type paths struct {
	fields map[string]bool
	whole  []string
}

func (p paths) leaf(path string) bool {
	return p.fields[path] || answer.Covers(nil, p.whole, path)
}

// under reports whether anything at or below a path is stored.
func (p paths) under(prefix string) bool {
	for field := range p.fields {
		if answer.Under(field, prefix) {
			return true
		}
	}
	for _, whole := range p.whole {
		if answer.Under(whole, prefix) || answer.Under(prefix, whole) {
			return true
		}
	}
	return false
}

// pendingRow is one row about to be written.
type pendingRow struct {
	parent  *pendingRow
	key     string
	ordinal int
	values  map[string]*answer.Value
	shape   map[string]string
	cursor  *float64
	id      int64
	source  *answer.Value
	rowSet  string
	isMain  bool
	written bool
}

// rowSetRows are the rows of one row set.
type rowSetRows struct {
	rowSet       string
	parentRowSet string
	rows         []*pendingRow
}

// decomposition is an answer cut into row sets.
type decomposition struct {
	order  []string
	sets   map[string]*rowSetRows
	stored paths
}

func (d *decomposition) add(rowSet, parentRowSet string, row *pendingRow) {
	set, found := d.sets[rowSet]
	if !found {
		set = &rowSetRows{rowSet: rowSet, parentRowSet: parentRowSet}
		d.sets[rowSet] = set
		d.order = append(d.order, rowSet)
	}
	row.rowSet = rowSet
	set.rows = append(set.rows, row)
}

// relative joins a step to a path relative to a row set.
func relative(base, step string) string {
	if base == "" {
		return answer.Join("", step)
	}
	return answer.Join(base, step)
}

// decompose cuts an answer into the rows of its row sets, keeping the stored fields only.
func decompose(value *answer.Value, stored paths, shape infer.Shape) *decomposition {
	d := &decomposition{sets: map[string]*rowSetRows{}, stored: stored}
	if value == nil {
		return d
	}
	var walk func(node *answer.Value, abs, rowSet string, row *pendingRow, rel string)
	child := func(node *answer.Value, abs, rowSet string, parent *pendingRow, key string, ordinal int) {
		item := &pendingRow{parent: parent, key: key, ordinal: ordinal, values: map[string]*answer.Value{},
			shape: map[string]string{}, source: node, isMain: abs == shape.RowSet}
		d.add(abs, rowSet, item)
		if node != nil && node.Scalar() {
			if stored.leaf(abs) {
				item.values[""] = node
			}
			return
		}
		walk(node, abs, abs, item, "")
	}
	walk = func(node *answer.Value, abs, rowSet string, row *pendingRow, rel string) {
		if node == nil {
			return
		}
		if node.Scalar() {
			if stored.leaf(abs) {
				row.values[rel] = node
			}
			return
		}
		if stored.fields[abs] && rel != "" {
			// A path read as a scalar holds a container now: the column keeps it whole.
			row.values[rel] = node
			return
		}
		switch node.Kind {
		case answer.Object:
			if node.ValueKeyed {
				members := answer.Join(abs, answer.AnyMember)
				if !stored.under(members) {
					return
				}
				row.shape[rel] = "object"
				for index, key := range node.Keys {
					child(node.Fields[key], members, rowSet, row, key, index)
				}
				return
			}
			if rel != "" && stored.under(abs) {
				row.shape[rel] = "object"
			}
			for _, key := range node.Keys {
				walk(node.Fields[key], answer.Join(abs, key), rowSet, row, relative(rel, key))
			}
		case answer.Array:
			items := answer.Join(abs, answer.AnyItem)
			if !stored.under(items) {
				return
			}
			row.shape[rel] = "array"
			for index, item := range node.Items {
				child(item, items, rowSet, row, strconv.Itoa(index), index)
			}
		}
	}
	if value.Kind == answer.Array {
		items := answer.AnyItem
		if stored.under(items) {
			for index, item := range value.Items {
				child(item, items, "", nil, strconv.Itoa(index), index)
			}
			if _, present := d.sets[items]; !present {
				d.sets[items] = &rowSetRows{rowSet: items}
				d.order = append(d.order, items)
			}
		}
		return d
	}
	root := &pendingRow{key: "", values: map[string]*answer.Value{}, shape: map[string]string{}, source: value}
	d.add("", "", root)
	walk(value, "", "", root, "")
	return d
}

// keyRows gives each row of the main row set its identity: the key's value, or a digest of the
// stored content for a datum with no key, made unique within the answer.
func keyRows(d *decomposition, shape infer.Shape) {
	set, present := d.sets[shape.RowSet]
	if !present || shape.RowSet == "" {
		return
	}
	seen := map[string]int{}
	valueKeyed := strings.HasSuffix(shape.RowSet, answer.AnyMember)
	for _, row := range set.rows {
		if valueKeyed {
			// A member of a value-keyed object is its key: the object is rebuilt from it.
			continue
		}
		key := ""
		if len(shape.Key) > 0 && !shape.DedupeOnContent {
			var parts []string
			complete := true
			for _, name := range shape.Key {
				field := row.source.Field(name)
				if field == nil || !field.Scalar() || field.Kind == answer.Null {
					complete = false
					break
				}
				parts = append(parts, field.Text)
			}
			if complete {
				key = strings.Join(parts, "\x1f")
			}
		}
		if key == "" {
			key = "#" + contentDigest(row)
		}
		seen[key]++
		if seen[key] > 1 {
			key += "#" + strconv.Itoa(seen[key])
		}
		row.key = key
	}
}

// contentDigest is a digest of a row's stored content.
func contentDigest(row *pendingRow) string {
	keys := make([]string, 0, len(row.values))
	for key := range row.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		hash.Write([]byte(key))
		hash.Write([]byte{0})
		hash.Write(row.values[key].Marshal())
		hash.Write([]byte{0})
	}
	if row.source != nil && len(row.values) == 0 {
		hash.Write(row.source.Marshal())
	}
	return hex.EncodeToString(hash.Sum(nil)[:12])
}

// Store writes one OK answer: the stored fields of its rows, in their tables, as one new
// generation.
func (m *Mirror) Store(ctx context.Context, datum *Datum, shape infer.Shape, value *answer.Value, fetch Fetch) (
	StoreResult, error) {
	m.mutex.Lock()
	lock, present := m.writing[datum.EndpointID]
	if !present {
		lock = &sync.Mutex{}
		m.writing[datum.EndpointID] = lock
	}
	m.mutex.Unlock()
	lock.Lock()
	defer lock.Unlock()

	fields, whole, err := m.readPaths(ctx, datum.ID)
	if err != nil {
		return StoreResult{}, err
	}
	stored := paths{fields: fields, whole: keysOf(whole)}
	generation := datum.Generation + 1
	result := StoreResult{}

	d := decompose(value, stored, shape)
	keyRows(d, shape)
	if main, present := d.sets[shape.RowSet]; present && shape.Nature == infer.NatureRecordStream &&
		len(shape.Cursor) > 0 {
		for _, row := range main.rows {
			if cursor, ok := infer.CursorValue(row.source, shape.Cursor, fetch.Normaliser); ok {
				row.cursor = &cursor
			}
		}
		result.Gap, err = m.streamGap(ctx, datum, shape, main.rows)
		if err != nil {
			return result, err
		}
	}

	written := map[string]*tableInfo{}
	for _, rowSet := range d.order {
		set := d.sets[rowSet]
		table, err := m.ensureTable(ctx, datum, rowSet, set.parentRowSet)
		if err != nil {
			return result, err
		}
		if err := m.ensureColumns(ctx, datum, table, set, shape); err != nil {
			return result, err
		}
		written[rowSet] = table
	}
	plan := planWrite(datum, d, shape, written, stored, generation, fetch)
	for _, rowSet := range d.order {
		changed, err := m.writeRows(ctx, datum, written[rowSet], d.sets[rowSet], shape, generation, fetch, plan)
		if err != nil {
			return result, err
		}
		result.Changed = result.Changed || changed
	}
	if err := m.releaseVanished(ctx, datum, d, shape, generation, plan); err != nil {
		return result, err
	}

	since := map[string]int64{}
	for path := range fields {
		since[path] = generation
		if previous, held := datum.storedPaths[path]; held {
			since[path] = previous
		}
	}
	wholeSince := map[string]int64{}
	for path := range whole {
		wholeSince[path] = generation
		if previous, held := datum.wholePaths[path]; held {
			wholeSince[path] = previous
		}
	}
	readWhole := whole[""]
	var body any
	if readWhole {
		body = string(fetch.Body)
	}
	rootShape, _ := json.Marshal(map[string]any{"root": int(value.Kind), "stored": since, "whole": wholeSince})
	key, _ := json.Marshal(shape.Key)
	cursor, _ := json.Marshal(shape.Cursor)
	changes := int64(0)
	if result.Changed {
		changes = 1
	}
	if _, err := m.db.ExecContext(ctx, `UPDATE opnsense.mirror_datum SET nature = ?, row_set = ?, key = CAST(? AS jsonb),
		cursor = CAST(? AS jsonb), cursor_type = ?, cursor_descending = CAST(? AS boolean),
		dedupe_on_content = CAST(? AS boolean), generation = ?, fetched_at = ?, attempted_at = ?, outcome = 'ok',
		status_code = ?, detail = NULL, root_shape = CAST(? AS jsonb), page_size = ?, body = ?,
		read_whole = CAST(? AS boolean), changes = changes + ? WHERE id = ?`,
		shape.Nature, shape.RowSet, string(key), string(cursor), shape.CursorType,
		strconv.FormatBool(shape.CursorDescending), strconv.FormatBool(shape.DedupeOnContent), generation, fetch.At,
		fetch.At, int64(fetch.StatusCode), string(rootShape), fetch.PageSize, body, strconv.FormatBool(readWhole),
		changes, datum.ID); err != nil {
		return result, fmt.Errorf("mirror: updating the datum: %w", err)
	}
	// The state is written under the datum's lock; a reader of the same datum reads a Snapshot.
	// Store's own reads above need no lock: only Store writes those fields, and the endpoint's
	// write lock serialises it.
	state := m.stateLock(datum.ID)
	state.Lock()
	datum.Nature, datum.RowSet, datum.Key, datum.Cursor = shape.Nature, shape.RowSet, shape.Key, shape.Cursor
	datum.CursorType, datum.CursorDescending, datum.DedupeOnContent = shape.CursorType, shape.CursorDescending,
		shape.DedupeOnContent
	datum.Generation, datum.FetchedAt, datum.AttemptedAt = generation, fetch.At, fetch.At
	datum.Outcome, datum.StatusCode, datum.Detail = "ok", fetch.StatusCode, ""
	datum.PageSize, datum.ReadWhole, datum.RootKind = fetch.PageSize, readWhole, value.Kind
	datum.storedPaths, datum.wholePaths = since, wholeSince
	datum.Body = ""
	if readWhole {
		datum.Body = string(fetch.Body)
	}
	state.Unlock()
	if result.Gap != nil {
		if err := m.RecordGap(ctx, datum.ID, *result.Gap); err != nil {
			return result, err
		}
	}
	return result, nil
}

// streamGap finds the interval a record stream's answer does not reach back to.
func (m *Mirror) streamGap(ctx context.Context, datum *Datum, shape infer.Shape, rows []*pendingRow) (*Gap, error) {
	if datum.Generation == 0 || shape.CursorType != infer.TypeTimestamp {
		return nil, nil
	}
	oldest := 0.0
	found := false
	for _, row := range rows {
		if row.cursor != nil && (!found || *row.cursor < oldest) {
			oldest, found = *row.cursor, true
		}
	}
	if !found {
		return nil, nil
	}
	table, err := m.loadTable(ctx, TableName(datum.Endpoint, shape.RowSet))
	if err != nil || table == nil {
		return nil, err
	}
	var newest *float64
	if err := m.db.QueryRowContext(ctx, `SELECT max("#cursor") FROM `+qualified(table.name)+
		` WHERE "#datum" = ?`, datum.ID).Scan(&newest); err != nil {
		return nil, err
	}
	if newest == nil || oldest <= *newest {
		return nil, nil
	}
	return &Gap{From: int64(*newest), To: int64(oldest), Reason: GapAnswerNewerThanStored}, nil
}

// absolute is a path relative to a row set, made absolute.
func absolute(rowSet, rel string) string {
	if rel == "" {
		return rowSet
	}
	if rowSet == "" {
		return rel
	}
	return rowSet + "." + rel
}

// ensureColumns gives a table a column for every stored field its rows carry, of the type this
// answer shows: a type the column cannot hold converts it.
func (m *Mirror) ensureColumns(ctx context.Context, datum *Datum, table *tableInfo, set *rowSetRows,
	shape infer.Shape) error {
	kinds := map[string]map[answer.Kind]bool{}
	for _, row := range set.rows {
		for rel, value := range row.values {
			if kinds[rel] == nil {
				kinds[rel] = map[answer.Kind]bool{}
			}
			kinds[rel][value.Kind] = true
		}
	}
	rels := make([]string, 0, len(kinds))
	for rel := range kinds {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		abs := absolute(set.rowSet, rel)
		kind := shape.Types[abs]
		if kind == "" {
			kind = infer.TypeText
		}
		seen := kinds[rel]
		delete(seen, answer.Null)
		switch {
		case seen[answer.Object] || seen[answer.Array] || len(seen) > 1:
			kind = infer.TypeJSON
		case seen[answer.Number] && kind != infer.TypeNumber && kind != infer.TypeTimestamp:
			kind = infer.TypeNumber
		case seen[answer.Bool]:
			kind = infer.TypeBoolean
		case seen[answer.String] && (kind == infer.TypeNumber || kind == infer.TypeBoolean):
			kind = infer.TypeText
		}
		if kind == infer.TypeTimestamp && seen[answer.Number] {
			// An epoch timestamp is a number the catalogue reads as an instant.
			kind = infer.TypeNumber
		}
		column, present := table.columns[rel]
		if present && column.kind != kind && kindsFit(column.kind, seen) {
			continue
		}
		if _, err := m.ensureColumn(ctx, datum, table, rel, kind, shape.Domains[abs],
			"read by "+displayPath(abs)); err != nil {
			return err
		}
	}
	return nil
}

// kindsFit reports whether a column of one type holds values of the JSON kinds seen.
func kindsFit(kind string, seen map[answer.Kind]bool) bool {
	switch sqlType(kind) {
	case "jsonb":
		return true
	case "numeric":
		return len(seen) == 1 && seen[answer.Number]
	case "boolean":
		return len(seen) == 1 && seen[answer.Bool]
	}
	return len(seen) <= 1 && (len(seen) == 0 || seen[answer.String])
}

func displayPath(path string) string {
	if path == "" {
		return "the answer's root"
	}
	return path
}

// writeRows writes a row set's rows as a new generation. The answer's top rows are upserted on
// their identity; the rows of a nested row set are replaced under their parents. A snapshot row
// whose stored content changed writes its previous content to the table's history. Under an
// incremental plan, a top row the newest answer holds unchanged -- the same digest, still held --
// is not written, and neither is anything nested under it.
func (m *Mirror) writeRows(ctx context.Context, datum *Datum, table *tableInfo, set *rowSetRows, shape infer.Shape,
	generation int64, fetch Fetch, plan *writePlan) (bool, error) {
	if len(set.rows) == 0 {
		return false, nil
	}
	persistent := set.rowSet == "" || set.rowSet == shape.RowSet
	work := set
	if plan.incremental {
		if persistent {
			if err := m.keepUnchanged(ctx, datum, table, set, plan); err != nil {
				return false, err
			}
		}
		work = &rowSetRows{rowSet: set.rowSet, parentRowSet: set.parentRowSet}
		for _, row := range set.rows {
			if !persistent && row.parent != nil && plan.kept[row.parent] {
				plan.kept[row] = true
			}
			if !plan.kept[row] {
				work.rows = append(work.rows, row)
			}
		}
	}
	if !persistent && len(work.rows) > 0 {
		parents := map[int64]bool{}
		for _, row := range work.rows {
			parents[parentID(row)] = true
		}
		if err := m.deleteChildren(ctx, datum, table.name, ids(parents)); err != nil {
			return false, err
		}
	}
	columns := sortedColumns(table)

	changed := false
	if persistent && shape.Nature == infer.NatureSnapshot && len(shape.Key) > 0 && set.rowSet == shape.RowSet {
		previous, err := m.currentContent(ctx, datum, table, columns, work)
		if err != nil {
			return false, err
		}
		var history []historyRow
		for _, row := range work.rows {
			old, held := previous[row.key]
			if !held {
				changed = true
				continue
			}
			if old.content != rowContent(row, columns) {
				changed = true
				history = append(history, historyRow{id: old.id, values: old.values})
			}
		}
		if err := m.writeHistory(ctx, datum, table, history, fetch.At); err != nil {
			return false, err
		}
	} else {
		changed = true
	}
	if len(work.rows) == 0 {
		return changed, nil
	}

	names := append([]string(nil), systemColumns...)
	types := append([]string(nil), systemColumnTypes...)
	for _, column := range columns {
		names = append(names, column.name)
		types = append(types, sqlType(column.kind))
		if column.kind == infer.TypeTimestamp {
			names = append(names, column.name+"#epoch")
			types = append(types, "bigint")
		}
	}
	arrays := make([][]*string, len(names))
	for _, row := range work.rows {
		cells := rowCells(datum, row, columns, generation, fetch)
		if digest, digested := plan.digests[row]; digested {
			cells[digestCell] = &digest
		}
		for index, value := range cells {
			arrays[index] = append(arrays[index], value)
		}
	}

	quotedNames := make([]string, len(names))
	casts := make([]string, len(names))
	arguments := make([]any, len(names))
	var updates []string
	for index, name := range names {
		quotedNames[index] = quote(name)
		casts[index] = "CAST(? AS " + types[index] + "[])"
		arguments[index] = arrayLiteral(arrays[index])
		if name != "#datum" && name != "#parent" && name != "#key" && name != "#first_seen" {
			updates = append(updates, quote(name)+" = excluded."+quote(name))
		}
	}
	statement := "INSERT INTO " + qualified(table.name) + " (" + strings.Join(quotedNames, ", ") + ") SELECT * FROM unnest(" +
		strings.Join(casts, ", ") + `) ON CONFLICT ("#datum", "#parent", "#key") DO UPDATE SET ` +
		strings.Join(updates, ", ") + ` RETURNING "#id", "#parent", "#key"`
	rows, err := m.db.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return false, fmt.Errorf("mirror: writing %s: %w", table.name, err)
	}
	defer func() { _ = rows.Close() }()
	byIdentity := map[[2]string]*pendingRow{}
	for _, row := range work.rows {
		byIdentity[[2]string{strconv.FormatInt(parentID(row), 10), row.key}] = row
	}
	for rows.Next() {
		var id, parent int64
		var key string
		if err := rows.Scan(&id, &parent, &key); err != nil {
			return false, err
		}
		if row, found := byIdentity[[2]string{strconv.FormatInt(parent, 10), key}]; found {
			row.id, row.written = id, true
		}
	}
	return changed, rows.Err()
}

// The places of the bookkeeping cells a row's cells begin with (systemColumns).
const (
	parentCell     = 1
	generationCell = 4
	firstSeenCell  = 5
	lastSeenCell   = 6
	lexicalCell    = 8
	shapeCell      = 9
	digestCell     = 12
)

// sortedColumns are a table's columns in the order a row's cells list them.
func sortedColumns(table *tableInfo) []*columnInfo {
	columns := make([]*columnInfo, 0, len(table.columns))
	for _, column := range table.columns {
		columns = append(columns, column)
	}
	sort.Slice(columns, func(i, j int) bool { return columns[i].name < columns[j].name })
	return columns
}

// rowCells are a row's cells as its table stores them: the bookkeeping cells (systemColumns), then
// each column's, and a timestamp column's epoch after it. The digest cell is left empty.
func rowCells(datum *Datum, row *pendingRow, columns []*columnInfo, generation int64, fetch Fetch) []*string {
	text := func(value string) *string { return &value }
	nulls := []string{}
	lexical := map[string]string{}
	for rel, value := range row.values {
		if value.Kind == answer.Null {
			nulls = append(nulls, rel)
		}
	}
	sort.Strings(nulls)
	encodedNulls, _ := json.Marshal(nulls)
	var cursor *string
	if row.cursor != nil {
		cursor = text(strconv.FormatFloat(*row.cursor, 'f', -1, 64))
	}
	cells := []*string{text(strconv.FormatInt(datum.ID, 10)), text(strconv.FormatInt(parentID(row), 10)),
		text(row.key), text(strconv.Itoa(row.ordinal)), text(strconv.FormatInt(generation, 10)),
		text(strconv.FormatInt(fetch.At, 10)), text(strconv.FormatInt(fetch.At, 10)), text(string(encodedNulls)),
		nil, nil, cursor, text("true"), nil}
	for _, column := range columns {
		value := row.values[column.path]
		cells = append(cells, cell(value, column, lexical))
		if column.kind == infer.TypeTimestamp {
			var epoch *string
			if value != nil {
				if instant, ok := infer.Instant(value, fetch.Normaliser); ok {
					epoch = text(strconv.FormatInt(instant, 10))
				}
			}
			cells = append(cells, epoch)
		}
	}
	if len(lexical) > 0 {
		encoded, _ := json.Marshal(lexical)
		cells[lexicalCell] = text(string(encoded))
	}
	if len(row.shape) > 0 {
		encoded, _ := json.Marshal(row.shape)
		cells[shapeCell] = text(string(encoded))
	}
	return cells
}

// parentID is the row id of a row's parent, 0 at the top.
func parentID(row *pendingRow) int64 {
	if row.parent == nil {
		return 0
	}
	return row.parent.id
}

func ids(set map[int64]bool) []int64 {
	result := make([]int64, 0, len(set))
	for id := range set {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// cell is a value as its column stores it, and what the column cannot print back exactly goes to
// the row's lexical record.
func cell(value *answer.Value, column *columnInfo, lexical map[string]string) *string {
	if value == nil || value.Kind == answer.Null {
		return nil
	}
	text := func(s string) *string { return &s }
	switch sqlType(column.kind) {
	case "numeric":
		if !answer.CanonicalNumber(value.Text) {
			lexical[column.path] = value.Text
		}
		return text(value.Text)
	case "boolean":
		return text(value.Text)
	case "jsonb":
		encoded := string(value.Marshal())
		if !value.Scalar() || (value.Kind == answer.Number && !answer.CanonicalNumber(value.Text)) {
			lexical[column.path] = encoded
		}
		return text(encoded)
	}
	return text(value.Text)
}

// arrayLiteral writes a PostgreSQL array literal of texts, NULL for nil.
func arrayLiteral(values []*string) string {
	var out strings.Builder
	out.WriteByte('{')
	for index, value := range values {
		if index > 0 {
			out.WriteByte(',')
		}
		if value == nil {
			out.WriteString("NULL")
			continue
		}
		out.WriteByte('"')
		for _, character := range *value {
			if character == '"' || character == '\\' {
				out.WriteByte('\\')
			}
			out.WriteRune(character)
		}
		out.WriteByte('"')
	}
	out.WriteByte('}')
	return out.String()
}

// deleteChildren removes the rows of a nested row set under the given parents, and the rows
// below them, before the new answer's are written.
func (m *Mirror) deleteChildren(ctx context.Context, datum *Datum, table string, parents []int64) error {
	if len(parents) == 0 {
		return nil
	}
	rows, err := m.db.QueryContext(ctx, `DELETE FROM `+qualified(table)+` WHERE "#datum" = ? AND "#parent" = ANY(CAST(? AS bigint[]))
		RETURNING "#id"`, datum.ID, int64ArrayLiteral(parents))
	if err != nil {
		return fmt.Errorf("mirror: replacing the rows of %s: %w", table, err)
	}
	var removed []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		removed = append(removed, id)
	}
	_ = rows.Close()
	if len(removed) == 0 {
		return nil
	}
	children, err := m.childTables(ctx, table)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := m.deleteChildren(ctx, datum, child, removed); err != nil {
			return err
		}
	}
	return nil
}

// childTables are the tables of the row sets nested in a table's.
func (m *Mirror) childTables(ctx context.Context, table string) ([]string, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT c.name FROM opnsense.mirror_table AS c
		JOIN opnsense.mirror_table AS p ON p.endpoint_id = c.endpoint_id AND c.parent_row_set = p.row_set
		WHERE p.name = ? AND c.name <> p.name`, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func int64ArrayLiteral(values []int64) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = strconv.FormatInt(value, 10)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// stored content of a row, for the snapshot history.
type storedRow struct {
	id      int64
	content string
	values  map[string]any
}

type historyRow struct {
	id     int64
	values map[string]any
}

// rowContent is a row's stored content as the comparison reads it.
func rowContent(row *pendingRow, columns []*columnInfo) string {
	parts := make([]string, 0, len(columns))
	lexical := map[string]string{}
	for _, column := range columns {
		value := row.values[column.path]
		switch {
		case value == nil:
			parts = append(parts, column.path+"=absent")
		case value.Kind == answer.Null:
			parts = append(parts, column.path+"=null")
		default:
			stored := cell(value, column, lexical)
			parts = append(parts, column.path+"="+*stored)
		}
	}
	return strings.Join(parts, "\x1e")
}

// currentContent reads the stored content of the rows an answer's keys name.
func (m *Mirror) currentContent(ctx context.Context, datum *Datum, table *tableInfo, columns []*columnInfo,
	set *rowSetRows) (map[string]storedRow, error) {
	selected := []string{`r."#id"`, `r."#key"`, `r."#nulls"`}
	for _, column := range columns {
		selected = append(selected, "CAST(r."+quote(column.name)+" AS text)")
	}
	keys := []*string{}
	for _, row := range set.rows {
		key := row.key
		keys = append(keys, &key)
	}
	// The keys are joined, never tested with = ANY (incremental.go, keepUnchanged).
	rows, err := m.db.QueryContext(ctx, "SELECT "+strings.Join(selected, ", ")+" FROM unnest(CAST(? AS text[])) AS k (key) "+
		"JOIN "+qualified(table.name)+` AS r ON r."#key" = k.key WHERE r."#datum" = ?`, arrayLiteral(keys), datum.ID)
	if err != nil {
		return nil, fmt.Errorf("mirror: reading the rows of %s: %w", table.name, err)
	}
	defer func() { _ = rows.Close() }()
	result := map[string]storedRow{}
	for rows.Next() {
		var (
			id    int64
			key   string
			nulls *string
		)
		cells := make([]*string, len(columns))
		targets := []any{&id, &key, &nulls}
		for index := range cells {
			targets = append(targets, &cells[index])
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		nullSet := map[string]bool{}
		if nulls != nil {
			var list []string
			_ = json.Unmarshal([]byte(*nulls), &list)
			for _, path := range list {
				nullSet[path] = true
			}
		}
		parts := make([]string, 0, len(columns))
		values := map[string]any{}
		for index, column := range columns {
			switch {
			case nullSet[column.path]:
				parts = append(parts, column.path+"=null")
				values[column.path] = nil
			case cells[index] == nil:
				parts = append(parts, column.path+"=absent")
			default:
				stored := *cells[index]
				if sqlType(column.kind) == "boolean" {
					stored = booleanText(stored)
				}
				parts = append(parts, column.path+"="+stored)
				values[column.path] = stored
			}
		}
		result[key] = storedRow{id: id, content: strings.Join(parts, "\x1e"), values: values}
	}
	return result, rows.Err()
}

// booleanText is PostgreSQL's text of a boolean as JSON writes it.
func booleanText(stored string) string {
	switch stored {
	case "t", "true":
		return "true"
	case "f", "false":
		return "false"
	}
	return stored
}

// writeHistory records the previous content of the snapshot rows whose stored fields changed.
func (m *Mirror) writeHistory(ctx context.Context, datum *Datum, table *tableInfo, history []historyRow,
	at int64) error {
	if len(history) == 0 {
		return nil
	}
	name := identifier(table.name + "#history")
	statements := []string{
		"CREATE TABLE IF NOT EXISTS " + qualified(name) + ` (
			"#row" BIGINT NOT NULL,
			"#datum" BIGINT NOT NULL REFERENCES opnsense.mirror_datum (id) ON DELETE CASCADE,
			"#changed_at" BIGINT NOT NULL,
			"#values" JSONB NOT NULL)`,
		"CREATE INDEX IF NOT EXISTS " + quote(identifier(name+"#row")) + " ON " + qualified(name) +
			` ("#datum", "#row", "#changed_at")`,
	}
	for _, statement := range statements {
		if _, err := m.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("mirror: creating the history of %s: %w", table.name, err)
		}
	}
	if err := m.store.GrantReadOnTable(ctx, Schema, name); err != nil {
		return err
	}
	for _, row := range history {
		encoded, _ := json.Marshal(row.values)
		if _, err := m.db.ExecContext(ctx, "INSERT INTO "+qualified(name)+
			` ("#row", "#datum", "#changed_at", "#values") VALUES (?, ?, ?, CAST(? AS jsonb))`,
			row.id, datum.ID, at, string(encoded)); err != nil {
			return fmt.Errorf("mirror: writing the history of %s: %w", table.name, err)
		}
	}
	return nil
}
