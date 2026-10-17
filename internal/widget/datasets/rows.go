package datasets

import (
	"bytes"
	"encoding/json"

	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// A script's rows of a dataset (host.datasets.rows, PA3 of the plugin-API amendment): the rows its filters
// and key set keep within a window, set-based, in one statement whatever the number of keys
// (dataset.CompileRows). It is the read a widget, a source script and a derivation share: a widget's
// read withholds what carries a domain name under aggregate_mode = no_domains, as every widget read does;
// a source script's and a derivation's do not, because what they write is stored, and the aggregate mode
// changes what is shown, never what is stored.

// RowsRead is what host.datasets.rows answers. state is one of the latest read's: current, no_rows,
// unknown_dataset or withheld_by_aggregate_mode.
type RowsRead struct {
	Dataset   string           `json:"dataset"`
	State     string           `json:"state"`
	Condition *string          `json:"condition"`
	Rows      []map[string]any `json:"rows"`
	Truncated bool             `json:"truncated"`
	Withheld  []string         `json:"withheld"`
}

// rowsOptions is the options object of a read.
type rowsOptions struct {
	Filters json.RawMessage `json:"filters"`
	Keys    json.RawMessage `json:"keys"`
	From    *int64          `json:"from"`
	To      *int64          `json:"to"`
	Columns []string        `json:"columns"`
}

// ReadRows reads a dataset's rows for a script. withhold applies the aggregate mode, for a widget's read.
// A refusal is options the catalogue does not admit: a column it does not declare, an operator its type
// does not admit, a value of the wrong type, a list beyond its bound.
func ReadRows(r *request.Request, name string, raw json.RawMessage, withhold bool) (RowsRead, *dataset.Refusal, error) {
	result := RowsRead{Dataset: name, State: LatestUnknownDataset, Rows: []map[string]any{}, Withheld: []string{}}
	declaration, found := r.Datasets.Lookup(name)
	if !found {
		return result, nil, nil
	}
	var options rowsOptions
	if len(bytes.TrimSpace(raw)) > 0 && string(bytes.TrimSpace(raw)) != "null" {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&options); err != nil {
			return result, &dataset.Refusal{Code: dataset.RefusalMalformedParameters, Path: "options", Reason: "wrong_shape"}, nil
		}
	}
	query, refusal := dataset.ParseScriptFilters(options.Filters, declaration)
	if refusal != nil {
		return result, refusal, nil
	}
	keys, refusal := dataset.ParseKeys(options.Keys, declaration)
	if refusal != nil {
		return result, refusal, nil
	}
	providers, err := request.ReadProviders(r.Ctx, r.Db)
	if err != nil {
		return result, nil, err
	}
	condition := request.KindCondition(providers, declaration.Freshness.Kind)
	result.Condition = &condition
	noDomains := withhold && r.NoDomains()
	if noDomains && (query.FiltersTouchDomain(declaration) || keysTouchDomain(keys, declaration)) {
		result.State = envelope.ConditionWithheldByAggregateMode
		return result, nil, nil
	}
	wanted := map[string]bool{}
	for index, name := range options.Columns {
		if _, found := declaration.Column(name); !found {
			return result, &dataset.Refusal{Code: dataset.RefusalUnknownColumn, Path: "columns[" + itoa(index) + "]"}, nil
		}
		wanted[name] = true
	}
	columns := make([]string, 0, len(declaration.Columns))
	for _, column := range declaration.Columns {
		if len(wanted) > 0 && !wanted[column.Name] {
			continue
		}
		if noDomains && column.CarriesDomain {
			result.Withheld = append(result.Withheld, column.Name)
			continue
		}
		columns = append(columns, column.Name)
	}
	if len(columns) == 0 {
		result.State = LatestNoRows
		return result, nil, nil
	}
	window := dataset.Window{From: 0, To: r.Now + 1}
	retention := r.Retention
	if declaration.Retention != nil && declaration.Retention.Seconds > 0 {
		retention = declaration.Retention.Seconds
	}
	if retention > 0 && r.Now > retention {
		window.From = r.Now - retention
	}
	if options.From != nil && *options.From > window.From {
		window.From = *options.From
	}
	if options.To != nil && *options.To < window.To {
		window.To = *options.To
	}
	statement := dataset.CompileRows(declaration, query, keys, window, columns, dataset.MaxScriptRows+1)
	err = r.Db.CatalogueRows(r.Ctx, statement.Text, statement.Args, dataset.StatementTimeout,
		func(cells []any) error {
			if len(result.Rows) == dataset.MaxScriptRows {
				result.Truncated = true
				return nil
			}
			row := make(map[string]any, len(columns))
			for index, column := range columns {
				row[column] = cells[index]
			}
			result.Rows = append(result.Rows, row)
			return nil
		})
	if err != nil {
		return result, nil, err
	}
	result.State = LatestNoRows
	if len(result.Rows) > 0 {
		result.State = LatestCurrent
	}
	return result, nil, nil
}

// keysTouchDomain reports whether a key set reads a column carrying a domain name.
func keysTouchDomain(keys *dataset.KeySet, declaration dataset.Declaration) bool {
	if keys == nil {
		return false
	}
	for _, name := range keys.Columns {
		if column, found := declaration.Column(name); found && column.CarriesDomain {
			return true
		}
	}
	return false
}

// itoa is a small index as text.
func itoa(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
