package readers

import (
	"strconv"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/store/samples"
	"github.com/NaejEL/opnview/internal/widget/figures"
	"github.com/NaejEL/opnview/internal/widget/script"
)

// host.rates: the current rates, from the latest samples (internal/widget/figures/rates.go): a
// second measure, sampled, never summed with the logged one. A subject with no current sample reads
// its named absence, no_current_sample, and one older than twice its cadence stale_sample; never a
// rate of 0. Every rate is a chip rate: {state, measure, in_bits_per_second, out_bits_per_second,
// out_measured, sampled_at, span_seconds, cadence_seconds, lower_bound}. Reading any of them renews
// the live-interest lease: the answer carries a live figure.
//
//	host.rates.current(groupBy)  the rates a "now" figure shows: {by_direction, outbound_unsplit,
//	                             by_member}, by_direction one {key, rate} per traffic direction
//	                             (outbound, inbound, inter_interface), by_member one per client
//	                             ("client") or interface ("interface") holding a current sample,
//	                             sorted by key, and empty for any other grouping
//	host.rates.byClient()        every client holding a current sample: a map from its id, as text,
//	                             to its rate
//	host.rates.client(id)        one client's current rate, or its named absence
//	host.rates.byPeer()          every peer address an inside local address was read with in a
//	                             current sample: a map from the address to its rate, the inbound
//	                             figure what the peer sent, the outbound one what was sent to it
//	                             (store.CurrentRates.ByPeer)
//	host.rates.clientPeers(id)   the same for one client: a map from each peer address it was
//	                             read with to its rate, empty when it has none
//	host.rates.none()            the named absence of a rate: what a subject with no current sample
//	                             reads

func init() {
	script.RegisterHostArea("rates", func(call *script.ScriptCall, area *goja.Object) {
		r := call.Request
		call.Function(area, "current", func(arguments goja.FunctionCall) goja.Value {
			rates, err := figures.CurrentRatesOf(r, call.TextOf(arguments.Argument(0), "the grouping"))
			if err != nil {
				return call.Fail(err)
			}
			return call.Value(rates)
		})
		call.Function(area, "byClient", func(goja.FunctionCall) goja.Value {
			rates, err := figures.ReadCurrentRates(r)
			if err != nil {
				return call.Fail(err)
			}
			byClient := make(map[string]figures.ChipRate, len(rates.ByClient))
			for id, rate := range rates.ByClient {
				byClient[strconv.FormatInt(id, 10)] = figures.SampledRate(rate, true, r.Now)
			}
			return call.Value(byClient)
		})
		call.Function(area, "client", func(arguments goja.FunctionCall) goja.Value {
			id := call.IntegerOf(arguments.Argument(0), "the client id")
			figures.RenewLive(r)
			rate, state, err := r.Db.ReadClientCurrentRate(r.Ctx, id, r.Now)
			if err != nil {
				return call.Fail(err)
			}
			return call.Value(figures.SampledRate(rate, state == samples.RateCurrent, r.Now))
		})
		call.Function(area, "byPeer", func(goja.FunctionCall) goja.Value {
			rates, err := figures.ReadCurrentRates(r)
			if err != nil {
				return call.Fail(err)
			}
			return call.Value(peerRates(rates.ByPeer, r.Now))
		})
		call.Function(area, "clientPeers", func(arguments goja.FunctionCall) goja.Value {
			id := call.IntegerOf(arguments.Argument(0), "the client id")
			rates, err := figures.ReadCurrentRates(r)
			if err != nil {
				return call.Fail(err)
			}
			return call.Value(peerRates(rates.ByClientPeer[id], r.Now))
		})
		call.Function(area, "none", func(goja.FunctionCall) goja.Value {
			return call.Value(figures.SampledRate(samples.CurrentRate{}, false, r.Now))
		})
	})
}

// peerRates renders rates keyed by peer address, each a chip rate: a peer is in the map only
// when it holds a reading, so none of them is a named absence.
func peerRates(rates map[string]samples.CurrentRate, now int64) map[string]figures.ChipRate {
	result := make(map[string]figures.ChipRate, len(rates))
	for peer, rate := range rates {
		result[peer] = figures.SampledRate(rate, true, now)
	}
	return result
}
