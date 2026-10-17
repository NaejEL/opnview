package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/NaejEL/opnview/internal/push"
)

// The learned API catalogue (A15 of the broker amendment): what the broker learned of every
// endpoint it called. It is served as JSON to the authenticated user (docs/widget-api.md, "API
// catalogue") and to plugins through host.catalogue(), and it NEVER CONTAINS A FIELD VALUE, A
// CREDENTIAL, THE BASE URL OR AN AUTHORISATION HEADER: names, roles, types, counts and timings
// only. A third-party author reads it before writing a line.

// Catalogue is the whole catalogue.
type Catalogue struct {
	Endpoints     []CatalogueEndpoint `json:"endpoints"`
	Subscriptions []SubscriptionState `json:"subscriptions"`
	Calls         int64               `json:"calls"`
	MirrorHits    int64               `json:"mirror_hits"`
	// HitRatio is the share of requests the mirror answered.
	HitRatio     *float64 `json:"mirror_hit_ratio"`
	WriteRefused int64    `json:"write_refused"`
	// Push is what opnview receives rather than calls (PS13 of the push amendment): its listeners,
	// its push declarations and the senders it refused. Inbound, so it is not a firewall call.
	Push push.Catalogue `json:"push"`
}

// SetPushCatalogue gives the catalogue its push part, read each time the catalogue is.
func (b *Broker) SetPushCatalogue(source func() push.Catalogue) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.pushCatalogue = source
}

// pushPart is the push part of the catalogue: the hub's, or, with no hub, no listener and no
// declaration routed.
func (b *Broker) pushPart() push.Catalogue {
	b.mutex.Lock()
	source := b.pushCatalogue
	b.mutex.Unlock()
	if source != nil {
		return source()
	}
	return push.NewHub(push.Options{}).Catalogue()
}

// CatalogueEndpoint is one endpoint.
type CatalogueEndpoint struct {
	Path     string `json:"path"`
	Method   string `json:"method"`
	Encoding string `json:"encoding"`
	// ObservedStream is what its answers' headers showed, DeclaredStream what a manifest said;
	// StreamMismatch is a disagreement.
	ObservedStream bool `json:"observed_stream"`
	DeclaredStream bool `json:"declared_stream"`
	StreamMismatch bool `json:"stream_mismatch"`
	// SurveySection cites a built-in's endpoint; Uncited lists the third-party readers that read
	// it with no upstream citation.
	SurveySection string               `json:"survey_section,omitempty"`
	Uncited       []string             `json:"uncited"`
	Parameters    []CatalogueParameter `json:"parameters"`
	Data          []CatalogueDatum     `json:"data"`
	Cost          CallCost             `json:"call_cost"`
	MirrorHits    int64                `json:"mirror_hits"`
	HitRatio      *float64             `json:"mirror_hit_ratio"`
	FloorSeconds  float64              `json:"freshness_floor_seconds"`
	WriteRefused  int64                `json:"write_refused"`
	Versions      []SchemaVersion      `json:"schema_versions"`
	Corrections   []AppliedCorrection  `json:"corrections"`
	Conflicts     map[string]string    `json:"correction_conflicts"`
	// EventIntervalMS and CutoffSeconds describe a stream: the median interval between its
	// events, and the longest a connection lasted before the server cut it.
	EventIntervalMS *float64 `json:"event_interval_ms"`
	CutoffSeconds   *float64 `json:"stream_cutoff_seconds"`
}

// CatalogueParameter is one request parameter.
type CatalogueParameter struct {
	Name        string `json:"name"`
	Role        string `json:"role"`
	CorrectedBy string `json:"corrected_by,omitempty"`
	// Honoured is null for a parameter whose window was never tried.
	Honoured *bool `json:"honoured"`
}

// CatalogueDatum is one datum: the names of its identifying parameters, never their values.
type CatalogueDatum struct {
	ID              int64             `json:"id"`
	IdentifiedBy    []string          `json:"identified_by"`
	Nature          string            `json:"nature"`
	RowSet          string            `json:"rows"`
	Key             []string          `json:"key"`
	Cursor          []string          `json:"cursor"`
	CursorType      string            `json:"cursor_type,omitempty"`
	Generations     int64             `json:"generations"`
	Changes         int64             `json:"changes"`
	ChangeFrequency *float64          `json:"change_frequency"`
	FetchedAt       *int64            `json:"fetched_at"`
	ReadWhole       bool              `json:"read_whole"`
	Tables          []CatalogueTable  `json:"tables"`
	Readers         []CatalogueReader `json:"readers"`
}

// CatalogueTable is one mirror table.
type CatalogueTable struct {
	Name    string            `json:"name"`
	RowSet  string            `json:"rows"`
	Columns []CatalogueColumn `json:"columns"`
}

// CatalogueColumn is one mirrored field.
type CatalogueColumn struct {
	Path          string `json:"path"`
	Column        string `json:"column"`
	Type          string `json:"type"`
	CarriesDomain bool   `json:"carries_domain"`
}

// CatalogueReader is one reader of a datum.
type CatalogueReader struct {
	Reader        string   `json:"reader"`
	Version       string   `json:"version"`
	MaxAgeSeconds *float64 `json:"max_age_seconds"`
	Fields        []string `json:"fields"`
	Whole         []string `json:"read_whole"`
}

// CallCost is an endpoint's observed cost.
type CallCost struct {
	Calls       int64    `json:"calls"`
	MedianMS    *float64 `json:"median_ms"`
	P95MS       *float64 `json:"p95_ms"`
	MedianBytes *float64 `json:"median_bytes"`
	P95Bytes    *float64 `json:"p95_bytes"`
}

// SchemaVersion is one change of an endpoint's mirrored schema.
type SchemaVersion struct {
	Version         int64  `json:"version"`
	ChangedAt       int64  `json:"changed_at"`
	Cause           string `json:"cause"`
	FirewallVersion string `json:"firewall_version,omitempty"`
}

// AppliedCorrection is one plugin's corrections of an endpoint.
type AppliedCorrection struct {
	Reader      string          `json:"reader"`
	Corrections json.RawMessage `json:"corrections"`
	Applied     bool            `json:"applied"`
}

// percentile reads a percentile of a recorded list.
func percentile(encoded string, fraction float64) *float64 {
	var values []float64
	if json.Unmarshal([]byte(encoded), &values) != nil || len(values) == 0 {
		return nil
	}
	sort.Float64s(values)
	index := int(fraction * float64(len(values)-1))
	value := values[index]
	return &value
}

// Catalogue assembles the catalogue from the mirror's metadata.
func (b *Broker) Catalogue(ctx context.Context) (Catalogue, error) {
	catalogue := Catalogue{Endpoints: []CatalogueEndpoint{}, Subscriptions: b.Subscriptions(), Push: b.pushPart()}
	if catalogue.Subscriptions == nil {
		catalogue.Subscriptions = []SubscriptionState{}
	}
	if b.mirror == nil {
		return catalogue, nil
	}
	b.recordCorrections(ctx)
	rows, err := b.mirror.Query(ctx, `SELECT id, path, method, encoding, observed_stream, declared_stream, calls,
		mirror_hits, write_refused, CAST(latencies_ms AS text), CAST(sizes_bytes AS text), event_interval_ms,
		stream_cutoff_seconds FROM opnsense.mirror_endpoint ORDER BY path, method`)
	if err != nil {
		return catalogue, fmt.Errorf("broker: reading the catalogue: %w", err)
	}
	type endpointRow struct {
		id int64
		CatalogueEndpoint
		latencies, sizes string
	}
	var endpoints []endpointRow
	for rows.Next() {
		var row endpointRow
		if err := rows.Scan(&row.id, &row.Path, &row.Method, &row.Encoding, &row.ObservedStream, &row.DeclaredStream,
			&row.Cost.Calls, &row.MirrorHits, &row.WriteRefused, &row.latencies, &row.sizes, &row.EventIntervalMS,
			&row.CutoffSeconds); err != nil {
			_ = rows.Close()
			return catalogue, err
		}
		endpoints = append(endpoints, row)
	}
	_ = rows.Close()
	conflicts := b.Conflicts()
	for _, row := range endpoints {
		entry := row.CatalogueEndpoint
		entry.Cost.MedianMS, entry.Cost.P95MS = percentile(row.latencies, 0.5), percentile(row.latencies, 0.95)
		entry.Cost.MedianBytes, entry.Cost.P95Bytes = percentile(row.sizes, 0.5), percentile(row.sizes, 0.95)
		if requests := entry.Cost.Calls + entry.MirrorHits; requests > 0 {
			ratio := float64(entry.MirrorHits) / float64(requests)
			entry.HitRatio = &ratio
		}
		entry.Uncited = []string{}
		entry.Conflicts = map[string]string{}
		if held := b.declarationOf(entry.Path); held != nil {
			entry.FloorSeconds = held.floor.Seconds()
			entry.SurveySection = held.surveySection
			if held.streamFlag != nil {
				entry.DeclaredStream = *held.streamFlag
			}
			for reader := range held.uncited {
				entry.Uncited = append(entry.Uncited, reader)
			}
			sort.Strings(entry.Uncited)
		}
		for reader, code := range conflicts[entry.Path] {
			entry.Conflicts[reader] = code
		}
		if median, found := b.medianLatency(ctx, entry.Path); found && median.Seconds() > entry.FloorSeconds {
			entry.FloorSeconds = median.Seconds()
		}
		entry.StreamMismatch = entry.ObservedStream != entry.DeclaredStream && (entry.ObservedStream ||
			b.declarationOf(entry.Path) != nil && b.declarationOf(entry.Path).streamFlag != nil)
		if entry.Parameters, err = b.catalogueParameters(ctx, row.id, entry.Path); err != nil {
			return catalogue, err
		}
		if entry.Data, err = b.catalogueData(ctx, row.id); err != nil {
			return catalogue, err
		}
		if entry.Versions, err = b.catalogueVersions(ctx, row.id); err != nil {
			return catalogue, err
		}
		if entry.Corrections, err = b.catalogueCorrections(ctx, row.id); err != nil {
			return catalogue, err
		}
		catalogue.Calls += entry.Cost.Calls
		catalogue.MirrorHits += entry.MirrorHits
		catalogue.WriteRefused += entry.WriteRefused
		catalogue.Endpoints = append(catalogue.Endpoints, entry)
	}
	if requests := catalogue.Calls + catalogue.MirrorHits; requests > 0 {
		ratio := float64(catalogue.MirrorHits) / float64(requests)
		catalogue.HitRatio = &ratio
	}
	return catalogue, nil
}

func (b *Broker) catalogueParameters(ctx context.Context, endpointID int64, path string) ([]CatalogueParameter, error) {
	parameters := []CatalogueParameter{}
	rows, err := b.mirror.Query(ctx, `SELECT name, role, coalesce(corrected_by, ''), honoured
		FROM opnsense.mirror_parameter WHERE endpoint_id = $1 ORDER BY name`, endpointID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for rows.Next() {
		var parameter CatalogueParameter
		if err := rows.Scan(&parameter.Name, &parameter.Role, &parameter.CorrectedBy, &parameter.Honoured); err != nil {
			_ = rows.Close()
			return nil, err
		}
		seen[parameter.Name] = true
		parameters = append(parameters, parameter)
	}
	_ = rows.Close()
	if held := b.declarationOf(path); held != nil {
		var names []string
		for name := range held.corrections.Parameters {
			if !seen[name] {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			parameters = append(parameters, CatalogueParameter{Name: name, Role: held.corrections.Parameters[name],
				CorrectedBy: held.correctedBy})
		}
	}
	return parameters, rows.Err()
}

func (b *Broker) catalogueData(ctx context.Context, endpointID int64) ([]CatalogueDatum, error) {
	data := []CatalogueDatum{}
	rows, err := b.mirror.Query(ctx, `SELECT id, CAST(identity AS text), coalesce(nature, ''), coalesce(row_set, ''),
		CAST(coalesce(key, '[]') AS text), CAST(coalesce(cursor, '[]') AS text), coalesce(cursor_type, ''), generation,
		changes, fetched_at, read_whole FROM opnsense.mirror_datum WHERE endpoint_id = $1 AND firewall = $2 ORDER BY id`,
		endpointID, b.firewallIdentity())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var datum CatalogueDatum
		var identity, key, cursor string
		if err := rows.Scan(&datum.ID, &identity, &datum.Nature, &datum.RowSet, &key, &cursor, &datum.CursorType,
			&datum.Generations, &datum.Changes, &datum.FetchedAt, &datum.ReadWhole); err != nil {
			_ = rows.Close()
			return nil, err
		}
		var parameters map[string]any
		_ = json.Unmarshal([]byte(identity), &parameters)
		datum.IdentifiedBy = []string{}
		for name := range parameters {
			datum.IdentifiedBy = append(datum.IdentifiedBy, name)
		}
		sort.Strings(datum.IdentifiedBy)
		_ = json.Unmarshal([]byte(key), &datum.Key)
		_ = json.Unmarshal([]byte(cursor), &datum.Cursor)
		if datum.Key == nil {
			datum.Key = []string{}
		}
		if datum.Cursor == nil {
			datum.Cursor = []string{}
		}
		if datum.Generations > 0 {
			frequency := float64(datum.Changes) / float64(datum.Generations)
			datum.ChangeFrequency = &frequency
		}
		data = append(data, datum)
	}
	_ = rows.Close()
	for index := range data {
		var err error
		if data[index].Tables, err = b.catalogueTables(ctx, endpointID); err != nil {
			return nil, err
		}
		if data[index].Readers, err = b.catalogueReaders(ctx, data[index].ID); err != nil {
			return nil, err
		}
	}
	return data, rows.Err()
}

func (b *Broker) catalogueTables(ctx context.Context, endpointID int64) ([]CatalogueTable, error) {
	tables := []CatalogueTable{}
	rows, err := b.mirror.Query(ctx, `SELECT t.name, t.row_set, c.path, c.column_name, c.type, c.carries_domain
		FROM opnsense.mirror_table AS t LEFT JOIN opnsense.mirror_column AS c ON c.table_name = t.name
		WHERE t.endpoint_id = $1 ORDER BY t.name, c.path`, endpointID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	byName := map[string]int{}
	for rows.Next() {
		var name, rowSet string
		var path, column, kind *string
		var domains *bool
		if err := rows.Scan(&name, &rowSet, &path, &column, &kind, &domains); err != nil {
			return nil, err
		}
		index, found := byName[name]
		if !found {
			tables = append(tables, CatalogueTable{Name: name, RowSet: rowSet, Columns: []CatalogueColumn{}})
			index = len(tables) - 1
			byName[name] = index
		}
		if path != nil {
			entry := CatalogueColumn{Path: *path, Column: *column, Type: *kind}
			if domains != nil {
				entry.CarriesDomain = *domains
			}
			tables[index].Columns = append(tables[index].Columns, entry)
		}
	}
	return tables, rows.Err()
}

func (b *Broker) catalogueReaders(ctx context.Context, datumID int64) ([]CatalogueReader, error) {
	readers := []CatalogueReader{}
	rows, err := b.mirror.Query(ctx, `SELECT reader, reader_version, max(max_age_seconds),
		coalesce(string_agg(path, chr(30) ORDER BY path) FILTER (WHERE NOT whole), ''),
		coalesce(string_agg(path, chr(30) ORDER BY path) FILTER (WHERE whole), '')
		FROM opnsense.mirror_read WHERE datum_id = $1 GROUP BY reader, reader_version ORDER BY reader, reader_version`,
		datumID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	split := func(joined string) []string {
		if joined == "" {
			return []string{}
		}
		var parts []string
		start := 0
		for index := 0; index < len(joined); index++ {
			if joined[index] == 30 {
				parts = append(parts, joined[start:index])
				start = index + 1
			}
		}
		return append(parts, joined[start:])
	}
	for rows.Next() {
		var reader CatalogueReader
		var fields, whole string
		if err := rows.Scan(&reader.Reader, &reader.Version, &reader.MaxAgeSeconds, &fields, &whole); err != nil {
			return nil, err
		}
		reader.Fields, reader.Whole = split(fields), split(whole)
		readers = append(readers, reader)
	}
	return readers, rows.Err()
}

func (b *Broker) catalogueVersions(ctx context.Context, endpointID int64) ([]SchemaVersion, error) {
	versions := []SchemaVersion{}
	rows, err := b.mirror.Query(ctx, `SELECT version, changed_at, cause, coalesce(firewall_version, '')
		FROM opnsense.mirror_schema_version WHERE endpoint_id = $1 ORDER BY version`, endpointID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var version SchemaVersion
		if err := rows.Scan(&version.Version, &version.ChangedAt, &version.Cause, &version.FirewallVersion); err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func (b *Broker) catalogueCorrections(ctx context.Context, endpointID int64) ([]AppliedCorrection, error) {
	corrections := []AppliedCorrection{}
	rows, err := b.mirror.Query(ctx, `SELECT reader, CAST(corrections AS text), applied FROM opnsense.mirror_correction
		WHERE endpoint_id = $1 ORDER BY reader`, endpointID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var correction AppliedCorrection
		var encoded string
		if err := rows.Scan(&correction.Reader, &encoded, &correction.Applied); err != nil {
			return nil, err
		}
		correction.Corrections = json.RawMessage(encoded)
		corrections = append(corrections, correction)
	}
	return corrections, rows.Err()
}

// recordCorrections writes the corrections the loaded plugins declared, and whether each applies.
func (b *Broker) recordCorrections(ctx context.Context) {
	b.mutex.Lock()
	type held struct {
		path, reader string
		corrections  []byte
		applied      bool
	}
	var pending []held
	for path, declaration := range b.declared {
		if declaration.correctedBy != "" {
			encoded, _ := json.Marshal(declaration.corrections)
			pending = append(pending, held{path: path, reader: declaration.correctedBy, corrections: encoded,
				applied: true})
		}
		for reader := range declaration.conflicts {
			pending = append(pending, held{path: path, reader: reader, corrections: []byte(`{}`), applied: false})
		}
	}
	b.mutex.Unlock()
	for _, entry := range pending {
		_, _ = b.mirror.Exec(ctx, `INSERT INTO opnsense.mirror_correction (endpoint_id, reader, corrections, applied,
			recorded_at) SELECT id, $1, CAST($2 AS jsonb), CAST($3 AS boolean), $4 FROM opnsense.mirror_endpoint WHERE path = $5
			ON CONFLICT (endpoint_id, reader) DO UPDATE SET corrections = excluded.corrections, applied = excluded.applied`,
			entry.reader, string(entry.corrections), fmt.Sprint(entry.applied), b.now(), entry.path)
	}
}
