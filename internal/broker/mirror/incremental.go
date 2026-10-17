package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/broker/infer"
)

// Writing an answer incrementally: the newest generation is stamped on the datum, and a row the
// answer holds unchanged is not written again (the maintainer's decision of 12 October 2026).
//
// WHAT "UNCHANGED" MEANS. Each top row -- the answer's root row and the rows of its main row set --
// carries a digest of everything it and every row nested under it store, and of the fields the
// datum stores at all (a field newly stored is a column every row gains, so every row is written
// again once). A top row whose digest is the one stored, and which the datum's last answer held,
// keeps its row, its id, its generation and everything nested under it; the others are written as
// before. A top row the answer no longer holds is marked so ("#held"), with everything nested under
// it, and is kept until the retention purges it.
//
// WHEN IT APPLIES. When the main row set's rows are the root's own -- `rows.*` under the answer's
// root, or a root array's items -- every row of the answer is a top row or nested under one, and the
// digest says everything. An answer whose main row set is nested deeper (the rows of `data.*.items.*`
// hang under rows that are themselves replaced at every write) is written whole, as it always was,
// and the rows of older generations are marked as no longer held.

// writePlan is what one write decided before writing.
type writePlan struct {
	// incremental says unchanged top rows are kept.
	incremental bool
	// digests are each top row's digest.
	digests map[*pendingRow]string
	// kept are the rows not written: top rows held unchanged, and everything nested under them.
	kept map[*pendingRow]bool
}

// nestedUnderRoot reports whether a row set's rows are the root's own: no step of its path but the
// last is a wildcard.
func nestedUnderRoot(rowSet string) bool {
	steps := answer.Steps(rowSet)
	for index, step := range steps {
		if index < len(steps)-1 && (step == answer.AnyItem || step == answer.AnyMember) {
			return false
		}
	}
	return true
}

// planWrite decides how an answer is written, and digests its top rows.
func planWrite(datum *Datum, d *decomposition, shape infer.Shape, tables map[string]*tableInfo, stored paths,
	generation int64, fetch Fetch) *writePlan {
	plan := &writePlan{digests: map[*pendingRow]string{}, kept: map[*pendingRow]bool{}}
	plan.incremental = shape.RowSet == "" || nestedUnderRoot(shape.RowSet)
	if !plan.incremental {
		return plan
	}
	persistent := func(rowSet string) bool { return rowSet == "" || rowSet == shape.RowSet }
	children := map[*pendingRow][]*pendingRow{}
	for _, rowSet := range d.order {
		if persistent(rowSet) {
			continue
		}
		for _, row := range d.sets[rowSet].rows {
			if row.parent != nil {
				children[row.parent] = append(children[row.parent], row)
			}
		}
	}
	// What every row's digest begins with: the fields the datum stores, and the paths read whole.
	fields := make([]string, 0, len(stored.fields))
	for path := range stored.fields {
		fields = append(fields, path)
	}
	sort.Strings(fields)
	whole := append([]string(nil), stored.whole...)
	sort.Strings(whole)
	header := strings.Join(fields, "\x1f") + "\x1e" + strings.Join(whole, "\x1f")

	columnsOf := map[string][]*columnInfo{}
	var digest func(row *pendingRow) []byte
	digest = func(row *pendingRow) []byte {
		columns, known := columnsOf[row.rowSet]
		if !known {
			if table := tables[row.rowSet]; table != nil {
				columns = sortedColumns(table)
			}
			columnsOf[row.rowSet] = columns
		}
		hash := sha256.New()
		hash.Write([]byte(header))
		hash.Write([]byte{0})
		hash.Write([]byte(row.rowSet))
		for _, column := range columns {
			hash.Write([]byte{0x1f})
			hash.Write([]byte(column.name + "\x1d" + column.kind))
		}
		for index, value := range rowCells(datum, row, columns, generation, fetch) {
			switch index {
			case parentCell, generationCell, firstSeenCell, lastSeenCell, digestCell:
				// Identity is the key under the same parent; when it was written is not content.
				continue
			}
			hash.Write([]byte{0x1e})
			if value == nil {
				hash.Write([]byte{0})
				continue
			}
			hash.Write([]byte{1})
			hash.Write([]byte(*value))
		}
		for _, child := range children[row] {
			hash.Write([]byte{0x1c})
			hash.Write(digest(child))
		}
		return hash.Sum(nil)
	}
	for _, rowSet := range d.order {
		if !persistent(rowSet) {
			continue
		}
		for _, row := range d.sets[rowSet].rows {
			plan.digests[row] = hex.EncodeToString(digest(row))
		}
	}
	return plan
}

// keptRow is a stored top row as the incremental write reads it.
type keptRow struct {
	id     int64
	digest *string
	held   bool
}

// keepUnchanged reads the stored digest of each of a top row set's rows and keeps those the datum's
// last answer held with the same digest: they are not written, and they keep their id.
func (m *Mirror) keepUnchanged(ctx context.Context, datum *Datum, table *tableInfo, set *rowSetRows,
	plan *writePlan) error {
	parents := map[int64]bool{}
	keys := make([]*string, 0, len(set.rows))
	for _, row := range set.rows {
		parents[parentID(row)] = true
		key := row.key
		keys = append(keys, &key)
	}
	// The keys are joined, never tested with = ANY: a prepared statement's array parameter is no
	// constant the planner can hash, and a thousand keys tested row by row cost a thousand times a
	// thousand comparisons.
	rows, err := m.db.QueryContext(ctx, `SELECT r."#id", r."#parent", r."#key", r."#digest", r."#held"
		FROM unnest(CAST($1 AS text[])) AS k (key)
		JOIN `+qualified(table.name)+` AS r ON r."#key" = k.key
		WHERE r."#datum" = $2 AND r."#parent" = ANY(CAST($3 AS bigint[]))`,
		arrayLiteral(keys), datum.ID, int64ArrayLiteral(ids(parents)))
	if err != nil {
		return fmt.Errorf("mirror: reading the digests of %s: %w", table.name, err)
	}
	defer func() { _ = rows.Close() }()
	stored := map[[2]string]keptRow{}
	for rows.Next() {
		var (
			held   keptRow
			parent int64
			key    string
		)
		if err := rows.Scan(&held.id, &parent, &key, &held.digest, &held.held); err != nil {
			return err
		}
		stored[[2]string{strconv.FormatInt(parent, 10), key}] = held
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, row := range set.rows {
		held, found := stored[[2]string{strconv.FormatInt(parentID(row), 10), row.key}]
		if found && held.held && held.digest != nil && *held.digest == plan.digests[row] {
			row.id, row.written = held.id, true
			plan.kept[row] = true
		}
	}
	return nil
}

// releaseVanished marks the rows of the main row set the newest answer does not hold -- and every
// row nested under them -- as no longer held: by key under an incremental write, by generation
// under a whole one.
func (m *Mirror) releaseVanished(ctx context.Context, datum *Datum, d *decomposition, shape infer.Shape,
	generation int64, plan *writePlan) error {
	if shape.RowSet == "" {
		return nil
	}
	name := TableName(datum.Endpoint, shape.RowSet)
	table, err := m.loadTable(ctx, name)
	if err != nil || table == nil {
		return err
	}
	if err := m.bringUpToDate(ctx, name); err != nil {
		return err
	}
	var (
		statement string
		arguments []any
	)
	if plan.incremental {
		parent := int64(0)
		if root, present := d.sets[""]; present && len(root.rows) == 1 {
			parent = root.rows[0].id
		}
		keys := []*string{}
		if main, present := d.sets[shape.RowSet]; present {
			for _, row := range main.rows {
				key := row.key
				keys = append(keys, &key)
			}
		}
		statement = `UPDATE ` + qualified(name) + ` AS r SET "#held" = false WHERE r."#datum" = $1 AND r."#held"
			AND r."#parent" = $2 AND NOT EXISTS (SELECT 1 FROM unnest(CAST($3 AS text[])) AS k (key) WHERE k.key = r."#key")
			RETURNING r."#id"`
		arguments = []any{datum.ID, parent, arrayLiteral(keys)}
	} else {
		statement = `UPDATE ` + qualified(name) + ` SET "#held" = false WHERE "#datum" = $1 AND "#held"
			AND "#generation" < $2 RETURNING "#id"`
		arguments = []any{datum.ID, generation}
	}
	gone, err := m.returnedIDs(ctx, statement, arguments...)
	if err != nil {
		return fmt.Errorf("mirror: releasing the rows of %s: %w", name, err)
	}
	return m.releaseChildren(ctx, datum, name, gone)
}

// releaseChildren marks the rows nested under released rows as no longer held, at every depth.
func (m *Mirror) releaseChildren(ctx context.Context, datum *Datum, table string, parents []int64) error {
	if len(parents) == 0 {
		return nil
	}
	children, err := m.childTables(ctx, table)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := m.bringUpToDate(ctx, child); err != nil {
			return err
		}
		gone, err := m.returnedIDs(ctx, `UPDATE `+qualified(child)+` SET "#held" = false WHERE "#datum" = $1
			AND "#held" AND "#parent" = ANY(CAST($2 AS bigint[])) RETURNING "#id"`, datum.ID, int64ArrayLiteral(parents))
		if err != nil {
			return fmt.Errorf("mirror: releasing the rows of %s: %w", child, err)
		}
		if err := m.releaseChildren(ctx, datum, child, gone); err != nil {
			return err
		}
	}
	return nil
}

// returnedIDs runs a statement returning row ids and collects them.
func (m *Mirror) returnedIDs(ctx context.Context, statement string, arguments ...any) ([]int64, error) {
	rows, err := m.db.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}
