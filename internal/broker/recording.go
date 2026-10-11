package broker

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/broker/mirror"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// Recording a stream (A14, "Recording").
//
// When at least one loaded plugin reads fields of a stream's events, those fields go into a
// TimescaleDB hypertable of the `opnsense` schema, timestamped at reception: its detail is kept for
// a short retention (mirror_stream_detail_retention_seconds), and two tables per stream keep, for
// each numeric field read, its minimum, maximum, sum, last value and event count per minute and per
// hour, for retention_seconds. Both are maintained by opnview as each event is received, on its own
// clock: no TimescaleDB background job and no continuous aggregate runs (docs/architecture.md,
// "Storage": the Apache-2.0 edition alone). A minute with no event has no row: a gap, never a zero.

// recorder holds the stream tables made so far.
type recorder struct {
	mutex  sync.Mutex
	fields map[string][]string // stream key → field paths read
	ready  map[string]map[string]bool
	// kinds are the detail columns' types, by table and path: numeric or text.
	kinds map[string]map[string]string
}

// streamTable is a stream's detail table.
func streamTable(endpoint opnsense.Endpoint) string {
	return mirror.TableName(mirror.Endpoint{Path: endpoint.Path, Method: endpoint.Method}, "") + "__events"
}

// StreamDone records what a reader read of a stream's events: a field read makes the stream feed a
// table, which keeps it open while the reader is loaded.
func (b *Broker) StreamDone(ctx context.Context, requester Requester, endpoint opnsense.Endpoint,
	options opnsense.RequestOptions, reads *answer.Reads) {
	if b.mirror == nil || reads == nil || reads.Empty() {
		return
	}
	key := streamKey(endpoint, options)
	datum, err := b.mirror.Datum(ctx, mirror.Endpoint{Path: endpoint.Path, Method: endpoint.Method,
		Encoding: encodingOf(endpoint)}, map[string]any{"#stream": key}, b.firewallIdentity())
	if err != nil {
		b.options.Log(fmt.Sprintf("recording the reads of %s: %v", key, err))
		return
	}
	if _, err := b.mirror.RecordReads(ctx, datum.ID, mirror.Read{Reader: requester.Name,
		ReaderVersion: requester.Version, Fields: reads.Fields(), Whole: reads.WholePaths()}); err != nil {
		b.options.Log(fmt.Sprintf("recording the reads of %s: %v", key, err))
		return
	}
	var fields []string
	for _, path := range reads.Fields() {
		if path != "" {
			fields = append(fields, path)
		}
	}
	if len(fields) == 0 {
		return
	}
	b.recorder.mutex.Lock()
	known := map[string]bool{}
	for _, path := range b.recorder.fields[key] {
		known[path] = true
	}
	for _, path := range fields {
		if !known[path] {
			b.recorder.fields[key] = append(b.recorder.fields[key], path)
		}
	}
	sort.Strings(b.recorder.fields[key])
	b.recorder.mutex.Unlock()
	b.Feed(requester.Name, endpoint, options, true)
}

// recordEvent writes one event's read fields into its stream's hypertable and its aggregates.
func (b *Broker) recordEvent(key string, endpoint opnsense.Endpoint, event StreamEvent) {
	if event.Value == nil {
		return
	}
	b.recorder.mutex.Lock()
	fields := append([]string(nil), b.recorder.fields[key]...)
	b.recorder.mutex.Unlock()
	if len(fields) == 0 {
		return
	}
	ctx := b.baseContext()
	table := streamTable(endpoint)
	if err := b.ensureStreamTables(ctx, table, fields, event.Value); err != nil {
		b.options.Log(fmt.Sprintf("recording %s: %v", key, err))
		return
	}
	columns := []string{`"#stream"`, `"#received_at"`}
	values := []string{"?", "?"}
	arguments := []any{key, event.ReceivedAt}
	numeric := map[string]string{}
	for _, path := range fields {
		value := valueAt(event.Value, path)
		columns = append(columns, quoteName(path))
		values = append(values, "CAST(? AS "+b.recorder.kindOf(table, path)+")")
		if value == nil || value.Kind == answer.Null {
			arguments = append(arguments, nil)
			continue
		}
		if value.Scalar() {
			if b.recorder.kindOf(table, path) == "numeric" && value.Kind != answer.Number {
				arguments = append(arguments, nil)
				continue
			}
			arguments = append(arguments, value.Text)
			if value.Kind == answer.Number {
				numeric[path] = value.Text
			}
		} else {
			arguments = append(arguments, string(value.Marshal()))
		}
	}
	if _, err := b.mirror.Exec(ctx, "INSERT INTO "+quotedTable(table)+" ("+strings.Join(columns, ", ")+
		") VALUES ("+strings.Join(values, ", ")+")", arguments...); err != nil {
		b.options.Log(fmt.Sprintf("recording an event of %s: %v", key, err))
		return
	}
	for _, period := range []struct {
		suffix string
		width  int64
	}{{"#minute", 60}, {"#hour", 3600}} {
		bucket := event.ReceivedAt - event.ReceivedAt%period.width
		paths := make([]string, 0, len(numeric))
		for path := range numeric {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		insertColumns := []string{`"#stream"`, `"#bucket"`, `"#count"`, `"#last_at"`}
		insertValues := []string{"?", "?", "1", "?"}
		updates := []string{`"#count" = ` + quotedTable(table+period.suffix) + `."#count" + 1`,
			`"#last_at" = greatest(` + quotedTable(table+period.suffix) + `."#last_at", excluded."#last_at")`}
		arguments := []any{key, bucket, event.ReceivedAt}
		for _, path := range paths {
			for _, statistic := range []string{"min", "max", "sum", "last"} {
				name := quoteName(path + "#" + statistic)
				insertColumns = append(insertColumns, name)
				insertValues = append(insertValues, "CAST(? AS numeric)")
				arguments = append(arguments, numeric[path])
				current := quotedTable(table+period.suffix) + "." + name
				switch statistic {
				case "min":
					updates = append(updates, name+" = least("+current+", excluded."+name+")")
				case "max":
					updates = append(updates, name+" = greatest("+current+", excluded."+name+")")
				case "sum":
					updates = append(updates, name+" = coalesce("+current+", 0) + excluded."+name)
				case "last":
					updates = append(updates, name+" = excluded."+name)
				}
			}
			insertColumns = append(insertColumns, quoteName(path+"#count"))
			insertValues = append(insertValues, "1")
			updates = append(updates, quoteName(path+"#count")+" = coalesce("+quotedTable(table+period.suffix)+"."+
				quoteName(path+"#count")+", 0) + 1")
		}
		statement := "INSERT INTO " + quotedTable(table+period.suffix) + " (" + strings.Join(insertColumns, ", ") +
			") VALUES (" + strings.Join(insertValues, ", ") + `) ON CONFLICT ("#stream", "#bucket") DO UPDATE SET ` +
			strings.Join(updates, ", ")
		if _, err := b.mirror.Exec(ctx, statement, arguments...); err != nil {
			b.options.Log(fmt.Sprintf("aggregating an event of %s: %v", key, err))
		}
	}
}

// valueAt is the value at a path relative to an event's root.
func valueAt(value *answer.Value, path string) *answer.Value {
	current := value
	for _, step := range answer.Steps(path) {
		if current == nil || step == answer.AnyItem || step == answer.AnyMember {
			return nil
		}
		current = current.Field(step)
	}
	return current
}

func quoteName(name string) string {
	if len(name) > 63 {
		name = mirror.TableName(mirror.Endpoint{Path: "/" + name}, "")
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func quotedTable(name string) string { return `"opnsense".` + quoteName(name) }

// ensureStreamTables creates a stream's hypertable and its two aggregate tables, and the columns
// of the fields read.
func (b *Broker) ensureStreamTables(ctx context.Context, table string, fields []string, sample *answer.Value) error {
	b.recorder.mutex.Lock()
	ready := b.recorder.ready[table]
	if ready == nil {
		ready = map[string]bool{}
		b.recorder.ready[table] = ready
	}
	var missing []string
	for _, path := range fields {
		if !ready[path] {
			missing = append(missing, path)
		}
	}
	created := ready["#table"]
	b.recorder.mutex.Unlock()
	if created && len(missing) == 0 {
		return nil
	}
	if !created {
		statements := []string{
			"CREATE TABLE IF NOT EXISTS " + quotedTable(table) + ` ("#stream" TEXT NOT NULL, "#received_at" BIGINT NOT NULL)`,
			"SELECT create_hypertable(CAST(? AS regclass), by_range('#received_at', 86400), if_not_exists => true)",
			"CREATE INDEX IF NOT EXISTS " + quoteName(table+"#stream") + " ON " + quotedTable(table) +
				` ("#stream", "#received_at")`,
		}
		for _, suffix := range []string{"#minute", "#hour"} {
			statements = append(statements, "CREATE TABLE IF NOT EXISTS "+quotedTable(table+suffix)+
				` ("#stream" TEXT NOT NULL, "#bucket" BIGINT NOT NULL, "#count" BIGINT NOT NULL,
				"#last_at" BIGINT NOT NULL, PRIMARY KEY ("#stream", "#bucket"))`)
		}
		for _, statement := range statements {
			var err error
			if strings.Contains(statement, "create_hypertable") {
				_, err = b.mirror.Exec(ctx, statement, `"opnsense".`+quoteName(table))
			} else {
				_, err = b.mirror.Exec(ctx, statement)
			}
			if err != nil {
				return fmt.Errorf("creating the stream table %s: %w", table, err)
			}
		}
		for _, name := range []string{table, table + "#minute", table + "#hour"} {
			if err := b.mirror.GrantRead(ctx, name); err != nil {
				return err
			}
		}
	}
	for _, path := range missing {
		value := valueAt(sample, path)
		kind := "text"
		if value != nil && value.Kind == answer.Number {
			kind = "numeric"
		}
		statements := []string{"ALTER TABLE " + quotedTable(table) + " ADD COLUMN IF NOT EXISTS " + quoteName(path) +
			" " + kind}
		if kind == "numeric" {
			for _, suffix := range []string{"#minute", "#hour"} {
				for _, statistic := range []string{"min", "max", "sum", "last"} {
					statements = append(statements, "ALTER TABLE "+quotedTable(table+suffix)+" ADD COLUMN IF NOT EXISTS "+
						quoteName(path+"#"+statistic)+" numeric")
				}
				statements = append(statements, "ALTER TABLE "+quotedTable(table+suffix)+" ADD COLUMN IF NOT EXISTS "+
					quoteName(path+"#count")+" bigint")
			}
		}
		for _, statement := range statements {
			if _, err := b.mirror.Exec(ctx, statement); err != nil {
				return fmt.Errorf("adding %s to the stream table %s: %w", path, table, err)
			}
		}
	}
	b.recorder.mutex.Lock()
	ready["#table"] = true
	for _, path := range missing {
		ready[path] = true
	}
	if b.recorder.kinds[table] == nil {
		b.recorder.kinds[table] = map[string]string{}
	}
	for _, path := range missing {
		kind := "text"
		if value := valueAt(sample, path); value != nil && value.Kind == answer.Number {
			kind = "numeric"
		}
		b.recorder.kinds[table][path] = kind
	}
	b.recorder.mutex.Unlock()
	return nil
}

// streamRetention is the default detail retention of a stream hypertable: one day, about 86 400
// events of a stream at one per second, a few megabytes for the processor stream's fields.
const streamRetention = 86400

// PurgeStreams removes stream detail older than its retention, by whole chunks then rows to the
// exact second, and aggregates older than the installation's retention.
func (b *Broker) PurgeStreams(ctx context.Context, now, detailRetention, retention int64) error {
	if b.mirror == nil {
		return nil
	}
	if detailRetention <= 0 {
		detailRetention = streamRetention
	}
	rows, err := b.mirror.Query(ctx, `SELECT c.relname FROM pg_class AS c JOIN pg_namespace AS n ON n.oid = c.relnamespace
		WHERE n.nspname = 'opnsense' AND c.relkind = 'r' AND right(c.relname, 8) = '__events'`)
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
	horizon := now - detailRetention
	for _, table := range tables {
		if _, err := b.mirror.Exec(ctx, "SELECT drop_chunks(CAST(? AS regclass), older_than => CAST(? AS bigint))",
			`"opnsense".`+quoteName(table), horizon); err != nil {
			return fmt.Errorf("dropping the old chunks of %s: %w", table, err)
		}
		if _, err := b.mirror.Exec(ctx, "DELETE FROM "+quotedTable(table)+` WHERE "#received_at" < ?`, horizon); err != nil {
			return err
		}
		if retention > 0 {
			for _, suffix := range []string{"#minute", "#hour"} {
				if _, err := b.mirror.Exec(ctx, "DELETE FROM "+quotedTable(table+suffix)+` WHERE "#bucket" < ?`,
					now-retention); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// kindOf is a detail column's type.
func (r *recorder) kindOf(table, path string) string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if kind := r.kinds[table][path]; kind != "" {
		return kind
	}
	return "text"
}
