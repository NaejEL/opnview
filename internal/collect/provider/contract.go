package provider

// The record contract of every kind, defined once, as data (kinds.go).
//
// A KIND IS A CONTRACT, AND THE CONTRACT IS WRITTEN DOWN AS DATA: kinds/<kind>.json publishes what
// an implementation hands the kind -- the functions its script exports, what each takes and
// returns, and the fields of every record, with their types, units, bounds and whether they are
// required -- and the kind's attributes. The plugin host checks what a script returns against
// exactly these tables (scripthost.CheckRecord), once, so a field the contract does not hold, a
// required one that is missing and a value outside its bounds are refused with the closed code
// records_invalid, and nothing of that answer is stored. docs/plugins.md carries the same tables,
// and a test asserts they are identical, so the document a third party writes a plugin from cannot
// drift from the data that reads the plugin.
//
// WHAT IS NOT HERE: derivation. A firewall_log record becomes a flow, and pairing,
// classification, attribution and aggregation run on it, in internal/collect and
// internal/store, exactly as for a built-in. A contract says what a source reports; what opnview
// makes of it is the core's.
//
// A KIND A PLUGIN DECLARES has no file here: its contract is its own tables (docs/plugins.md,
// "A kind of your own") and its attributes its manifest's `kinds` entry; a provider set (Set)
// knows it beside the core kinds.

// FieldType is the JSON type a record field takes, and how the host reads it.
type FieldType string

// The field types.
const (
	// FieldText is a JSON string.
	FieldText FieldType = "text"
	// FieldInteger is a JSON number with no fractional part.
	FieldInteger FieldType = "integer"
	// FieldNumber is any JSON number.
	FieldNumber FieldType = "number"
	// FieldBoolean is true or false.
	FieldBoolean FieldType = "boolean"
	// FieldTextList is an array of strings.
	FieldTextList FieldType = "text_list"
	// FieldObject is a JSON object, stored as its JSON text.
	FieldObject FieldType = "object"
	// FieldRecord is one record of another table of the same contract, named by Of.
	FieldRecord FieldType = "record"
	// FieldRecords is an array of records of another table of the same contract, named by Of.
	FieldRecords FieldType = "records"
)

// Field is one field of a record.
type Field struct {
	// Name is the field's name as a script writes it: snake_case.
	Name string `json:"name"`
	// Type is its JSON type.
	Type FieldType `json:"type"`
	// Required says the field must be present and not null. An optional field may be absent
	// or null, and the host then stores it as absent -- never as a zero.
	Required bool `json:"required,omitempty"`
	// Of names the table of a record or records field.
	Of string `json:"of,omitempty"`
	// Values, when not empty, is the closed vocabulary of a text field.
	Values []string `json:"values,omitempty"`
	// IntegerValues, when not empty, is the closed set of an integer field (an IP version: 4 or 6).
	IntegerValues []int64 `json:"integer_values,omitempty"`
	// Min and Max bound an integer or number field, when set: a port is 0 to 65535, a length 0 or more.
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
	// Term says a text field is a term the schema constrains: non-empty, lower-case, no space
	// (TermShapeValid) -- a measure, a unit, a subject kind, a set key.
	Term bool `json:"term,omitempty"`
	// Default is the value of an exported value a provider with no script -- a declarative one -- takes.
	Default any `json:"default,omitempty"`
	// Unit is the unit of a numeric field, or its encoding ("UTC epoch seconds").
	Unit string `json:"unit,omitempty"`
	// Description says what the field is, in a sentence.
	Description string `json:"description"`
}

// Record is one table of a contract: a record a function returns, or an element of one.
type Record struct {
	// Name is the table's name in docs/plugins.md.
	Name string `json:"name"`
	// Description says what one record is.
	Description string `json:"description"`
	// Fields are its fields, in the order the document lists them.
	Fields []Field `json:"fields"`
}

// Function is one function a kind's script exports.
type Function struct {
	// Name is the exported function's name.
	Name string `json:"name"`
	// Arguments describes what it is called with, after the host object.
	Arguments string `json:"arguments"`
	// Returns names the record the function returns.
	Returns string `json:"returns"`
	// Description says when the host calls it.
	Description string `json:"description"`
}

// Contract is one kind's declaration: its attributes and its whole contract.
type Contract struct {
	// Kind is the kind's registry name.
	Kind string `json:"kind"`
	// Description says what the kind is.
	Description string `json:"description"`
	// Concurrent says several providers of the kind may be active at once. A kind admits several
	// concurrently active providers exactly when the identity of its destination rows includes the
	// provider: where it does, two active providers can neither collide nor double-count, because every
	// row says who reported it; where it does not, the second provider's rows are indistinguishable from
	// the first's by origin. The database enforces it from the provider_kind row this attribute writes
	// (internal/store/schema.sql, uq_provider_active_per_exclusive_kind).
	Concurrent bool `json:"concurrent"`
	// Push says a push entry may deliver records of the kind (docs/plugins.md, "Push sources").
	Push bool `json:"push"`
	// Bindable says a widget manifest may bind the kind in `providers` and `primary`.
	Bindable bool `json:"bindable"`
	// CoreOnly, when not empty, says no plugin may implement the kind, and why: it is opnview's own
	// machinery rather than a source.
	CoreOnly string `json:"core_only,omitempty"`
	// Service says the kind is a service kind: an external service's data a plugin acquires through
	// the fetch facility and answers from, rather than a firewall source.
	Service bool `json:"service,omitempty"`
	// OwnCadence says each provider of the kind states its own cadence in its manifest, since no
	// surveyed cadence exists for its sources.
	OwnCadence bool `json:"own_cadence,omitempty"`
	// PushRecord names the record table a push entry delivering the kind fills, exactly when Push is set.
	PushRecord string `json:"push_record,omitempty"`
	// LiveAnswer names the answer table a live pass of the kind returns, when the kind accepts a live pass
	// (docs/plugins.md, "Live pass"): only a kind whose records a fine sample can be.
	LiveAnswer string `json:"live_answer,omitempty"`
	// Registered are registry rows the kind's declaration writes itself, for an implementation no
	// plugin declares: a core-only kind's historical provider.
	Registered []RegisteredProvider `json:"registered,omitempty"`
	// Functions are what an implementation's script exports under the kind's name.
	Functions []Function `json:"functions,omitempty"`
	// Values are the plain values the script may export beside the functions, as fields.
	Values []Field `json:"values,omitempty"`
	// Records are the record tables, the answer of each function first.
	Records []Record `json:"records,omitempty"`
}

// RegisteredProvider is a registry row a kind's declaration writes.
type RegisteredProvider struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
}

// Record returns one of the contract's record tables by name.
func (c Contract) Record(name string) (Record, bool) {
	for _, record := range c.Records {
		if record.Name == name {
			return record, true
		}
	}
	return Record{}, false
}

// Field returns one of the record's fields by name.
func (r Record) Field(name string) (Field, bool) {
	for _, field := range r.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return Field{}, false
}

// PolledRecordTables are the record tables a contract's functions can return, directly or nested:
// what an implementation delivers by polling. A push entry delivering a record table outside this set
// delivers what no polled path delivers, so it replaces nothing (PS9 of the push amendment, as the
// NetFlow join amendment's PS17 amends it).
func PolledRecordTables(contract Contract) map[string]bool {
	reached := map[string]bool{}
	var visit func(name string)
	visit = func(name string) {
		if reached[name] {
			return
		}
		reached[name] = true
		record, found := LookupRecord(contract, name)
		if !found {
			return
		}
		for _, field := range record.Fields {
			if field.Of != "" {
				visit(field.Of)
			}
		}
	}
	for _, function := range contract.Functions {
		visit(function.Returns)
	}
	return reached
}
