package dataset

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The roles a column plays.
const (
	// RoleTime is the column holding each row's instant, in UTC epoch seconds: the one a period
	// window and a time axis read.
	RoleTime = "time"
	// RoleDimension is a column a query groups, splits or filters by.
	RoleDimension = "dimension"
	// RoleMeasure is a numeric column a query aggregates.
	RoleMeasure = "measure"
)

// The natures of a dataset: what a bucket with no row means.
const (
	// NatureEvent is a dataset of things that happened: a bucket the collection covered and that
	// holds no row is a true zero.
	NatureEvent = "event"
	// NatureSample is a dataset of readings taken: a bucket with no row is a gap, never a zero.
	NatureSample = "sample"
)

// The column types, as plugin tables declare them: the plugin type vocabulary (PS4 of the push
// amendment). A query reads an instant as an integer of UTC epoch seconds, and a boolean, an address
// and a MAC address as text, because every instant the JSON API serves is epoch seconds (P3) and the
// rest are compared for equality only.
const (
	TypeInteger = "integer"
	TypeReal    = "real"
	TypeText    = "text"
	TypeBoolean = "boolean"
	TypeIP      = "ip"
	TypeMAC     = "mac"
	TypeInstant = "instant"
)

// readType is the type a query reads a column as: an instant as an integer, a boolean, an address
// and a MAC address as text, every other type as itself; "" for a word outside the vocabulary.
func readType(columnType string) string {
	switch columnType {
	case TypeInteger, TypeReal, TypeText:
		return columnType
	case TypeInstant:
		return TypeInteger
	case TypeBoolean, TypeIP, TypeMAC:
		return TypeText
	}
	return ""
}

// The aggregations, a closed list.
const (
	AggregationSum           = "sum"
	AggregationMean          = "mean"
	AggregationMin           = "min"
	AggregationMax           = "max"
	AggregationLast          = "last"
	AggregationCount         = "count"
	AggregationCountDistinct = "count_distinct"
)

// measureAggregations are the aggregations a measure may admit, in the order a default lists them.
var measureAggregations = []string{AggregationSum, AggregationMean, AggregationMin, AggregationMax,
	AggregationLast, AggregationCount}

// dimensionAggregations are the aggregations a dimension may admit.
var dimensionAggregations = []string{AggregationCount, AggregationCountDistinct}

// Aggregations is every aggregation a query may name.
func Aggregations() []string {
	return []string{AggregationSum, AggregationMean, AggregationMin, AggregationMax, AggregationLast,
		AggregationCount, AggregationCountDistinct}
}

// Column is one column of a dataset.
type Column struct {
	Name string `json:"name"`
	// Type is integer, real or text.
	Type string `json:"type"`
	// Role is time, dimension or measure; empty is dimension, or time for the retention column.
	Role string `json:"role,omitempty"`
	// Unit is a measure's unit: a lower-case word with no space, such as byte or second.
	Unit string `json:"unit,omitempty"`
	// Nullable admits NULL.
	Nullable bool `json:"nullable,omitempty"`
	// CarriesDomain says the column holds a domain name: under aggregate_mode = no_domains a query
	// reading, filtering or splitting on it is withheld.
	CarriesDomain bool `json:"carries_domain,omitempty"`
	// Aggregations are the aggregations the column admits; empty takes its role's defaults
	// (DefaultAggregations).
	Aggregations []string `json:"aggregations,omitempty"`
}

// Retention is where a dataset's horizon comes from: its time column, and how long a row is kept
// after it. Zero seconds follows the installation's retention_seconds setting.
type Retention struct {
	Column  string `json:"column"`
	Seconds int64  `json:"seconds,omitempty"`
}

// Freshness names the provider whose last successful pass is a dataset's freshness: a kind, and
// one implementation of it when only one writes the dataset.
type Freshness struct {
	Kind        string `json:"kind"`
	ProviderKey string `json:"provider_key,omitempty"`
}

// Declaration is one dataset.
type Declaration struct {
	// Name is the dataset's name in the API: a core dataset's own name, or <plugin>.<table>.
	Name string `json:"name"`
	// Origin is "core", or the name of the plugin that declares it.
	Origin string `json:"origin"`
	// Label is the catalogue key of its name.
	Label string `json:"label"`
	// Nature is event or sample.
	Nature string `json:"nature"`
	// Freshness is the provider whose last successful pass dates it.
	Freshness Freshness `json:"freshness"`
	// Retention is its horizon; nil is a dataset with no time column, never purged.
	Retention *Retention `json:"retention,omitempty"`
	// Columns are its columns, in order.
	Columns []Column `json:"columns"`
	// LossDetectable is set for a dataset fed by a push source (PS7 of the push amendment): whether a
	// loss on the network between the firewall and opnview would be detected and recorded as a gap
	// -- true over TCP syslog and NetFlow, false over UDP syslog, which carries no sequence number.
	// Nil for every other dataset.
	LossDetectable *bool `json:"loss_detectable,omitempty"`

	// relation is the SELECT the dataset reads, owned by the server: a core dataset's
	// relation.sql, or a plugin table's qualified name. It may use the :from and :to parameters,
	// which the compiler binds to the window. It is never listed.
	relation string
	// earliest is the statement that reads the instant of the dataset's oldest row, for its
	// coverage; empty reads min(time) over the relation.
	earliest string
}

// OriginCore is the origin of the core's own datasets.
const OriginCore = "core"

// namePattern is what a dataset's own name and a column name match.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// unitPattern is what a unit matches.
var unitPattern = regexp.MustCompile(`^[a-z][a-z_]*$`)

// TimeColumn returns the dataset's time column, if any.
func (d Declaration) TimeColumn() (Column, bool) {
	for _, column := range d.Columns {
		if column.Role == RoleTime {
			return column, true
		}
	}
	return Column{}, false
}

// Column returns a column by name.
func (d Declaration) Column(name string) (Column, bool) {
	for _, column := range d.Columns {
		if column.Name == name {
			return column, true
		}
	}
	return Column{}, false
}

// Admits reports whether a column admits an aggregation.
func (c Column) Admits(aggregation string) bool { return slices.Contains(c.Aggregations, aggregation) }

// DefaultAggregations are the aggregations a column admits when it declares none: every
// aggregation of a measure, last only where the dataset has a time column to order by, and the
// counts of a dimension.
func DefaultAggregations(role string, timed bool) []string {
	switch role {
	case RoleMeasure:
		var result []string
		for _, aggregation := range measureAggregations {
			if aggregation != AggregationLast || timed {
				result = append(result, aggregation)
			}
		}
		return result
	case RoleDimension:
		return append([]string(nil), dimensionAggregations...)
	}
	return []string{}
}

// Normalise fills a declaration's defaults -- each column's role and aggregations -- and checks it.
// The relation is set by whoever built the declaration.
func Normalise(declaration Declaration) (Declaration, error) {
	if declaration.Nature != NatureEvent && declaration.Nature != NatureSample {
		return declaration, fmt.Errorf("dataset %s: nature %q is not event or sample", declaration.Name,
			declaration.Nature)
	}
	if declaration.Freshness.Kind == "" {
		return declaration, fmt.Errorf("dataset %s: no freshness kind", declaration.Name)
	}
	if len(declaration.Columns) == 0 {
		return declaration, fmt.Errorf("dataset %s: no column", declaration.Name)
	}
	timeColumn := ""
	if declaration.Retention != nil {
		timeColumn = declaration.Retention.Column
	}
	columns := make([]Column, 0, len(declaration.Columns))
	seen := map[string]bool{}
	times := 0
	for _, column := range declaration.Columns {
		if !namePattern.MatchString(column.Name) || seen[column.Name] {
			return declaration, fmt.Errorf("dataset %s: the column name %q is ill-formed or declared twice",
				declaration.Name, column.Name)
		}
		seen[column.Name] = true
		if readType(column.Type) == "" {
			return declaration, fmt.Errorf("dataset %s: the column %s has the type %q", declaration.Name,
				column.Name, column.Type)
		}
		if column.Role == "" {
			column.Role = RoleDimension
			if column.Name == timeColumn {
				column.Role = RoleTime
			}
		}
		switch column.Role {
		case RoleTime:
			times++
			if readType(column.Type) != TypeInteger || column.Name != timeColumn {
				return declaration, fmt.Errorf("dataset %s: the time column %s is not the integer or instant column "+
					"the retention reads", declaration.Name, column.Name)
			}
		case RoleMeasure:
			if column.Type != TypeInteger && column.Type != TypeReal {
				return declaration, fmt.Errorf("dataset %s: the measure %s is not a number", declaration.Name, column.Name)
			}
		case RoleDimension:
		default:
			return declaration, fmt.Errorf("dataset %s: the column %s has the role %q", declaration.Name,
				column.Name, column.Role)
		}
		if column.Unit != "" && (column.Role != RoleMeasure || !unitPattern.MatchString(column.Unit)) {
			return declaration, fmt.Errorf("dataset %s: the unit %q of %s is not a measure's lower-case word",
				declaration.Name, column.Unit, column.Name)
		}
		columns = append(columns, column)
	}
	if timeColumn != "" && times != 1 {
		return declaration, fmt.Errorf("dataset %s: the retention column %s is not a declared column",
			declaration.Name, timeColumn)
	}
	timed := timeColumn != ""
	for index, column := range columns {
		admissible := DefaultAggregations(column.Role, timed)
		if len(column.Aggregations) == 0 {
			columns[index].Aggregations = admissible
			continue
		}
		for _, aggregation := range column.Aggregations {
			if !slices.Contains(admissible, aggregation) {
				return declaration, fmt.Errorf("dataset %s: the column %s cannot admit %q", declaration.Name,
					column.Name, aggregation)
			}
		}
	}
	declaration.Columns = columns
	return declaration, nil
}

// PluginDataset builds the declaration of a plugin table: its name is <plugin>.<table>, its
// label the plugin's own key plugin.<plugin>.dataset.<table>, and its relation the table itself.
// sqlName is the table's qualified, quoted name; columns are the table's declared columns.
func PluginDataset(plugin, table, sqlName, nature string, freshness Freshness, retention *Retention,
	columns []Column) (Declaration, error) {
	if nature == "" {
		nature = NatureSample
	}
	names := make([]string, 0, len(columns))
	instantTime := false
	for _, column := range columns {
		names = append(names, projection(column))
		if retention != nil && column.Name == retention.Column && column.Type == TypeInstant {
			instantTime = true
		}
	}
	declaration := Declaration{Name: plugin + "." + table, Origin: plugin,
		Label: "plugin." + plugin + ".dataset." + table, Nature: nature, Freshness: freshness,
		Retention: retention, Columns: columns}
	declaration.relation = "SELECT " + strings.Join(names, ", ") + " FROM " + sqlName
	if retention != nil {
		declaration.earliest = "SELECT min(" + quote(retention.Column) + ") FROM " + sqlName
		if instantTime {
			// The window is applied to the timestamp itself, so a hypertable's chunks outside it are
			// never read; the earliest row is read as epoch seconds like every instant served.
			declaration.relation += " WHERE " + quote(retention.Column) + " >= to_timestamp(:from) AND " +
				quote(retention.Column) + " < to_timestamp(:to)"
			declaration.earliest = "SELECT CAST(floor(extract(epoch FROM min(" + quote(retention.Column) +
				"))) AS BIGINT) FROM " + sqlName
		}
	}
	return Normalise(declaration)
}

// projection is how the relation reads one column: under its own name, an instant as epoch
// seconds, a boolean, an address and a MAC address as text.
func projection(column Column) string {
	name := quote(column.Name)
	switch column.Type {
	case TypeInstant:
		return "CAST(floor(extract(epoch FROM " + name + ")) AS BIGINT) AS " + name
	case TypeIP:
		return "host(" + name + ") AS " + name
	case TypeMAC:
		return "CAST(" + name + " AS TEXT) AS " + name
	case TypeBoolean:
		return "CASE WHEN " + name + " THEN 'true' WHEN NOT " + name + " THEN 'false' END AS " + name
	}
	return name
}

// quote quotes an identifier.
func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// OriginOpnsense is the origin of the datasets the broker learned of the firewall API.
const OriginOpnsense = "opnsense"

// Learned builds a dataset whose relation the server composes at run time: a mirror table or a
// stream recording of the broker (A15). The relation and the earliest statement are the caller's
// own SQL, every identifier in them quoted from the mirror's metadata; nothing in them comes from
// a request.
func Learned(declaration Declaration, relation, earliest string) (Declaration, error) {
	declaration.relation, declaration.earliest = relation, earliest
	return Normalise(declaration)
}
