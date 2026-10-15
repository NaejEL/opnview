package values

import (
	"strings"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/script"
)

// host.envelope: what the response carries around the data (docs/widget-api.md, "The envelope").
// The core read the availability, the gaps and the requested window before the script runs, and
// computes the condition after it returns; a script records here what only it knows.
//
//	host.envelope.state(code)            records a named state beside the data
//	host.envelope.withhold(path)         records a field or level the aggregate mode withheld
//	host.envelope.override(condition)    replaces the computed condition by one of the widget's own
//	                                     (no_owner_exists answers with no data)
//	host.envelope.cover(interval)        records the covered part of the window, null for none
//	host.envelope.coverFrom(earliest)    records coverage from the earliest record a source holds to
//	                                     now; null when it holds none
//	host.envelope.coverFlows(state)      records the coverage of a reading of the flows alone, and the
//	                                     state named when part of the window lies past the retention
//	                                     horizon ("" for none)
//	host.envelope.coverFlowsState(state) records that state alone
//	host.envelope.earliest(name)         the instant of one of the core's widget_earliest_* statements,
//	                                     or null when the source holds no record
//	host.envelope.availability()         every provider of the kinds the widget binds, with its
//	                                     condition, as the response carries them
//	host.envelope.availabilitySince()    when each registered provider's current state began, by
//	                                     "<kind>/<provider key>": {after_at, by_at, state}
//	host.envelope.kindCondition(kind)   the condition of a provider kind: its active providers' best
//	host.envelope.gaps()                 the gaps of the window, as the response carries them
//	host.envelope.readGaps(kinds, window) the gaps of other kinds or another window
//	host.envelope.hourFreshness(from, to) records the freshness of the 1 h slots of [from, to)
//	host.envelope.conditions             the condition codes, by name
//	host.envelope.coverage               the coverage values, by name

func init() {
	script.RegisterHostArea("envelope", func(call *script.ScriptCall, area *goja.Object) {
		r := call.Request
		text := func(arguments goja.FunctionCall, what string) string {
			return call.TextOf(arguments.Argument(0), what)
		}
		call.Function(area, "state", func(arguments goja.FunctionCall) goja.Value {
			r.State(text(arguments, "the state"))
			return goja.Undefined()
		})
		call.Function(area, "withhold", func(arguments goja.FunctionCall) goja.Value {
			r.Withhold(text(arguments, "the withheld path"))
			return goja.Undefined()
		})
		call.Function(area, "override", func(arguments goja.FunctionCall) goja.Value {
			r.Override = text(arguments, "the condition")
			return goja.Undefined()
		})
		call.Function(area, "cover", func(arguments goja.FunctionCall) goja.Value {
			r.Cover(call.OptionalIntervalOf(arguments.Argument(0), "the covered interval"))
			return goja.Undefined()
		})
		call.Function(area, "coverFrom", func(arguments goja.FunctionCall) goja.Value {
			earliest := call.OptionalIntegerOf(arguments.Argument(0), "the earliest record")
			if earliest == nil {
				r.CoverFrom(0, false)
			} else {
				r.CoverFrom(*earliest, true)
			}
			return goja.Undefined()
		})
		call.Function(area, "coverFlows", func(arguments goja.FunctionCall) goja.Value {
			if err := r.CoverFlows(call.TextOf(arguments.Argument(0), "the state")); err != nil {
				return call.Fail(err)
			}
			return goja.Undefined()
		})
		call.Function(area, "coverFlowsState", func(arguments goja.FunctionCall) goja.Value {
			if err := r.CoverFlowsState(call.TextOf(arguments.Argument(0), "the state")); err != nil {
				return call.Fail(err)
			}
			return goja.Undefined()
		})
		call.Function(area, "earliest", func(arguments goja.FunctionCall) goja.Value {
			name := text(arguments, "the statement name")
			if !strings.HasPrefix(name, "widget_earliest_") {
				call.Throw("envelope.earliest reads one of the core's widget_earliest_* statements, not %q", name)
			}
			earliest, has, err := r.Earliest(name)
			if err != nil {
				return call.Fail(err)
			}
			if !has {
				return goja.Null()
			}
			return call.VM.ToValue(earliest)
		})
		call.Function(area, "availability", func(goja.FunctionCall) goja.Value {
			return call.Value(r.Response.Availability)
		})
		call.Function(area, "availabilitySince", func(goja.FunctionCall) goja.Value {
			since, err := request.ReadAvailabilitySince(r.Ctx, r.Db)
			if err != nil {
				return call.Fail(err)
			}
			return call.Value(since)
		})
		call.Function(area, "kindCondition", func(arguments goja.FunctionCall) goja.Value {
			return call.VM.ToValue(request.KindCondition(r.Response.Availability, text(arguments, "the provider kind")))
		})
		call.Function(area, "gaps", func(goja.FunctionCall) goja.Value {
			return call.Value(r.Response.Gaps)
		})
		call.Function(area, "readGaps", func(arguments goja.FunctionCall) goja.Value {
			kinds := call.TextsOf(arguments.Argument(0), "the provider kinds")
			gaps, err := r.ReadGaps(kinds, call.IntervalOf(arguments.Argument(1), "the window"))
			if err != nil {
				return call.Fail(err)
			}
			return call.Value(gaps)
		})
		call.Function(area, "hourFreshness", func(arguments goja.FunctionCall) goja.Value {
			from := call.IntegerOf(arguments.Argument(0), "from")
			to := call.IntegerOf(arguments.Argument(1), "to")
			if err := r.HourFreshness(from, to); err != nil {
				return call.Fail(err)
			}
			return goja.Undefined()
		})
		_ = area.Set("conditions", call.Value(map[string]string{
			"reachable": envelope.ConditionReachable, "reachable_no_rows": envelope.ConditionReachableNoRows,
			"unavailable": envelope.ConditionUnavailable, "present_but_disabled": envelope.ConditionPresentButDisabled,
			"not_yet_probed": envelope.ConditionNotYetProbed, "state_unknown": envelope.ConditionStateUnknown,
			"no_owner_exists": envelope.ConditionNoOwnerExists, "unresolved_reference": envelope.ConditionUnresolvedReference,
			"withheld_by_aggregate_mode": envelope.ConditionWithheldByAggregateMode,
		}))
		_ = area.Set("coverage", call.Value(map[string]string{
			"covered": request.CoverageFull, "partially_covered": request.CoveragePartial, "not_covered": request.CoverageNone,
		}))
	})
}
