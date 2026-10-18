package samples

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/NaejEL/opnview/internal/store/accounts"
	"github.com/NaejEL/opnview/internal/store/flows"
	"github.com/NaejEL/opnview/internal/store/reads"
	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The read functions over measurement_sample: current rates, bytes seen in
// samples, and per-interface throughput.
//
// WHAT /api/diagnostics/traffic/top MEASURES, read at opnsense/core 26.7.3
// (scripts/interfaces/traffic_top.py): it runs iftop on each interface with
// `-s 2`, one text output after two seconds, and reports per local address and
// per peer a rate_bits figure -- iftop's average over the last two seconds -- and
// a cumulative_bytes figure, the bytes of that same two-second capture. So:
//
//   - a CURRENT RATE is a rate read in a recent sample. A sample is current for
//     twice the configured measurement interval; beyond that there is no current
//     sample, which is a state and never a rate of 0;
//   - SAMPLED BYTES are the bytes seen in samples, two seconds per sample, carried
//     with the seconds sampled as their coverage and NEVER extrapolated to a
//     period. A period with no sample is "not sampled", never 0.
//
// It is a second measure, distinct from logged bytes, and neither is the volume of
// a conversation.

// MeasurementIntervalKey is the setting row holding the sampled-measurement
// interval, in seconds, and DefaultMeasurementIntervalSeconds the interval in force
// when the row is absent. internal/config reads the same row and derives its
// default from this constant, so the interval the sampler runs at and the bound a
// current rate is read within cannot drift apart.
const (
	MeasurementIntervalKey            = "poll_interval_measurement_seconds"
	DefaultMeasurementIntervalSeconds = 300
)

// MeasurementIntervalSeconds reads the configured baseline sampling interval: the
// cadence every baseline sample was taken at.
func (s *SampleStore) MeasurementIntervalSeconds(ctx context.Context) (int64, error) {
	interval := int64(DefaultMeasurementIntervalSeconds)
	value, present, err := accounts.On(s.Handle).Setting(ctx, MeasurementIntervalKey)
	if err != nil {
		return 0, err
	}
	if present {
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || parsed <= 0 {
			return 0, fmt.Errorf("store: the setting %s is %q, not a positive whole number of seconds",
				MeasurementIntervalKey, value)
		}
		interval = parsed
	}
	return interval, nil
}

// CurrentSampleBound is how far back a sample still counts as current: twice the
// configured measurement interval, read from the setting row. One missed sampling
// pass therefore does not turn a rate into "no current sample"; two do.
func (s *SampleStore) CurrentSampleBound(ctx context.Context) (int64, error) {
	interval, err := s.MeasurementIntervalSeconds(ctx)
	if err != nil {
		return 0, err
	}
	return 2 * interval, nil
}

// CurrentRate is a current rate, in bits per second, read from the latest samples.
type CurrentRate struct {
	// InBitsPerSecond and OutBitsPerSecond are the sums of the latest rate_bits_in
	// and rate_bits_out readings, relative to the local address.
	InBitsPerSecond  float64
	OutBitsPerSecond float64
	// OutMeasured is true when every reading summed carried an outbound figure.
	// When it is false, OutBitsPerSecond is the sum of the figures that were
	// reported and not a measurement of the whole: the zero of a subject nobody
	// measured outbound is never a rate of 0.
	OutMeasured bool
	// OldestSampledAt and SampledAt bound the instants of the readings summed.
	OldestSampledAt int64
	SampledAt       int64
	// CadenceSeconds is the cadence the readings summed were taken at: a fine sample's
	// live cadence, a baseline sample's measurement interval -- the coarsest of them
	// when several are summed. It is what the rate's age is judged against (Stale).
	CadenceSeconds int64
	// outSeen and outMissing count the readings summed with and without an
	// outbound figure.
	outSeen, outMissing int
}

// Total is the two directions together.
func (r CurrentRate) Total() float64 { return r.InBitsPerSecond + r.OutBitsPerSecond }

// Stale reports whether the rate is older than its cadence allows: its oldest reading
// was taken more than two cadences before now. One missed pass is tolerated, two are
// not -- the same rule CurrentSampleBound applies to the baseline. A stale rate is a
// named state with its figures beside it, never a rate of 0.
func (r CurrentRate) Stale(now int64) bool {
	return r.CadenceSeconds > 0 && now-r.OldestSampledAt > 2*r.CadenceSeconds
}

func (r *CurrentRate) stamp(sampledAt, cadence int64) {
	if r.SampledAt == 0 || sampledAt > r.SampledAt {
		r.SampledAt = sampledAt
	}
	if r.OldestSampledAt == 0 || sampledAt < r.OldestSampledAt {
		r.OldestSampledAt = sampledAt
	}
	if cadence > r.CadenceSeconds {
		r.CadenceSeconds = cadence
	}
}

// addIn adds an inbound figure.
func (r *CurrentRate) addIn(in float64, sampledAt, cadence int64) {
	r.stamp(sampledAt, cadence)
	r.InBitsPerSecond += in
}

// addOut adds an outbound figure, or records that a reading carried none.
func (r *CurrentRate) addOut(out *float64, sampledAt, cadence int64) {
	r.stamp(sampledAt, cadence)
	if out == nil {
		r.outMissing++
	} else {
		r.OutBitsPerSecond += *out
		r.outSeen++
	}
	r.OutMeasured = r.outSeen > 0 && r.outMissing == 0
}

// add adds one subject's two figures.
func (r *CurrentRate) add(in float64, out *float64, sampledAt, cadence int64) {
	r.addIn(in, sampledAt, cadence)
	r.addOut(out, sampledAt, cadence)
}

// CurrentRates are the current rates per subject. A subject absent from a map has
// NO CURRENT SAMPLE: nothing was sampled for it within the bound.
type CurrentRates struct {
	// ByClient is per client id, from the totals of the client's local addresses.
	ByClient map[int64]CurrentRate
	// ByDevice is per network device the readings were taken on.
	ByDevice map[string]CurrentRate
	// ByOwner is per owner id, with the key 0 for the unassigned bucket.
	ByOwner map[int64]CurrentRate
	// ByDirection is per traffic direction: outbound, inbound or inter_interface.
	ByDirection map[string]CurrentRate
	// ByPeer is per peer address: the per-pair readings (interface_endpoint_pair) of every
	// inside local address that peer was read with. Its inbound figure is what the peer sent to
	// those local addresses; its outbound figure what they sent to it, measured only for a local
	// address read with that one peer (traffic/top reports a local address's sending only as its
	// total over every peer), so OutMeasured is false whenever one of the pairs summed carried no
	// outbound figure. A pair whose local end is outside -- the firewall's own upstream address,
	// the same conversation read again after NAT -- is not summed.
	ByPeer map[string]CurrentRate
	// ByClientPeer is the same per client id, then per peer address: one client's current rate
	// with each peer it was read with.
	ByClientPeer map[int64]map[string]CurrentRate
	// OutboundUnsplit is the outbound rate of the inside local addresses whose peers
	// lie both outside and behind an interface. traffic/top reports a local
	// address's sending only as its total over every peer, so that total cannot be
	// split between outbound and inter_interface. It is reported here, whole,
	// with a zero SampledAt when no address is in that case,
	// rather than guessed into either direction or dropped from both.
	OutboundUnsplit CurrentRate
}

func newCurrentRates() CurrentRates {
	return CurrentRates{
		ByClient: map[int64]CurrentRate{}, ByDevice: map[string]CurrentRate{},
		ByOwner: map[int64]CurrentRate{}, ByDirection: map[string]CurrentRate{},
		ByPeer: map[string]CurrentRate{}, ByClientPeer: map[int64]map[string]CurrentRate{},
	}
}

// endpointReading is the latest totals of one local address on one device.
type endpointReading struct {
	device, local string
	in, out       *float64
	sampledAt     int64
	cadence       int64
}

// pairReading is the latest reading of one interface endpoint pair.
type pairReading struct {
	device, local, peer string
	// in is what the peer sent to the local address; out what the local address sent to the
	// peer, read only when the local address had that one peer.
	in, out   *float64
	sampledAt int64
	cadence   int64
}

// endpointKey names one local address read on one device, and pairKey one pair: the parts the
// provider reported, compared as they are and never joined into a text.
type endpointKey struct{ device, local string }

type pairKey struct{ device, local, peer string }

// rateRow is one row of read_pair_rates.
type rateRow struct {
	kind, device, local, peer, measure string
	value                              float64
	sampledAt                          int64
	// cadence is the live cadence of a fine sample, or the measurement interval.
	cadence int64
}

// readRateRows reads the rate readings of both sampled subjects taken in [from, to].
// The rows are read by SUBJECT KIND AND MEASURE, never by provider: whichever
// provider of the measurement_sample kind wrote a rate is read the same way.
func (s *SampleStore) readRateRows(ctx context.Context, from, to, interval int64) ([]rateRow, error) {
	text, err := sqldb.Statement("read_pair_rates")
	if err != nil {
		return nil, err
	}
	rows, err := sqldb.Query(ctx, s.Reader, text, map[string]any{"since": from, "now": to})
	if err != nil {
		return nil, fmt.Errorf("store: read_pair_rates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var result []rateRow
	for rows.Next() {
		var (
			row  rateRow
			live reads.NullInt64
		)
		if err := rows.Scan(&row.kind, &row.device, &row.local, &row.peer, &row.measure, &row.value, &row.sampledAt,
			&live); err != nil {
			return nil, fmt.Errorf("store: read_pair_rates: %w", err)
		}
		row.cadence = interval
		if live.Valid {
			row.cadence = live.Int64
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read_pair_rates: %w", err)
	}
	return result, nil
}

// latestReadings keeps each subject's latest reading among the rows.
func latestReadings(rows []rateRow) (map[endpointKey]*endpointReading, map[pairKey]*pairReading) {
	endpoints := map[endpointKey]*endpointReading{}
	pairs := map[pairKey]*pairReading{}
	for _, row := range rows {
		figure := row.value
		switch vocab.SubjectKind(row.kind) {
		case vocab.SubjectInterfaceEndpoint:
			key := endpointKey{device: row.device, local: row.local}
			reading, present := endpoints[key]
			if !present || row.sampledAt > reading.sampledAt {
				reading = &endpointReading{device: row.device, local: row.local, sampledAt: row.sampledAt,
					cadence: row.cadence}
				endpoints[key] = reading
			} else if row.sampledAt < reading.sampledAt {
				continue
			}
			switch vocab.Measure(row.measure) {
			case vocab.MeasureRateBitsIn:
				reading.in = &figure
			case vocab.MeasureRateBitsOut:
				reading.out = &figure
			}
		case vocab.SubjectInterfaceEndpointPair:
			key := pairKey{device: row.device, local: row.local, peer: row.peer}
			reading, present := pairs[key]
			if !present || row.sampledAt > reading.sampledAt {
				reading = &pairReading{device: row.device, local: row.local, peer: row.peer, sampledAt: row.sampledAt,
					cadence: row.cadence}
				pairs[key] = reading
			} else if row.sampledAt < reading.sampledAt {
				continue
			}
			switch vocab.Measure(row.measure) {
			case vocab.MeasureRateBitsIn:
				reading.in = &figure
			case vocab.MeasureRateBitsOut:
				reading.out = &figure
			}
		}
	}
	return endpoints, pairs
}

// rateSplitter places local addresses inside or outside, caching every answer for the
// life of one read.
type rateSplitter struct {
	store    *SampleStore
	prefixes []flows.PrefixEvidence
	inside   map[string]bool
}

func (s *SampleStore) newRateSplitter(ctx context.Context) (*rateSplitter, error) {
	prefixes, err := flows.LoadPrefixes(ctx, s.Reader)
	if err != nil {
		return nil, err
	}
	return &rateSplitter{store: s, prefixes: prefixes, inside: map[string]bool{}}, nil
}

func (r *rateSplitter) isInside(ctx context.Context, address string) (bool, error) {
	if known, present := r.inside[address]; present {
		return known, nil
	}
	interfaceID, err := flows.Membership(ctx, r.store.Reader, address, r.prefixes)
	if err != nil {
		return false, err
	}
	r.inside[address] = interfaceID != nil
	return interfaceID != nil, nil
}

// split turns one set of readings into rates per device and per direction, and, when
// withClients is true, per client and per owner.
//
// The totals of each local address -- the interface_endpoint subject -- are what a
// device, a client and an owner sum, because a local address's outbound figure
// exists only as that total. A client, an owner and a direction count only the
// local addresses that are inside: the same conversation is read on the client's
// interface and again on the upstream one, and the second reading's local address
// is the firewall's own upstream address, which is outside.
//
// Per direction, the inbound figures are per peer and are placed by the peer. The
// outbound total of a local address goes to outbound when every peer it was read
// with is outside, to inter_interface when every one is inside, and to
// OutboundUnsplit otherwise.
func (r *rateSplitter) split(ctx context.Context, endpoints map[endpointKey]*endpointReading,
	pairs map[pairKey]*pairReading, withClients bool) (CurrentRates, error) {
	result := newCurrentRates()
	// The peers each local address was read with in its latest sample.
	peersOf := map[endpointKey][]*pairReading{}
	for _, pair := range pairs {
		key := endpointKey{device: pair.device, local: pair.local}
		if endpoint, present := endpoints[key]; present && endpoint.sampledAt == pair.sampledAt {
			peersOf[key] = append(peersOf[key], pair)
		}
	}
	// In order of device, then local address: the order the sums are made in, so a figure is the
	// same sum every time.
	keys := make([]endpointKey, 0, len(endpoints))
	for key := range endpoints {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].device != keys[j].device {
			return keys[i].device < keys[j].device
		}
		return keys[i].local < keys[j].local
	})
	for _, key := range keys {
		endpoint := endpoints[key]
		in := 0.0
		if endpoint.in != nil {
			in = *endpoint.in
		}
		addRate(result.ByDevice, endpoint.device, in, endpoint.out, endpoint.sampledAt, endpoint.cadence)

		localInside, err := r.isInside(ctx, endpoint.local)
		if err != nil {
			return result, err
		}
		if !localInside {
			continue
		}

		peers := peersOf[key]
		sort.Slice(peers, func(i, j int) bool { return peers[i].peer < peers[j].peer })
		outside, between := 0, 0
		for _, pair := range peers {
			peerInside, err := r.isInside(ctx, pair.peer)
			if err != nil {
				return result, err
			}
			direction := "inbound"
			if peerInside {
				direction = "inter_interface"
				between++
			} else {
				outside++
			}
			if pair.in != nil {
				rate := result.ByDirection[direction]
				rate.addIn(*pair.in, pair.sampledAt, pair.cadence)
				result.ByDirection[direction] = rate
			}
		}
		// The local address's outbound total, or the record that it carried none.
		outDirection := ""
		switch {
		case outside > 0 && between == 0:
			outDirection = "outbound"
		case between > 0 && outside == 0:
			outDirection = "inter_interface"
		}
		if outDirection == "" {
			result.OutboundUnsplit.addOut(endpoint.out, endpoint.sampledAt, endpoint.cadence)
		} else {
			rate := result.ByDirection[outDirection]
			rate.addOut(endpoint.out, endpoint.sampledAt, endpoint.cadence)
			result.ByDirection[outDirection] = rate
		}

		if !withClients {
			continue
		}
		for _, pair := range peers {
			addPairRate(result.ByPeer, pair.peer, pair)
		}
		clientID, found, err := sqldb.QueryOptionalInt(ctx, r.store.Reader, "client_identity_at_address",
			map[string]any{"address": endpoint.local})
		if err != nil {
			return result, err
		}
		if !found {
			clientID, found, err = r.store.addressLevelClient(ctx, endpoint.local)
			if err != nil {
				return result, err
			}
		}
		if !found {
			continue
		}
		addRate(result.ByClient, clientID, in, endpoint.out, endpoint.sampledAt, endpoint.cadence)
		if len(peers) > 0 && result.ByClientPeer[clientID] == nil {
			result.ByClientPeer[clientID] = map[string]CurrentRate{}
		}
		for _, pair := range peers {
			addPairRate(result.ByClientPeer[clientID], pair.peer, pair)
		}
		ownerKey, _, err := sqldb.QueryOptionalInt(ctx, r.store.Reader, "read_client_owner",
			map[string]any{"client_id": clientID})
		if err != nil {
			return result, err
		}
		addRate(result.ByOwner, ownerKey, in, endpoint.out, endpoint.sampledAt, endpoint.cadence)
	}
	return result, nil
}

// ReadCurrentRates reads the rates sampled within CurrentSampleBound of now and
// keeps each subject's latest reading, fine or baseline: a fine sample is newer than
// the baseline one whenever a live pass ran since, and is then the current rate.
func (s *SampleStore) ReadCurrentRates(ctx context.Context, now int64) (CurrentRates, error) {
	interval, err := s.MeasurementIntervalSeconds(ctx)
	if err != nil {
		return newCurrentRates(), err
	}
	rows, err := s.readRateRows(ctx, now-2*interval, now, interval)
	if err != nil {
		return newCurrentRates(), err
	}
	splitter, err := s.newRateSplitter(ctx)
	if err != nil {
		return newCurrentRates(), err
	}
	endpoints, pairs := latestReadings(rows)
	return splitter.split(ctx, endpoints, pairs, true)
}

// RateInstant is the rates of one sampling instant: what every reading taken at that
// instant says, per direction and per device.
type RateInstant struct {
	// At is the instant.
	At int64
	// Rates are the rates of the readings taken then; ByClient and ByOwner are empty.
	Rates CurrentRates
}

// ReadRateSeries reads every sampling instant in [from, to), fine and baseline, oldest
// first, each split per direction and per device exactly as ReadCurrentRates splits the
// latest one. It is what the rolling strip draws its throughput from: an instant is a
// sample, and a bucket of the strip holding none is a gap, never a rate of 0.
func (s *SampleStore) ReadRateSeries(ctx context.Context, from, to int64) ([]RateInstant, error) {
	if to <= from {
		return nil, nil
	}
	interval, err := s.MeasurementIntervalSeconds(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.readRateRows(ctx, from, to-1, interval)
	if err != nil {
		return nil, err
	}
	byInstant := map[int64][]rateRow{}
	for _, row := range rows {
		byInstant[row.sampledAt] = append(byInstant[row.sampledAt], row)
	}
	instants := make([]int64, 0, len(byInstant))
	for at := range byInstant {
		instants = append(instants, at)
	}
	sort.Slice(instants, func(i, j int) bool { return instants[i] < instants[j] })
	splitter, err := s.newRateSplitter(ctx)
	if err != nil {
		return nil, err
	}
	series := make([]RateInstant, 0, len(instants))
	for _, at := range instants {
		endpoints, pairs := latestReadings(byInstant[at])
		rates, err := splitter.split(ctx, endpoints, pairs, false)
		if err != nil {
			return nil, err
		}
		series = append(series, RateInstant{At: at, Rates: rates})
	}
	return series, nil
}

// addressLevelClient returns the most recent address-level client at an address.
func (s *SampleStore) addressLevelClient(ctx context.Context, address string) (int64, bool, error) {
	return sqldb.QueryOptionalInt(ctx, s.Reader, "read_address_level_client", map[string]any{"address": address})
}

func addRate[K comparable](rates map[K]CurrentRate, key K, in float64, out *float64, sampledAt, cadence int64) {
	rate := rates[key]
	rate.add(in, out, sampledAt, cadence)
	rates[key] = rate
}

// addPairRate adds one per-pair reading under a peer: its inbound figure, and its outbound one
// or the record that it carried none. A pair with neither figure adds nothing.
func addPairRate(rates map[string]CurrentRate, peer string, pair *pairReading) {
	if pair.in == nil && pair.out == nil {
		return
	}
	in := 0.0
	if pair.in != nil {
		in = *pair.in
	}
	addRate(rates, peer, in, pair.out, pair.sampledAt, pair.cadence)
}

// ClientRateState says whether a client has a current rate.
type ClientRateState string

// The two states of a client's current rate.
const (
	// RateCurrent is a rate read within the bound.
	RateCurrent ClientRateState = "current"
	// RateNoCurrentSample is no reading within the bound: not a rate of 0.
	RateNoCurrentSample ClientRateState = "no_current_sample"
	// RateStaleSample is a rate whose oldest reading is older than twice the cadence
	// it was taken at (CurrentRate.Stale): a live pass that stopped answering while
	// somebody looks. Its figures are served beside the state, never as a rate of 0.
	RateStaleSample ClientRateState = "stale_sample"
)

// ReadClientCurrentRate returns one client's current rate, or the no-current-sample
// state.
func (s *SampleStore) ReadClientCurrentRate(ctx context.Context, clientID, now int64) (
	CurrentRate, ClientRateState, error) {
	rates, err := s.ReadCurrentRates(ctx, now)
	if err != nil {
		return CurrentRate{}, "", err
	}
	rate, present := rates.ByClient[clientID]
	if !present {
		return CurrentRate{}, RateNoCurrentSample, nil
	}
	return rate, RateCurrent, nil
}

// ThroughputPoint is the throughput between two consecutive counter samples.
type ThroughputPoint struct {
	// From and To are the two samples' instants.
	From int64
	To   int64
	// PerSecond is the counter's increase divided by the seconds between the two
	// samples, and nil when the counter decreased.
	PerSecond *float64
	// Reset says the counter decreased between the two samples: the interface's
	// counters were reset or the firewall restarted. It is never a negative rate.
	Reset bool
}

// ReadInterfaceThroughput derives the throughput of one counter -- bytes_in,
// bytes_out, packets_in, packets_out, errors_in or errors_out -- of one network
// device over [from, to), from consecutive samples.
func (s *SampleStore) ReadInterfaceThroughput(ctx context.Context, device string, measure vocab.Measure,
	from, to int64) ([]ThroughputPoint, error) {
	text, err := sqldb.Statement("read_interface_counter")
	if err != nil {
		return nil, err
	}
	rows, err := sqldb.Query(ctx, s.Reader, text, map[string]any{"device": device, "measure": string(measure), "from": from, "to": to})
	if err != nil {
		return nil, fmt.Errorf("store: read_interface_counter: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var (
		points     []ThroughputPoint
		previous   float64
		previousAt int64
		have       bool
	)
	for rows.Next() {
		var (
			sampledAt int64
			value     float64
		)
		if err := rows.Scan(&sampledAt, &value); err != nil {
			return nil, fmt.Errorf("store: read_interface_counter: %w", err)
		}
		if have && sampledAt > previousAt {
			point := ThroughputPoint{From: previousAt, To: sampledAt}
			if value < previous {
				point.Reset = true
			} else {
				perSecond := (value - previous) / float64(sampledAt-previousAt)
				point.PerSecond = &perSecond
			}
			points = append(points, point)
		}
		previous, previousAt, have = value, sampledAt, true
	}
	return points, rows.Err()
}
