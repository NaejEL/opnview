package widget

import (
	"sort"

	"github.com/NaejEL/opnview/internal/store"
)

// Current rates, from the latest traffic/top samples (store.ReadCurrentRates): a second
// measure, sampled, never summed with the logged one. A subject with no current sample
// reads no_current_sample, never a rate of 0.

// sampledRate renders one current rate, or its named absence.
func sampledRate(rate store.CurrentRate, present bool) ChipRate {
	if !present {
		return ChipRate{State: string(store.RateNoCurrentSample), Measure: MeasureSampled}
	}
	in, at := rate.InBitsPerSecond, rate.SampledAt
	result := ChipRate{State: string(store.RateCurrent), Measure: MeasureSampled, InBitsPerSecond: &in,
		OutMeasured: rate.OutMeasured, SampledAt: &at}
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

// currentRates reads the current rates, grouped by client or by interface.
func (r *Request) currentRates(groupBy string) (*CurrentRates, error) {
	rates, err := r.db.ReadCurrentRates(r.ctx, r.now)
	if err != nil {
		return nil, err
	}
	result := &CurrentRates{ByDirection: []KeyedRate{}, ByMember: []KeyedRate{},
		OutboundUnsplit: sampledRate(rates.OutboundUnsplit, rates.OutboundUnsplit.SampledAt != 0)}
	for _, direction := range []string{directionOutbound, directionInbound, directionInterInterface} {
		rate, present := rates.ByDirection[direction]
		result.ByDirection = append(result.ByDirection, KeyedRate{Key: direction, Rate: sampledRate(rate, present)})
	}
	switch groupBy {
	case "client":
		for id, rate := range rates.ByClient {
			result.ByMember = append(result.ByMember, KeyedRate{Key: clientKey(id), Rate: sampledRate(rate, true)})
		}
	case "interface":
		cache, err := r.install()
		if err != nil {
			return nil, err
		}
		for device, rate := range rates.ByDevice {
			key := "device:" + device
			if info, known := cache.byDevice[device]; known {
				key = interfaceKey(info.ID)
			}
			result.ByMember = append(result.ByMember, KeyedRate{Key: key, Rate: sampledRate(rate, true)})
		}
	}
	sort.Slice(result.ByMember, func(i, j int) bool { return result.ByMember[i].Key < result.ByMember[j].Key })
	return result, nil
}
