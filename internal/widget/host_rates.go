package widget

import (
	"strconv"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/store"
)

// host.rates: the current rates, from the latest samples (rates.go): a second measure, sampled,
// never summed with the logged one. A subject with no current sample reads its named absence,
// no_current_sample, and one older than twice its cadence stale_sample; never a rate of 0. Every
// rate is a chip rate: {state, measure, in_bits_per_second, out_bits_per_second, out_measured,
// sampled_at, span_seconds, cadence_seconds, lower_bound}. Reading any of them renews the
// live-interest lease: the answer carries a live figure.
//
//	host.rates.current(groupBy)  the rates a "now" figure shows: {by_direction, outbound_unsplit,
//	                             by_member}, by_direction one {key, rate} per traffic direction
//	                             (outbound, inbound, inter_interface), by_member one per client
//	                             ("client") or interface ("interface") holding a current sample,
//	                             sorted by key, and empty for any other grouping
//	host.rates.byClient()        every client holding a current sample: a map from its id, as text,
//	                             to its rate
//	host.rates.client(id)        one client's current rate, or its named absence
//	host.rates.none()            the named absence of a rate: what a subject with no current sample
//	                             reads

func init() {
	registerHostArea("rates", func(call *scriptCall, area *goja.Object) {
		r := call.request
		call.function(area, "current", func(arguments goja.FunctionCall) goja.Value {
			rates, err := r.CurrentRates(call.textOf(arguments.Argument(0), "the grouping"))
			if err != nil {
				return call.fail(err)
			}
			return call.value(rates)
		})
		call.function(area, "byClient", func(goja.FunctionCall) goja.Value {
			rates, err := r.readCurrentRates()
			if err != nil {
				return call.fail(err)
			}
			byClient := make(map[string]ChipRate, len(rates.ByClient))
			for id, rate := range rates.ByClient {
				byClient[strconv.FormatInt(id, 10)] = SampledRate(rate, true, r.Now)
			}
			return call.value(byClient)
		})
		call.function(area, "client", func(arguments goja.FunctionCall) goja.Value {
			id := call.integerOf(arguments.Argument(0), "the client id")
			r.renewLive()
			rate, state, err := r.Db.ReadClientCurrentRate(r.Ctx, id, r.Now)
			if err != nil {
				return call.fail(err)
			}
			return call.value(SampledRate(rate, state == store.RateCurrent, r.Now))
		})
		call.function(area, "none", func(goja.FunctionCall) goja.Value {
			return call.value(SampledRate(store.CurrentRate{}, false, r.Now))
		})
	})
}
