package readers

import (
	"encoding/json"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/datasets"
	"github.com/NaejEL/opnview/internal/widget/script"
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
// unknown_dataset with no row -- the data catalogue API's own refusal code for such a name -- so a
// widget reading a dataset some installations lack keeps working on the others. The states:
//
//	current                     rows were found; at is their instant
//	no_rows                     the dataset is there and nothing matched within its horizon
//	unknown_dataset             the catalogue holds no dataset of that name
//	withheld_by_aggregate_mode  aggregate_mode is no_domains and the filter reads a column that
//	                            carries a domain name: nothing is read
//
// Under no_domains a column carrying a domain name is left out of every row and named in withheld.
// condition is the dataset's provider kind's condition, as the catalogue lists it (freshness), null
// for an absent dataset. Values are as the catalogue reads them: an instant as epoch seconds, a
// boolean, an address and a MAC address as text.

func init() {
	script.RegisterHostArea("datasets", func(call *script.ScriptCall, area *goja.Object) {
		call.Function(area, "latest", func(arguments goja.FunctionCall) goja.Value {
			name := call.TextOf(arguments.Argument(0), "the dataset name")
			var filter json.RawMessage
			if !script.Absent(arguments.Argument(1)) {
				encoded, err := call.Encode(arguments.Argument(1))
				if err != nil {
					call.Throw("the filter cannot be read: %v", err)
				}
				filter = encoded
			}
			latest, refusal, err := datasets.ReadLatestRows(call.Request, name, filter)
			if refusal != nil {
				call.Throw("host.datasets.latest(%q): the filter is refused: %s at %s", name, refusal.Code, refusal.Path)
			}
			if err != nil {
				return call.Fail(err)
			}
			return call.Value(latest)
		})
	})
}
