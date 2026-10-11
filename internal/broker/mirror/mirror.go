// Package mirror is the learned mirror of the firewall API (A10 and A11 of the broker amendment
// to specs/SPEC-backend-completion-plugins.md): the PostgreSQL schema `opnsense`, holding what the
// broker observed of the firewall's answers, field for field as the firewall wrote them, and only
// the fields some reader read.
//
// THE TABLES ARE LEARNED. Nothing names an endpoint in advance. The metadata tables are the core
// schema's (internal/store/schema.sql); the data tables are created here, one per endpoint and row
// set, with one column per field path a reader read, and altered as readers read more: a new field
// is a new column, a type conflict converts a column to JSONB without loss, and every change records
// a schema version. Nothing is dropped: a column nobody reads any more stops being filled and is
// kept, with its rows, until retention.
//
// A FAILED MIRROR WRITE NEVER FAILS A REQUEST. The broker serves the firewall's answer whatever
// happens here, logs the failure, and treats the datum as not mirrored.
package mirror

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/store"
)

// Schema is the mirror's PostgreSQL schema. No plugin schema can take its name: theirs carry
// the plugin prefix.
const Schema = "opnsense"

// Endpoint names one firewall endpoint as the mirror keys it.
type Endpoint struct {
	Path     string
	Method   string
	Encoding string
}

// Datum is one endpoint with one combination of identifying parameters, from one firewall.
type Datum struct {
	ID         int64
	EndpointID int64
	Endpoint   Endpoint
	Identity   map[string]any
	Firewall   string

	Nature           string
	RowSet           string
	Key              []string
	Cursor           []string
	CursorType       string
	CursorDescending bool
	DedupeOnContent  bool

	Generation  int64
	FetchedAt   int64
	AttemptedAt int64
	Outcome     string
	StatusCode  int
	Detail      string
	PageSize    int64
	ReadWhole   bool
	Body        string
	// RootKind is the last answer's root type.
	RootKind answer.Kind
	// storedPaths are the field paths the last write stored, and since the generation each
	// became stored: a row written before does not hold it.
	storedPaths map[string]int64
	wholePaths  map[string]int64
}

// Mirror is the mirror over one store.
type Mirror struct {
	store *store.Store
	db    *sql.DB
	now   func() int64

	mutex     sync.Mutex
	endpoints map[Endpoint]int64
	// writing serialises the writes of one endpoint's tables.
	writing map[int64]*sync.Mutex
	// readers are the readers loaded now, by name, with their version; a reader absent here no
	// longer decides what is stored. Core readers are always loaded.
	readers map[string]string
	// firewallVersion is the OPNsense version schema changes are recorded under.
	firewallVersion string
	// current are the data tables known to carry every bookkeeping column (tables.go).
	current map[string]bool
}

// New returns the mirror over a store. now is opnview's clock.
func New(database *store.Store, now func() int64) *Mirror {
	return &Mirror{store: database, db: database.DB(), now: now, endpoints: map[Endpoint]int64{},
		writing: map[int64]*sync.Mutex{}, current: map[string]bool{}}
}

// SetLoadedReaders states which plugins are loaded, by name and version. The fields only an
// unloaded plugin read are no longer stored.
func (m *Mirror) SetLoadedReaders(readers map[string]string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.readers = readers
}

// loaded reports whether a reader's reads decide what is stored.
func (m *Mirror) loaded(reader, version string) bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if strings.HasPrefix(reader, CoreReaderPrefix) || m.readers == nil {
		return true
	}
	held, present := m.readers[reader]
	return present && (held == version || version == "")
}

// CoreReaderPrefix begins the name of a core reader: discovery, the probe round, a coordinator.
const CoreReaderPrefix = "core:"

// encodingName names a body encoding.
func encodingName(encoding string) string {
	if encoding == "" {
		return "none"
	}
	return encoding
}

// EndpointID returns an endpoint's row, creating it at its first sighting.
func (m *Mirror) EndpointID(ctx context.Context, endpoint Endpoint) (int64, error) {
	endpoint.Encoding = encodingName(endpoint.Encoding)
	m.mutex.Lock()
	id, found := m.endpoints[endpoint]
	m.mutex.Unlock()
	if found {
		return id, nil
	}
	err := m.db.QueryRowContext(ctx, `INSERT INTO opnsense.mirror_endpoint (path, method, encoding, first_seen_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (path, method) DO UPDATE SET encoding = excluded.encoding
		RETURNING id`, endpoint.Path, endpoint.Method, endpoint.Encoding, m.now()).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("mirror: registering %s %s: %w", endpoint.Method, endpoint.Path, err)
	}
	m.mutex.Lock()
	m.endpoints[endpoint] = id
	m.mutex.Unlock()
	return id, nil
}

// IdentityKey is the canonical text of a datum's identifying parameters.
func IdentityKey(identity map[string]any) string {
	encoded, _ := json.Marshal(sortedIdentity(identity))
	return string(encoded)
}

// sortedIdentity renders an identity with its keys in order, so two equal identities encode
// alike.
func sortedIdentity(identity map[string]any) [][2]any {
	keys := make([]string, 0, len(identity))
	for key := range identity {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([][2]any, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, [2]any{key, identity[key]})
	}
	return pairs
}

// FirewallIdentity is the non-secret identity of a firewall: a digest of its base URL, never the
// URL itself.
func FirewallIdentity(baseURL string) string {
	sum := sha256.Sum256([]byte(strings.TrimRight(strings.ToLower(baseURL), "/")))
	return hex.EncodeToString(sum[:8])
}

// Datum returns a datum, creating its row at its first request.
func (m *Mirror) Datum(ctx context.Context, endpoint Endpoint, identity map[string]any, firewall string) (
	*Datum, error) {
	endpointID, err := m.EndpointID(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	key := IdentityKey(identity)
	encoded, _ := json.Marshal(identity)
	var id int64
	if err := m.db.QueryRowContext(ctx, `INSERT INTO opnsense.mirror_datum (endpoint_id, identity, identity_key, firewall)
		VALUES (?, CAST(? AS jsonb), ?, ?)
		ON CONFLICT (endpoint_id, identity_key, firewall) DO UPDATE SET identity = excluded.identity
		RETURNING id`, endpointID, string(encoded), key, firewall).Scan(&id); err != nil {
		return nil, fmt.Errorf("mirror: registering a datum of %s: %w", endpoint.Path, err)
	}
	datum, err := m.LoadDatum(ctx, id)
	if err != nil {
		return nil, err
	}
	datum.Endpoint = Endpoint{Path: endpoint.Path, Method: endpoint.Method, Encoding: encodingName(endpoint.Encoding)}
	return datum, nil
}

// LoadDatum reads a datum's row.
func (m *Mirror) LoadDatum(ctx context.Context, id int64) (*Datum, error) {
	datum := &Datum{ID: id}
	var (
		identity, key, cursor, rootShape sql.NullString
		nature, rowSet, cursorType       sql.NullString
		fetchedAt, attemptedAt           sql.NullInt64
		outcome, detail, body            sql.NullString
		statusCode, pageSize             sql.NullInt64
		path, method, encoding           string
	)
	err := m.db.QueryRowContext(ctx, `SELECT d.endpoint_id, e.path, e.method, e.encoding, CAST(d.identity AS text),
		d.firewall, d.nature, d.row_set, CAST(d.key AS text), CAST(d.cursor AS text), d.cursor_type,
		d.cursor_descending, d.dedupe_on_content, d.generation, d.fetched_at, d.attempted_at, d.outcome,
		d.status_code, d.detail, CAST(d.root_shape AS text), d.page_size, d.read_whole, d.body
		FROM opnsense.mirror_datum AS d JOIN opnsense.mirror_endpoint AS e ON e.id = d.endpoint_id
		WHERE d.id = ?`, id).Scan(&datum.EndpointID, &path, &method, &encoding, &identity, &datum.Firewall, &nature,
		&rowSet, &key, &cursor, &cursorType, &datum.CursorDescending, &datum.DedupeOnContent, &datum.Generation,
		&fetchedAt, &attemptedAt, &outcome, &statusCode, &detail, &rootShape, &pageSize, &datum.ReadWhole, &body)
	if err != nil {
		return nil, fmt.Errorf("mirror: reading the datum %d: %w", id, err)
	}
	datum.Endpoint = Endpoint{Path: path, Method: method, Encoding: encoding}
	datum.Nature, datum.RowSet, datum.CursorType = nature.String, rowSet.String, cursorType.String
	datum.FetchedAt, datum.AttemptedAt = fetchedAt.Int64, attemptedAt.Int64
	datum.Outcome, datum.Detail, datum.Body = outcome.String, detail.String, body.String
	datum.StatusCode, datum.PageSize = int(statusCode.Int64), pageSize.Int64
	if identity.Valid {
		_ = json.Unmarshal([]byte(identity.String), &datum.Identity)
	}
	if key.Valid {
		_ = json.Unmarshal([]byte(key.String), &datum.Key)
	}
	if cursor.Valid {
		_ = json.Unmarshal([]byte(cursor.String), &datum.Cursor)
	}
	datum.storedPaths, datum.wholePaths = map[string]int64{}, map[string]int64{}
	if rootShape.Valid {
		var shape struct {
			Root   int              `json:"root"`
			Stored map[string]int64 `json:"stored"`
			Whole  map[string]int64 `json:"whole"`
		}
		if json.Unmarshal([]byte(rootShape.String), &shape) == nil {
			datum.RootKind = answer.Kind(shape.Root)
			if shape.Stored != nil {
				datum.storedPaths = shape.Stored
			}
			if shape.Whole != nil {
				datum.wholePaths = shape.Whole
			}
		}
	}
	return datum, nil
}

// Read is one reader's reading of a datum, as the broker records it.
type Read struct {
	Reader        string
	ReaderVersion string
	MaxAgeSeconds float64
	Fields        []string
	Whole         []string
}

// RecordReads records which field paths a reader read of a datum. It returns the paths no loaded
// reader had read before: each becomes stored from the next call.
func (m *Mirror) RecordReads(ctx context.Context, datumID int64, read Read) ([]string, error) {
	if len(read.Fields) == 0 && len(read.Whole) == 0 {
		return nil, nil
	}
	known, knownWhole, err := m.readPaths(ctx, datumID)
	if err != nil {
		return nil, err
	}
	now := m.now()
	var added []string
	write := func(path string, whole bool) error {
		_, err := m.db.ExecContext(ctx, `INSERT INTO opnsense.mirror_read
			(datum_id, reader, reader_version, path, whole, max_age_seconds, first_read_at, last_read_at)
			VALUES (?, ?, ?, ?, CAST(? AS boolean), ?, ?, ?)
			ON CONFLICT (datum_id, reader, reader_version, path, whole) DO UPDATE
			SET last_read_at = excluded.last_read_at, max_age_seconds = excluded.max_age_seconds`,
			datumID, read.Reader, read.ReaderVersion, path, strconv.FormatBool(whole), read.MaxAgeSeconds, now, now)
		return err
	}
	for _, path := range read.Fields {
		if !known[path] && !answer.Covers(nil, keysOf(knownWhole), path) {
			added = append(added, path)
		}
		if err := write(path, false); err != nil {
			return nil, fmt.Errorf("mirror: recording a read: %w", err)
		}
	}
	for _, path := range read.Whole {
		if !knownWhole[path] && !answer.Covers(nil, keysOf(knownWhole), path) {
			added = append(added, path)
		}
		if err := write(path, true); err != nil {
			return nil, fmt.Errorf("mirror: recording a read: %w", err)
		}
	}
	return added, nil
}

// readPaths returns the paths the loaded readers have read of a datum.
func (m *Mirror) readPaths(ctx context.Context, datumID int64) (map[string]bool, map[string]bool, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT reader, reader_version, path, whole FROM opnsense.mirror_read
		WHERE datum_id = ?`, datumID)
	if err != nil {
		return nil, nil, fmt.Errorf("mirror: reading the reads: %w", err)
	}
	defer func() { _ = rows.Close() }()
	fields, whole := map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var reader, version, path string
		var isWhole bool
		if err := rows.Scan(&reader, &version, &path, &isWhole); err != nil {
			return nil, nil, err
		}
		if !m.loaded(reader, version) {
			continue
		}
		if isWhole {
			whole[path] = true
		} else {
			fields[path] = true
		}
	}
	return fields, whole, rows.Err()
}

func keysOf(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Attempt records a call that did not answer OK: it counts for freshness, and replaces no row.
func (m *Mirror) Attempt(ctx context.Context, datum *Datum, at int64, outcome string, status int, detail string) error {
	_, err := m.db.ExecContext(ctx, `UPDATE opnsense.mirror_datum
		SET attempted_at = ?, outcome = ?, status_code = ?, detail = ? WHERE id = ?`,
		at, outcome, int64(status), detail, datum.ID)
	if err != nil {
		return fmt.Errorf("mirror: recording an attempt: %w", err)
	}
	datum.AttemptedAt, datum.Outcome, datum.StatusCode, datum.Detail = at, outcome, status, detail
	return nil
}

// Gap is an interval a datum or a stream is known not to cover.
type Gap struct {
	From   int64  `json:"from"`
	To     int64  `json:"to"`
	Reason string `json:"reason"`
}

// The reasons of a gap.
const (
	GapAnswerNewerThanStored = "answer_newer_than_stored"
	GapCallFailed            = "call_failed"
	GapStreamCut             = "stream_cut"
)

// RecordGap records a gap of a datum.
func (m *Mirror) RecordGap(ctx context.Context, datumID int64, gap Gap) error {
	if gap.To < gap.From {
		return nil
	}
	_, err := m.db.ExecContext(ctx, `INSERT INTO opnsense.mirror_gap (datum_id, from_at, to_at, reason, recorded_at)
		VALUES (?, ?, ?, ?, ?)`, datumID, gap.From, gap.To, gap.Reason, m.now())
	return err
}

// Gaps returns the gaps of a datum meeting an interval.
func (m *Mirror) Gaps(ctx context.Context, datumID, from, to int64) ([]Gap, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT from_at, to_at, reason FROM opnsense.mirror_gap
		WHERE datum_id = ? AND to_at >= ? AND from_at <= ? ORDER BY from_at`, datumID, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	gaps := []Gap{}
	for rows.Next() {
		var gap Gap
		if err := rows.Scan(&gap.From, &gap.To, &gap.Reason); err != nil {
			return nil, err
		}
		gaps = append(gaps, gap)
	}
	return gaps, rows.Err()
}

// ErrNotMirrored is a datum the mirror holds no answer of.
var ErrNotMirrored = errors.New("mirror: the datum holds no answer")

// Exec runs a statement on the mirror's metadata, for the broker's counters.
func (m *Mirror) Exec(ctx context.Context, query string, arguments ...any) (sql.Result, error) {
	return m.db.ExecContext(ctx, query, arguments...)
}

// QueryRow reads one row of the mirror's metadata.
func (m *Mirror) QueryRow(ctx context.Context, query string, arguments ...any) *sql.Row {
	return m.db.QueryRowContext(ctx, query, arguments...)
}

// Query reads rows of the mirror's metadata, for the catalogue.
func (m *Mirror) Query(ctx context.Context, query string, arguments ...any) (*sql.Rows, error) {
	return m.db.QueryContext(ctx, query, arguments...)
}

// NewestCursor is the newest cursor a record stream's rows hold.
func (m *Mirror) NewestCursor(ctx context.Context, datum *Datum) (float64, bool) {
	if datum.Generation == 0 || datum.RowSet == "" {
		return 0, false
	}
	var newest *float64
	if err := m.db.QueryRowContext(ctx, `SELECT max("#cursor") FROM `+qualified(TableName(datum.Endpoint,
		datum.RowSet))+` WHERE "#datum" = ?`, datum.ID).Scan(&newest); err != nil || newest == nil {
		return 0, false
	}
	return *newest, true
}

// SetParameter records a parameter's role, who corrected it, and whether a window is honoured.
func (m *Mirror) SetParameter(ctx context.Context, endpointID int64, name, role, correctedBy string,
	honoured *bool) error {
	var corrected, held any
	if correctedBy != "" {
		corrected = correctedBy
	}
	if honoured != nil {
		held = strconv.FormatBool(*honoured)
	}
	_, err := m.db.ExecContext(ctx, `INSERT INTO opnsense.mirror_parameter (endpoint_id, name, role, corrected_by, honoured)
		VALUES (?, ?, ?, ?, CAST(? AS boolean))
		ON CONFLICT (endpoint_id, name) DO UPDATE SET role = excluded.role, corrected_by = excluded.corrected_by,
		honoured = coalesce(excluded.honoured, opnsense.mirror_parameter.honoured)`,
		endpointID, name, role, corrected, held)
	return err
}

// Honoured reads whether a window parameter is honoured: nil when it was never tried.
func (m *Mirror) Honoured(ctx context.Context, endpointID int64, name string) *bool {
	var honoured *bool
	if err := m.db.QueryRowContext(ctx, `SELECT honoured FROM opnsense.mirror_parameter WHERE endpoint_id = ? AND name = ?`,
		endpointID, name).Scan(&honoured); err != nil {
		return nil
	}
	return honoured
}
