package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/broker/infer"
)

// The data tables.
//
// ONE TABLE PER ENDPOINT AND ROW SET. An answer is a tree; the mirror cuts it at every array and
// every value-keyed object a reader reached into, and stores each such row set in a table of its
// own: the answer's root object in the endpoint's table, `rows[]` in `<endpoint>__rows`, a
// value-keyed object `interfaces.*` in `<endpoint>__interfaces`, its members keyed by their key --
// never one column per key, which would write installation data into the schema. A row set's rows
// hold the fields read below it, flattened: `meta.version` is a column of the row that holds `meta`.
//
// EVERY TABLE CARRIES THE SAME BOOKKEEPING, under names beginning `#`, which no field name of an
// answer takes (a field beginning with `#` is stored under `##...`):
//
//	#id          the row
//	#datum       the datum it belongs to
//	#parent      the row of the enclosing row set holding it, 0 at the top
//	#key         its identity: the identity key's value, the member's key, the item's place, or
//	             a digest of its stored content
//	#ordinal     its place in the last answer that held it
//	#generation  the answer that last wrote it: the answer that held it first, or changed it
//	#first_seen, #last_seen  the instants of the answer that held it first and of the one that last
//	             wrote it; while the datum's newest answer holds it, that answer's instant is the
//	             datum's own fetched_at
//	#nulls       the fields the firewall answered null, which an absent field is not
//	#lexical     a value whose column cannot print it back exactly -- a number in exponent form,
//	             an object in a JSONB column -- kept as the firewall wrote it
//	#shape       the containers present in the row, so an empty array is not an absent one
//	#cursor      a record stream's cursor, as a number: an instant, or the cursor itself
//	#held        whether the datum's newest answer holds it
//	#digest      a digest of everything the row and the rows nested under it store, and of the
//	             fields the datum stores: what tells an unchanged row from a changed one
//
// THE NEWEST GENERATION IS STAMPED ON THE DATUM, NOT ON EVERY ROW (the maintainer's decision of 12
// October 2026). A write rewrites the rows whose digest changed, inserts the new ones and marks those
// the answer no longer holds; a row the answer holds unchanged is not touched, so an answer that
// repeats the last one writes the datum's row and nothing else (write.go).

// systemColumns are the bookkeeping columns, in the order every insert writes them.
var systemColumns = []string{"#datum", "#parent", "#key", "#ordinal", "#generation", "#first_seen", "#last_seen",
	"#nulls", "#lexical", "#shape", "#cursor", "#held", "#digest"}

// systemColumnTypes are their types, in the same order.
var systemColumnTypes = []string{"bigint", "bigint", "text", "bigint", "bigint", "bigint", "bigint", "jsonb", "jsonb",
	"jsonb", "double precision", "boolean", "text"}

// ValueColumn is the column of an array's scalar items.
const ValueColumn = "#value"

// sqlType is the column type of a mirror type.
func sqlType(kind string) string {
	switch kind {
	case infer.TypeNumber:
		return "numeric"
	case infer.TypeBoolean:
		return "boolean"
	case infer.TypeJSON:
		return "jsonb"
	}
	return "text"
}

// tableInfo is one data table and its columns, as the metadata records them.
type tableInfo struct {
	name    string
	rowSet  string
	columns map[string]*columnInfo // by path relative to the row set
}

// columnInfo is one column.
type columnInfo struct {
	path    string // relative to the row set; "" is an array's scalar item
	name    string
	kind    string
	domains bool
}

// identifier is a PostgreSQL name for a text: kept as it is when it fits, else cut and
// completed by a digest so two long names stay apart.
func identifier(text string) string {
	if len(text) <= 63 {
		return text
	}
	sum := sha256.Sum256([]byte(text))
	return text[:50] + "_" + hex.EncodeToString(sum[:6])
}

// quote quotes an identifier.
func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// qualified is a data table's qualified, quoted name.
func qualified(table string) string { return quote(Schema) + "." + quote(table) }

// TableName is the data table of an endpoint's row set: the API path's elements, the method when
// it is POST, and the row set's steps -- every one an API name, never a value.
func TableName(endpoint Endpoint, rowSet string) string {
	var parts []string
	for _, element := range strings.Split(strings.Trim(endpoint.Path, "/"), "/") {
		parts = append(parts, sanitise(element))
	}
	name := strings.Join(parts, "_")
	if endpoint.Method == "POST" {
		name += "_post"
	}
	for index, step := range answer.Steps(rowSet) {
		switch {
		case step == answer.AnyItem && index == 0:
			name += "__items"
		case step == answer.AnyMember && index == 0:
			name += "__members"
		case step == answer.AnyItem || step == answer.AnyMember:
			// The field before the wildcard names the row set.
		default:
			name += "__" + sanitise(step)
		}
	}
	return identifier(name)
}

// sanitise keeps the letters, digits and underscores of a name.
func sanitise(text string) string {
	var out strings.Builder
	for _, character := range text {
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9', character == '_':
			out.WriteRune(character)
		default:
			out.WriteByte('_')
		}
	}
	return out.String()
}

// columnName is the column of a field path relative to its row set.
func columnName(relative string) string {
	if relative == "" {
		return ValueColumn
	}
	if strings.HasPrefix(relative, "#") {
		relative = "#" + relative
	}
	return identifier(relative)
}

// loadTable reads one data table's metadata, or nil when it does not exist yet.
func (m *Mirror) loadTable(ctx context.Context, name string) (*tableInfo, error) {
	var rowSet string
	err := m.db.QueryRowContext(ctx, `SELECT row_set FROM opnsense.mirror_table WHERE name = ?`, name).Scan(&rowSet)
	if err != nil {
		return nil, nil
	}
	table := &tableInfo{name: name, rowSet: rowSet, columns: map[string]*columnInfo{}}
	rows, err := m.db.QueryContext(ctx, `SELECT path, column_name, type, carries_domain FROM opnsense.mirror_column
		WHERE table_name = ?`, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		column := &columnInfo{}
		if err := rows.Scan(&column.path, &column.name, &column.kind, &column.domains); err != nil {
			return nil, err
		}
		table.columns[column.path] = column
	}
	return table, rows.Err()
}

// ensureTable creates a row set's table when it does not exist, records it, and grants the read
// role SELECT on it.
func (m *Mirror) ensureTable(ctx context.Context, datum *Datum, rowSet, parentRowSet string) (*tableInfo, error) {
	name := TableName(datum.Endpoint, rowSet)
	table, err := m.loadTable(ctx, name)
	if err != nil {
		return nil, err
	}
	if table != nil {
		return table, m.bringUpToDate(ctx, name)
	}
	statements := []string{
		"CREATE TABLE IF NOT EXISTS " + qualified(name) + ` (
			"#id" BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			"#datum" BIGINT NOT NULL REFERENCES opnsense.mirror_datum (id) ON DELETE CASCADE,
			"#parent" BIGINT NOT NULL DEFAULT 0,
			"#key" TEXT NOT NULL,
			"#ordinal" BIGINT NOT NULL,
			"#generation" BIGINT NOT NULL,
			"#first_seen" BIGINT NOT NULL,
			"#last_seen" BIGINT NOT NULL,
			"#nulls" JSONB,
			"#lexical" JSONB,
			"#shape" JSONB,
			"#cursor" DOUBLE PRECISION,
			"#held" BOOLEAN NOT NULL DEFAULT true,
			"#digest" TEXT)`,
		"CREATE UNIQUE INDEX IF NOT EXISTS " + quote(identifier(name+"#identity")) + " ON " + qualified(name) +
			` ("#datum", "#parent", "#key")`,
		"CREATE INDEX IF NOT EXISTS " + quote(identifier(name+"#generation")) + " ON " + qualified(name) +
			` ("#datum", "#generation", "#ordinal")`,
		"CREATE INDEX IF NOT EXISTS " + quote(identifier(name+"#cursor")) + " ON " + qualified(name) +
			` ("#datum", "#cursor")`,
	}
	for _, statement := range statements {
		if _, err := m.db.ExecContext(ctx, statement); err != nil {
			return nil, fmt.Errorf("mirror: creating the table %s: %w", name, err)
		}
	}
	var parent any
	if parentRowSet != "" || rowSet != "" {
		parent = parentRowSet
	}
	if _, err := m.db.ExecContext(ctx, `INSERT INTO opnsense.mirror_table (name, endpoint_id, row_set, parent_row_set,
		created_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT (name) DO NOTHING`, name, datum.EndpointID, rowSet, parent,
		m.now()); err != nil {
		return nil, fmt.Errorf("mirror: recording the table %s: %w", name, err)
	}
	if err := m.store.GrantReadOnTable(ctx, Schema, name); err != nil {
		return nil, err
	}
	if err := m.recordVersion(ctx, datum.EndpointID, "table "+name+" created for "+displayRowSet(rowSet)); err != nil {
		return nil, err
	}
	m.mutex.Lock()
	m.current[name] = true
	m.mutex.Unlock()
	return &tableInfo{name: name, rowSet: rowSet, columns: map[string]*columnInfo{}}, nil
}

// bringUpToDate gives a table created before the newest generation was stamped on the datum the two
// bookkeeping columns that stamp needs, once per process: every row its datum's newest answer did
// not write is then marked as not held, which is what the generation it carries said.
func (m *Mirror) bringUpToDate(ctx context.Context, name string) error {
	m.mutex.Lock()
	done := m.current[name]
	m.mutex.Unlock()
	if done {
		return nil
	}
	var present bool
	if err := m.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema = ? AND table_name = ? AND column_name = '#held')`, Schema, name).Scan(&present); err != nil {
		return fmt.Errorf("mirror: reading the columns of %s: %w", name, err)
	}
	if !present {
		for _, statement := range []string{
			"ALTER TABLE " + qualified(name) + ` ADD COLUMN IF NOT EXISTS "#held" BOOLEAN NOT NULL DEFAULT true,
				ADD COLUMN IF NOT EXISTS "#digest" TEXT`,
			"UPDATE " + qualified(name) + ` AS r SET "#held" = false FROM opnsense.mirror_datum AS d
				WHERE d.id = r."#datum" AND r."#generation" < d.generation`,
		} {
			if _, err := m.db.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("mirror: bringing %s up to date: %w", name, err)
			}
		}
	}
	m.mutex.Lock()
	m.current[name] = true
	m.mutex.Unlock()
	return nil
}

func displayRowSet(rowSet string) string {
	if rowSet == "" {
		return "the answer's root"
	}
	return rowSet
}

// ensureColumn adds a column for a field path when the table has none, or converts it when the
// field's type no longer fits: a conflict becomes JSONB, a text-shaped type that stopped holding
// becomes text. Each change is a schema version.
func (m *Mirror) ensureColumn(ctx context.Context, datum *Datum, table *tableInfo, relative, kind string,
	domains bool, cause string) (*columnInfo, error) {
	column, present := table.columns[relative]
	if !present {
		column = &columnInfo{path: relative, name: columnName(relative), kind: kind, domains: domains}
		statements := []string{"ALTER TABLE " + qualified(table.name) + " ADD COLUMN IF NOT EXISTS " +
			quote(column.name) + " " + sqlType(kind)}
		if kind == infer.TypeTimestamp {
			statements = append(statements, "ALTER TABLE "+qualified(table.name)+" ADD COLUMN IF NOT EXISTS "+
				quote(column.name+"#epoch")+" BIGINT")
		}
		for _, statement := range statements {
			if _, err := m.db.ExecContext(ctx, statement); err != nil {
				return nil, fmt.Errorf("mirror: adding the column %s.%s: %w", table.name, column.name, err)
			}
		}
		if _, err := m.db.ExecContext(ctx, `INSERT INTO opnsense.mirror_column (table_name, path, column_name, type,
			carries_domain, added_at) VALUES (?, ?, ?, ?, CAST(? AS boolean), ?)
			ON CONFLICT (table_name, path) DO NOTHING`, table.name, relative, column.name, kind,
			strconv.FormatBool(domains), m.now()); err != nil {
			return nil, err
		}
		table.columns[relative] = column
		if err := m.recordVersion(ctx, datum.EndpointID, fmt.Sprintf("column %q added to %s: %s", relative,
			table.name, cause)); err != nil {
			return nil, err
		}
		return column, nil
	}
	if column.domains != domains && domains {
		if _, err := m.db.ExecContext(ctx, `UPDATE opnsense.mirror_column SET carries_domain = true
			WHERE table_name = ? AND path = ?`, table.name, relative); err != nil {
			return nil, err
		}
		column.domains = true
	}
	if column.kind == kind || column.kind == infer.TypeJSON {
		return column, nil
	}
	target := infer.TypeJSON
	if sqlType(column.kind) == "text" && sqlType(kind) == "text" {
		target = infer.TypeText
	}
	if column.kind == infer.TypeText && target == infer.TypeText {
		// A text column stays text: a value that is now an address is still text.
		return column, nil
	}
	if sqlType(target) != sqlType(column.kind) {
		using := "to_jsonb(" + quote(column.name) + ")"
		if _, err := m.db.ExecContext(ctx, "ALTER TABLE "+qualified(table.name)+" ALTER COLUMN "+
			quote(column.name)+" TYPE jsonb USING "+using); err != nil {
			return nil, fmt.Errorf("mirror: converting %s.%s to JSONB: %w", table.name, column.name, err)
		}
	}
	if _, err := m.db.ExecContext(ctx, `UPDATE opnsense.mirror_column SET type = ? WHERE table_name = ? AND path = ?`,
		target, table.name, relative); err != nil {
		return nil, err
	}
	previous := column.kind
	column.kind = target
	if err := m.recordVersion(ctx, datum.EndpointID, fmt.Sprintf("column %q of %s converted from %s to %s: a %s value",
		relative, table.name, previous, target, kind)); err != nil {
		return nil, err
	}
	return column, nil
}

// recordVersion records a change of an endpoint's mirrored schema.
func (m *Mirror) recordVersion(ctx context.Context, endpointID int64, cause string) error {
	m.mutex.Lock()
	firewallVersion := m.firewallVersion
	m.mutex.Unlock()
	var version any
	if firewallVersion != "" {
		version = firewallVersion
	}
	_, err := m.db.ExecContext(ctx, `INSERT INTO opnsense.mirror_schema_version
		(endpoint_id, version, changed_at, cause, firewall_version)
		SELECT ?, coalesce(max(version), 0) + 1, ?, ?, ? FROM opnsense.mirror_schema_version WHERE endpoint_id = ?`,
		endpointID, m.now(), cause, version, endpointID)
	if err != nil {
		return fmt.Errorf("mirror: recording a schema version: %w", err)
	}
	return nil
}

// SetFirewallVersion states the OPNsense version the next schema changes are recorded under.
func (m *Mirror) SetFirewallVersion(version string) {
	m.mutex.Lock()
	m.firewallVersion = version
	m.mutex.Unlock()
}

// GrantRead gives the read role SELECT on one table of the mirror's schema.
func (m *Mirror) GrantRead(ctx context.Context, table string) error {
	return m.store.GrantReadOnTable(ctx, Schema, table)
}

// Purge removes what is older than the horizon, to the exact second (A10, "Retention"): a row no
// answer held since it, a record past it on its cursor, a history entry written before it. A datum
// whose last answer is older keeps nothing current, and is fetched again at its next request.
func (m *Mirror) Purge(ctx context.Context, horizon int64) error {
	rows, err := m.db.QueryContext(ctx, `SELECT name FROM opnsense.mirror_table ORDER BY name`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return err
		}
		tables = append(tables, name)
	}
	_ = rows.Close()
	for _, table := range tables {
		if err := m.bringUpToDate(ctx, table); err != nil {
			return err
		}
		// A row's instant: a record's own time when its cursor is a timestamp; when a record
		// stream was received, for any other cursor -- a sequence number is no instant; and for
		// a snapshot, the last answer that held it: the datum's newest while that one holds it.
		if _, err := m.db.ExecContext(ctx, "DELETE FROM "+qualified(table)+` AS r USING opnsense.mirror_datum AS d
			WHERE d.id = r."#datum" AND (CASE
				WHEN d.cursor_type = 'timestamp' AND r."#cursor" IS NOT NULL THEN r."#cursor"
				WHEN d.nature = 'record_stream' THEN CAST(r."#first_seen" AS double precision)
				WHEN r."#held" THEN CAST(coalesce(d.fetched_at, r."#last_seen") AS double precision)
				ELSE CAST(r."#last_seen" AS double precision) END) < ?`, horizon); err != nil {
			return fmt.Errorf("mirror: purging %s: %w", table, err)
		}
		history := identifier(table + "#history")
		var exists bool
		if err := m.db.QueryRowContext(ctx, `SELECT to_regclass(?) IS NOT NULL`, Schema+"."+quote(history)).
			Scan(&exists); err == nil && exists {
			if _, err := m.db.ExecContext(ctx, "DELETE FROM "+qualified(history)+` WHERE "#changed_at" < ?`,
				horizon); err != nil {
				return fmt.Errorf("mirror: purging %s: %w", history, err)
			}
		}
	}
	_, err = m.db.ExecContext(ctx, `DELETE FROM opnsense.mirror_gap WHERE to_at < ?`, horizon)
	return err
}
