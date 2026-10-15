package readers

import (
	"sort"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/widget/figures"
	"github.com/NaejEL/opnview/internal/widget/script"
)

// host.attribution: how much of the window's outbound traffic could be named at all, as a whole and
// per client (internal/widget/figures/attribution.go) -- store.ReadAttributionRate's counts, with
// the rate RateOf makes of them. A rate is UNDEFINED -- null, never 0 -- when no resolver source
// answered for the window or there is no eligible flow to divide by; rate_state says which.
//
//	host.attribution.window()    {resolver_covered, cache_covered, attribution}: whether a resolver
//	                             source answered for the window, whether its cache was read over it,
//	                             and the window's attribution row as a whole (its client empty, no
//	                             eligible_bytes)
//	host.attribution.clients()   the ids of the clients with an eligible flow in the window, ascending
//	host.attribution.client(id)  one client's attribution row: {client, eligible_flows,
//	                             eligible_bytes, named_flows, methods, rate, rate_state,
//	                             mean_delay_seconds, max_delay_seconds}, its counts 0 when it has no
//	                             eligible flow; null for no such client
//
// Each count is read once per request, and the per-client counts only when a function needs them.

// scriptWindowAttribution is the window's attribution as a script reads it.
type scriptWindowAttribution struct {
	ResolverCovered bool                   `json:"resolver_covered"`
	CacheCovered    bool                   `json:"cache_covered"`
	Attribution     figures.AttributionRow `json:"attribution"`
}

func init() {
	script.RegisterHostArea("attribution", func(call *script.ScriptCall, area *goja.Object) {
		r := call.Request
		var (
			wholeRead, countsRead bool
			counts                map[int64]figures.ClientAttribution
			whole                 reads.AttributionRate
		)
		// loadWhole reads the window as a whole; loadCounts the per-client counts with it, which a
		// widget reading only the whole never pays for.
		loadWhole := func() bool {
			if wholeRead {
				return true
			}
			var err error
			whole, err = r.Db.ReadAttributionRate(r.Ctx, r.Window.From, r.Window.To, nil)
			if err != nil {
				call.Fail(err)
				return false
			}
			wholeRead = true
			return true
		}
		loadCounts := func() bool {
			if countsRead {
				return true
			}
			var err error
			counts, whole, err = figures.AttributionByClient(r)
			if err != nil {
				call.Fail(err)
				return false
			}
			wholeRead, countsRead = true, true
			return true
		}
		call.Function(area, "window", func(goja.FunctionCall) goja.Value {
			if !loadWhole() {
				return goja.Undefined()
			}
			rate, state := figures.RateOf(figures.ClientAttribution{Eligible: whole.EligibleFlows, Named: whole.NamedFlows},
				whole.ResolverCovered)
			return call.Value(scriptWindowAttribution{ResolverCovered: whole.ResolverCovered,
				CacheCovered: whole.CacheCovered,
				Attribution: figures.AttributionRow{EligibleFlows: r.CountOf(whole.EligibleFlows),
					NamedFlows: r.CountOf(whole.NamedFlows),
					Methods:    figures.MethodCounts{ResolverCacheAnswer: whole.NamedByCacheAnswer, LookupTiming: whole.NamedByLookupTiming},
					Rate:       rate, RateState: state, MeanDelaySeconds: whole.MeanDelaySeconds,
					MaxDelaySeconds: whole.MaxDelaySeconds}})
		})
		call.Function(area, "clients", func(goja.FunctionCall) goja.Value {
			if !loadCounts() {
				return goja.Undefined()
			}
			ids := make([]int64, 0, len(counts))
			for id := range counts {
				ids = append(ids, id)
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			return call.Value(ids)
		})
		call.Function(area, "client", func(arguments goja.FunctionCall) goja.Value {
			id := call.IntegerOf(arguments.Argument(0), "the client id")
			if !loadCounts() {
				return goja.Undefined()
			}
			info, err := r.Client(id)
			if err != nil {
				return call.Fail(err)
			}
			if info == nil {
				return goja.Null()
			}
			count := counts[id]
			rate, state := figures.RateOf(count, whole.ResolverCovered)
			eligibleBytes := r.ByteCountOf(figures.MeasuredFigures(count.Bytes, count.Measured), false)
			return call.Value(figures.AttributionRow{Client: *info, EligibleFlows: r.CountOf(count.Eligible),
				EligibleBytes: &eligibleBytes, NamedFlows: r.CountOf(count.Named),
				Methods: figures.MethodCounts{ResolverCacheAnswer: count.Cache, LookupTiming: count.Timing},
				Rate:    rate, RateState: state, MeanDelaySeconds: count.Mean, MaxDelaySeconds: count.Maximum})
		})
	})
}
