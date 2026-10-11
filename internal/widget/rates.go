package widget

import (
	"sort"
	"time"

	"github.com/NaejEL/opnview/internal/live"
	"github.com/NaejEL/opnview/internal/store"
)

// Current rates, from the latest samples of the measurement_sample kind (store.ReadCurrentRates):
// a second measure, sampled, never summed with the logged one. They are read by subject kind and
// measure, from whichever provider wrote them, fine sample or baseline, and never from the firewall:
// a request for one renews the live-interest lease (renewLive), and the plugins declaring a live
// pass sample while it holds. A subject with no current sample reads no_current_sample, and one
// older than twice its cadence stale_sample: never a rate of 0.

// SampledRate renders one current rate at an instant, or its named absence.
func SampledRate(rate store.CurrentRate, present bool, now int64) ChipRate {
	if !present {
		return ChipRate{State: string(store.RateNoCurrentSample), Measure: MeasureSampled, LowerBound: true}
	}
	state := store.RateCurrent
	if rate.Stale(now) {
		state = store.RateStaleSample
	}
	in, at := rate.InBitsPerSecond, rate.SampledAt
	span, cadence := int64(store.SampleSeconds), rate.CadenceSeconds
	result := ChipRate{State: string(state), Measure: MeasureSampled, InBitsPerSecond: &in,
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

// renewLive renews the live-interest lease: this request carries a live figure.
func (r *Request) renewLive() {
	r.liveInterest.Renew(time.Unix(r.Now, 0))
}

// readCurrentRates reads the current rates for a live figure, renewing the lease.
func (r *Request) readCurrentRates() (store.CurrentRates, error) {
	r.renewLive()
	return r.Db.ReadCurrentRates(r.Ctx, r.Now)
}

// CurrentRates reads the current rates, grouped by client or by interface.
func (r *Request) CurrentRates(groupBy string) (*CurrentRates, error) {
	rates, err := r.readCurrentRates()
	if err != nil {
		return nil, err
	}
	result := &CurrentRates{ByDirection: []KeyedRate{}, ByMember: []KeyedRate{},
		OutboundUnsplit: SampledRate(rates.OutboundUnsplit, rates.OutboundUnsplit.SampledAt != 0, r.Now)}
	for _, direction := range []string{DirectionOutbound, DirectionInbound, DirectionInterInterface} {
		rate, present := rates.ByDirection[direction]
		result.ByDirection = append(result.ByDirection, KeyedRate{Key: direction, Rate: SampledRate(rate, present, r.Now)})
	}
	switch groupBy {
	case "client":
		for id, rate := range rates.ByClient {
			result.ByMember = append(result.ByMember, KeyedRate{Key: ClientKey(id), Rate: SampledRate(rate, true, r.Now)})
		}
	case "interface":
		cache, err := r.Install()
		if err != nil {
			return nil, err
		}
		for device, rate := range rates.ByDevice {
			key := "device:" + device
			if info, known := cache.ByDevice[device]; known {
				key = InterfaceKey(info.ID)
			}
			result.ByMember = append(result.ByMember, KeyedRate{Key: key, Rate: SampledRate(rate, true, r.Now)})
		}
	}
	sort.Slice(result.ByMember, func(i, j int) bool { return result.ByMember[i].Key < result.ByMember[j].Key })
	return result, nil
}

// SetLiveInterest gives the service the live-interest lease its live figures renew. It is called
// once, before the service serves anything.
func (s *Service) SetLiveInterest(interest *live.Interest) { s.liveInterest = interest }

// LiveInterest is the lease the service's live figures renew; nil when none was given.
func (s *Service) LiveInterest() *live.Interest { return s.liveInterest }

// Store is the database the service reads, for a surface serving a live figure beside it.
func (s *Service) Store() *store.Store { return s.store }
