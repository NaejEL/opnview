package widget

import (
	"encoding/json"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/dataset"
)

// host.datasets: another plugin's dataset, read by its data-catalogue name, without SQL (the
// "after the gap build" decision of specs/SPEC-backend-completion-plugins.md). The read goes
// through the catalogue's own filter grammar and compiler (internal/dataset, GET
// /api/datasets/<name>/query): it names nothing a catalogue query could not name, and binds every
// value.
//
//	host.datasets.latest(name, filter)  the dataset's latest rows: {dataset, state, at, condition,
//	                                    rows, truncated, withheld}
//
// "LATEST" is the rows of the most recent instant: among the rows filter keeps, within the
// dataset's retention horizon, those whose time column holds the greatest instant, every one of
// them and nothing older. A dataset with no time column has no instant: every row filter keeps, at
// null. At most dataset.MaxRows rows are answered, truncated true when more matched.
//
// filter is a list of {column, op, value}, the catalogue's "filters" (docs/widget-api.md, "Data
// catalogue"); null or absent keeps every row. A filter the catalogue refuses -- a column the
// dataset does not declare, an operator its type does not admit -- is the script's mistake: a
// TypeError it may catch, naming the refusal's code and path.
//
// A DATASET THAT IS NOT THERE IS A NAMED ABSENCE, NEVER AN ERROR. A name the catalogue does not
// hold -- the plugin that declares it is not installed, or was refused -- answers state
// dataset_absent with no row, so a widget reading a dataset some installations lack keeps working
// on the others. The states:
//
//	current                     rows were found; at is their instant
//	no_rows                     the dataset is there and nothing matched within its horizon
//	dataset_absent              the catalogue holds no dataset of that name
//	withheld_by_aggregate_mode  aggregate_mode is no_domains and the filter reads a column that
//	                            carries a domain name: nothing is read
//
// Under no_domains a column carrying a domain name is left out of every row and named in withheld.
// condition is the dataset's provider kind's condition, as the catalogue lists it (freshness), null
// for an absent dataset. Values are as the catalogue reads them: an instant as epoch seconds, a
// boolean, an address and a MAC address as text.

// The states of a latest read.
const (
	LatestCurrent       = "current"
	LatestNoRows        = "no_rows"
	LatestDatasetAbsent = "dataset_absent"
)

// LatestRows is what host.datasets.latest answers.
type LatestRows struct {
	Dataset   string           `json:"dataset"`
	State     string           `json:"state"`
	At        *int64           `json:"at"`
	Condition *string          `json:"condition"`
	Rows      []map[string]any `json:"rows"`
	Truncated bool             `json:"truncated"`
	Withheld  []string         `json:"withheld"`
}

func init() {
	registerHostArea("datasets", func(call *scriptCall, area *goja.Object) {
		call.function(area, "latest", func(arguments goja.FunctionCall) goja.Value {
			name := call.textOf(arguments.Argument(0), "the dataset name")
			var filter json.RawMessage
			if !absent(arguments.Argument(1)) {
				encoded, err := call.encode(arguments.Argument(1))
				if err != nil {
					call.throw("the filter cannot be read: %v", err)
				}
				filter = encoded
			}
			latest, refusal, err := call.request.LatestRows(name, filter)
			if refusal != nil {
				call.throw("host.datasets.latest(%q): the filter is refused: %s at %s", name, refusal.Code, refusal.Path)
			}
			if err != nil {
				return call.fail(err)
			}
			return call.value(latest)
		})
	})
}

// LatestRows reads a dataset's latest rows; a refusal is a filter the catalogue does not admit.
func (r *Request) LatestRows(name string, filter json.RawMessage) (LatestRows, *dataset.Refusal, error) {
	result := LatestRows{Dataset: name, State: LatestDatasetAbsent, Rows: []map[string]any{}, Withheld: []string{}}
	declaration, found := r.datasets.Lookup(name)
	if !found {
		return result, nil, nil
	}
	query, refusal := dataset.ParseFilters(filter, declaration)
	if refusal != nil {
		return result, refusal, nil
	}
	providers, err := ReadProviders(r.Ctx, r.Db)
	if err != nil {
		return result, nil, err
	}
	condition := KindCondition(providers, declaration.Freshness.Kind)
	result.Condition = &condition
	if r.NoDomains() && query.FiltersTouchDomain(declaration) {
		result.State = ConditionWithheldByAggregateMode
		return result, nil, nil
	}
	columns := make([]string, 0, len(declaration.Columns))
	for _, column := range declaration.Columns {
		if r.NoDomains() && column.CarriesDomain {
			result.Withheld = append(result.Withheld, column.Name)
			continue
		}
		columns = append(columns, column.Name)
	}
	window := dataset.Window{From: 0, To: r.Now + 1}
	retention := r.retention
	if declaration.Retention != nil && declaration.Retention.Seconds > 0 {
		retention = declaration.Retention.Seconds
	}
	if retention > 0 && r.Now > retention {
		window.From = r.Now - retention
	}
	statement, layout := dataset.CompileLatest(declaration, query, window, columns, dataset.MaxRows+1)
	err = r.Db.CatalogueRows(r.Ctx, statement.Text, statement.Args, dataset.StatementTimeout,
		func(cells []any) error {
			if len(result.Rows) == dataset.MaxRows {
				result.Truncated = true
				return nil
			}
			if at, ok := cellNumber(cells[0]); ok {
				instant := int64(at)
				result.At = &instant
			}
			row := make(map[string]any, len(layout.Columns))
			for index, column := range layout.Columns {
				row[column] = cells[index+1]
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
