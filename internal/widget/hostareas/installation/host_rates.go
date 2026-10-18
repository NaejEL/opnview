package installation

import (
	"strconv"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/store/samples"
	"github.com/NaejEL/opnview/internal/store/vocab"
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
//	host.rates.series(window)    every sampling instant of [from, to), fine and baseline, oldest first,
//	                             each split per direction as the current rates are: {span_seconds,
//	                             instants: [{at, by_direction, outbound_unsplit}]}, by_direction a map
//	                             from each direction read at that instant to {in_bits_per_second,
//	                             out_bits_per_second}. A direction absent from an instant read nothing
//	                             there, which is a measured 0 at a sampled instant, and an instant
//	                             absent from the list was not sampled -- never a rate of 0. It renews
//	                             no lease: it reads history

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
		call.Function(area, "series", func(arguments goja.FunctionCall) goja.Value {
			window := call.IntervalOf(arguments.Argument(0), "the window")
			instants, err := r.Db.ReadRateSeries(r.Ctx, window.From, window.To)
			if err != nil {
				return call.Fail(err)
			}
			series := rateSeries{SpanSeconds: vocab.SampleSeconds, Instants: make([]rateInstant, 0, len(instants))}
			for _, instant := range instants {
				entry := rateInstant{At: instant.At, ByDirection: map[string]rateFigures{},
					OutboundUnsplit: figuresOf(instant.Rates.OutboundUnsplit)}
				for direction, rate := range instant.Rates.ByDirection {
					entry.ByDirection[direction] = figuresOf(rate)
				}
				series.Instants = append(series.Instants, entry)
			}
			return call.Value(series)
		})
		call.Function(area, "none", func(goja.FunctionCall) goja.Value {
			return call.Value(figures.SampledRate(samples.CurrentRate{}, false, r.Now))
		})
	})
}

// rateSeries is host.rates.series: the sampling instants of a window.
type rateSeries struct {
	SpanSeconds int64         `json:"span_seconds"`
	Instants    []rateInstant `json:"instants"`
}

// rateInstant is one sampling instant, split per direction.
type rateInstant struct {
	At              int64                  `json:"at"`
	ByDirection     map[string]rateFigures `json:"by_direction"`
	OutboundUnsplit rateFigures            `json:"outbound_unsplit"`
}

// rateFigures are a rate's two figures, relative to the local addresses.
type rateFigures struct {
	In  float64 `json:"in_bits_per_second"`
	Out float64 `json:"out_bits_per_second"`
}

func figuresOf(rate samples.CurrentRate) rateFigures {
	return rateFigures{In: rate.InBitsPerSecond, Out: rate.OutBitsPerSecond}
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
