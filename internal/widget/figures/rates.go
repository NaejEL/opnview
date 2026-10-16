package figures

import (
	"sort"
	"time"

	"github.com/NaejEL/opnview/internal/store/samples"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/widget/envelope"
	"github.com/NaejEL/opnview/internal/widget/request"
	"github.com/NaejEL/opnview/internal/widget/traffic"
)

// Current rates, from the latest samples of the measurement_sample kind (store.ReadCurrentRates):
// a second measure, sampled, never summed with the logged one. They are read by subject kind and
// measure, from whichever provider wrote them, fine sample or baseline, and never from the firewall:
// a request for one renews the live-interest lease (renewLive), and the plugins declaring a live
// pass sample while it holds. A subject with no current sample reads no_current_sample, and one
// older than twice its cadence stale_sample: never a rate of 0.

// SampledRate renders one current rate at an instant, or its named absence.
func SampledRate(rate samples.CurrentRate, present bool, now int64) ChipRate {
	if !present {
		return ChipRate{State: string(samples.RateNoCurrentSample), Measure: envelope.MeasureSampled, LowerBound: true}
	}
	state := samples.RateCurrent
	if rate.Stale(now) {
		state = samples.RateStaleSample
	}
	in, at := rate.InBitsPerSecond, rate.SampledAt
	span, cadence := int64(vocab.SampleSeconds), rate.CadenceSeconds
	result := ChipRate{State: string(state), Measure: envelope.MeasureSampled, InBitsPerSecond: &in,
		OutMeasured: rate.OutMeasured, SampledAt: &at, SpanSeconds: &span, LowerBound: true}
	if cadence > 0 {
		result.CadenceSeconds = &cadence
	}
	if rate.OutMeasured {
		out := rate.OutBitsPerSecond
		result.OutBitsPerSecond = &out
	}
	return result
}

// KeyedRate is one subject's current rate.
type KeyedRate struct {
	Key  string   `json:"key"`
	Rate ChipRate `json:"rate"`
}

// CurrentRates are the current rates a "now" figure shows: per traffic direction, and per
// member of the grouping asked for.
type CurrentRates struct {
	ByDirection []KeyedRate `json:"by_direction"`
	// OutboundUnsplit is the outbound rate of local addresses whose peers lie both outside
	// and behind an interface, which no sample splits.
	OutboundUnsplit ChipRate    `json:"outbound_unsplit"`
	ByMember        []KeyedRate `json:"by_member"`
}

// RenewLive renews the live-interest lease: this request carries a live figure.
func RenewLive(r *request.Request) {
	r.LiveInterest.Renew(time.Unix(r.Now, 0))
}

// ReadCurrentRates reads the current rates for a live figure, renewing the lease.
func ReadCurrentRates(r *request.Request) (samples.CurrentRates, error) {
	RenewLive(r)
	return r.Db.ReadCurrentRates(r.Ctx, r.Now)
}

// CurrentRates reads the current rates, grouped by client or by interface.
func CurrentRatesOf(r *request.Request, groupBy string) (*CurrentRates, error) {
	rates, err := ReadCurrentRates(r)
	if err != nil {
		return nil, err
	}
	result := &CurrentRates{ByDirection: []KeyedRate{}, ByMember: []KeyedRate{},
		OutboundUnsplit: SampledRate(rates.OutboundUnsplit, rates.OutboundUnsplit.SampledAt != 0, r.Now)}
	for _, direction := range []string{traffic.DirectionOutbound, traffic.DirectionInbound, traffic.DirectionInterInterface} {
		rate, present := rates.ByDirection[direction]
		result.ByDirection = append(result.ByDirection, KeyedRate{Key: direction, Rate: SampledRate(rate, present, r.Now)})
	}
	switch groupBy {
	case "client":
		for id, rate := range rates.ByClient {
			result.ByMember = append(result.ByMember, KeyedRate{Key: traffic.ClientKey(id), Rate: SampledRate(rate, true, r.Now)})
		}
	case "interface":
		cache, err := r.Install()
		if err != nil {
			return nil, err
		}
		for device, rate := range rates.ByDevice {
			key := "device:" + device
			if info, known := cache.ByDevice[device]; known {
				key = traffic.InterfaceKey(info.ID)
			}
			result.ByMember = append(result.ByMember, KeyedRate{Key: key, Rate: SampledRate(rate, true, r.Now)})
		}
	}
	sort.Slice(result.ByMember, func(i, j int) bool { return result.ByMember[i].Key < result.ByMember[j].Key })
	return result, nil
}
