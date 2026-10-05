package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
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

// CurrentSampleBound is how far back a sample still counts as current: twice the
// configured measurement interval, read from the setting row. One missed sampling
// pass therefore does not turn a rate into "no current sample"; two do.
func (s *Store) CurrentSampleBound(ctx context.Context) (int64, error) {
	interval := int64(DefaultMeasurementIntervalSeconds)
	value, present, err := s.Setting(ctx, MeasurementIntervalKey)
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
	// outSeen and outMissing count the readings summed with and without an
	// outbound figure.
	outSeen, outMissing int
}

// Total is the two directions together.
func (r CurrentRate) Total() float64 { return r.InBitsPerSecond + r.OutBitsPerSecond }

func (r *CurrentRate) stamp(sampledAt int64) {
	if r.SampledAt == 0 || sampledAt > r.SampledAt {
		r.SampledAt = sampledAt
	}
	if r.OldestSampledAt == 0 || sampledAt < r.OldestSampledAt {
		r.OldestSampledAt = sampledAt
	}
}

// addIn adds an inbound figure.
func (r *CurrentRate) addIn(in float64, sampledAt int64) {
	r.stamp(sampledAt)
	r.InBitsPerSecond += in
}

// addOut adds an outbound figure, or records that a reading carried none.
func (r *CurrentRate) addOut(out *float64, sampledAt int64) {
	r.stamp(sampledAt)
	if out == nil {
		r.outMissing++
	} else {
		r.OutBitsPerSecond += *out
		r.outSeen++
	}
	r.OutMeasured = r.outSeen > 0 && r.outMissing == 0
}

// add adds one subject's two figures.
func (r *CurrentRate) add(in float64, out *float64, sampledAt int64) {
	r.addIn(in, sampledAt)
	r.addOut(out, sampledAt)
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
	// OutboundUnsplit is the outbound rate of the inside local addresses whose peers
	// lie both outside and behind an interface. traffic/top reports a local
	// address's sending only as its total over every peer, so that total cannot be
	// split between outbound and inter_interface. It is reported here, whole,
	// with a zero SampledAt when no address is in that case,
	// rather than guessed into either direction or dropped from both.
	OutboundUnsplit CurrentRate
}

// endpointReading is the latest totals of one local address on one device.
type endpointReading struct {
	device, local string
	in, out       *float64
	sampledAt     int64
}

// pairReading is the latest reading of one interface endpoint pair.
type pairReading struct {
	device, local, peer string
	in                  *float64
	sampledAt           int64
}

// ReadCurrentRates reads the rates sampled within CurrentSampleBound of now and
// keeps each subject's latest reading.
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
func (s *Store) ReadCurrentRates(ctx context.Context, now int64) (CurrentRates, error) {
	result := CurrentRates{
		ByClient: map[int64]CurrentRate{}, ByDevice: map[string]CurrentRate{},
		ByOwner: map[int64]CurrentRate{}, ByDirection: map[string]CurrentRate{},
	}
	bound, err := s.CurrentSampleBound(ctx)
	if err != nil {
		return result, err
	}
	text, err := Statement("read_pair_rates")
	if err != nil {
		return result, err
	}
	rows, err := s.db.QueryContext(ctx, text, sql.Named("since", now-bound), sql.Named("now", now))
	if err != nil {
		return result, fmt.Errorf("store: read_pair_rates: %w", err)
	}
	endpoints := map[string]*endpointReading{}
	pairs := map[string]*pairReading{}
	for rows.Next() {
		var (
			kind, key, measure string
			value              float64
			sampledAt          int64
		)
		if err := rows.Scan(&kind, &key, &measure, &value, &sampledAt); err != nil {
			_ = rows.Close()
			return result, fmt.Errorf("store: read_pair_rates: %w", err)
		}
		parts := strings.Split(key, " ")
		figure := value
		switch {
		case SubjectKind(kind) == SubjectInterfaceEndpoint && len(parts) == 2:
			reading, present := endpoints[key]
			if !present || sampledAt > reading.sampledAt {
				reading = &endpointReading{device: parts[0], local: parts[1], sampledAt: sampledAt}
				endpoints[key] = reading
			} else if sampledAt < reading.sampledAt {
				continue
			}
			switch Measure(measure) {
			case MeasureRateBitsIn:
				reading.in = &figure
			case MeasureRateBitsOut:
				reading.out = &figure
			}
		case SubjectKind(kind) == SubjectInterfaceEndpointPair && len(parts) == 3:
			reading, present := pairs[key]
			if !present || sampledAt > reading.sampledAt {
				reading = &pairReading{device: parts[0], local: parts[1], peer: parts[2], sampledAt: sampledAt}
				pairs[key] = reading
			} else if sampledAt < reading.sampledAt {
				continue
			}
			if Measure(measure) == MeasureRateBitsIn {
				reading.in = &figure
			}
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return result, fmt.Errorf("store: read_pair_rates: %w", err)
	}

	// The peers each local address was read with in its latest sample.
	peersOf := map[string][]*pairReading{}
	for _, pair := range pairs {
		key := InterfaceEndpointKey(pair.device, pair.local)
		if endpoint, present := endpoints[key]; present && endpoint.sampledAt == pair.sampledAt {
			peersOf[key] = append(peersOf[key], pair)
		}
	}

	prefixes, err := loadPrefixes(ctx, s.db)
	if err != nil {
		return result, err
	}
	insideCache := map[string]bool{}
	inside := func(address string) (bool, error) {
		if known, present := insideCache[address]; present {
			return known, nil
		}
		interfaceID, err := membership(ctx, s.db, address, prefixes)
		if err != nil {
			return false, err
		}
		insideCache[address] = interfaceID != nil
		return interfaceID != nil, nil
	}

	keys := make([]string, 0, len(endpoints))
	for key := range endpoints {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		endpoint := endpoints[key]
		in := 0.0
		if endpoint.in != nil {
			in = *endpoint.in
		}
		addRate(result.ByDevice, endpoint.device, in, endpoint.out, endpoint.sampledAt)

		localInside, err := inside(endpoint.local)
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
			peerInside, err := inside(pair.peer)
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
				rate.addIn(*pair.in, pair.sampledAt)
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
			result.OutboundUnsplit.addOut(endpoint.out, endpoint.sampledAt)
		} else {
			rate := result.ByDirection[outDirection]
			rate.addOut(endpoint.out, endpoint.sampledAt)
			result.ByDirection[outDirection] = rate
		}

		clientID, found, err := queryOptionalInt(ctx, s.db, "client_identity_at_address",
			map[string]any{"address": endpoint.local})
		if err != nil {
			return result, err
		}
		if !found {
			clientID, found, err = s.addressLevelClient(ctx, endpoint.local)
			if err != nil {
				return result, err
			}
		}
		if !found {
			continue
		}
		addRate(result.ByClient, clientID, in, endpoint.out, endpoint.sampledAt)
		ownerKey, _, err := queryOptionalInt(ctx, s.db, "read_client_owner",
			map[string]any{"client_id": clientID})
		if err != nil {
			return result, err
		}
		addRate(result.ByOwner, ownerKey, in, endpoint.out, endpoint.sampledAt)
	}
	return result, nil
}

// addressLevelClient returns the most recent address-level client at an address.
func (s *Store) addressLevelClient(ctx context.Context, address string) (int64, bool, error) {
	return queryOptionalInt(ctx, s.db, "read_address_level_client", map[string]any{"address": address})
}

func addRate[K comparable](rates map[K]CurrentRate, key K, in float64, out *float64, sampledAt int64) {
	rate := rates[key]
	rate.add(in, out, sampledAt)
	rates[key] = rate
}

// ClientRateState says whether a client has a current rate.
type ClientRateState string

// The two states of a client's current rate.
const (
	// RateCurrent is a rate read within the bound.
	RateCurrent ClientRateState = "current"
	// RateNoCurrentSample is no reading within the bound: not a rate of 0.
	RateNoCurrentSample ClientRateState = "no_current_sample"
)

// ReadClientCurrentRate returns one client's current rate, or the no-current-sample
// state.
func (s *Store) ReadClientCurrentRate(ctx context.Context, clientID, now int64) (
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

// SampledBytesState says whether a device was sampled in a window.
type SampledBytesState string

// The two states of sampled bytes.
const (
	// Sampled means at least one sample was taken in the window.
	Sampled SampledBytesState = "sampled"
	// NotSampled means none was: a state, never 0 bytes.
	NotSampled SampledBytesState = "not_sampled"
)

// SampledBytes is the bytes seen in samples on one device.
type SampledBytes struct {
	Device string
	// Bytes is the sum of cumulative_bytes_in and cumulative_bytes_out over every
	// sample in the window: bytes seen in samples, never extrapolated.
	Bytes float64
	// Samples is how many sampling instants the window holds, and SampledSeconds
	// the seconds they cover, which is the coverage Bytes must be read against.
	Samples        int64
	SampledSeconds int64
}

// ReadSampledBytes returns the bytes seen in samples in [from, to), per device. A
// window with no sample on any device is NotSampled and holds no figure.
func (s *Store) ReadSampledBytes(ctx context.Context, from, to int64) ([]SampledBytes, SampledBytesState, error) {
	text, err := Statement("read_sampled_pair_bytes")
	if err != nil {
		return nil, "", err
	}
	rows, err := s.db.QueryContext(ctx, text, sql.Named("from", from), sql.Named("to", to))
	if err != nil {
		return nil, "", fmt.Errorf("store: read_sampled_pair_bytes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	byDevice := map[string]*SampledBytes{}
	for rows.Next() {
		var (
			device    string
			sampledAt int64
			bytes     float64
		)
		if err := rows.Scan(&device, &sampledAt, &bytes); err != nil {
			return nil, "", fmt.Errorf("store: read_sampled_pair_bytes: %w", err)
		}
		entry, present := byDevice[device]
		if !present {
			entry = &SampledBytes{Device: device}
			byDevice[device] = entry
		}
		entry.Bytes += bytes
		entry.Samples++
		entry.SampledSeconds += SampleSeconds
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("store: read_sampled_pair_bytes: %w", err)
	}
	if len(byDevice) == 0 {
		return nil, NotSampled, nil
	}
	devices := make([]string, 0, len(byDevice))
	for device := range byDevice {
		devices = append(devices, device)
	}
	sort.Strings(devices)
	result := make([]SampledBytes, 0, len(devices))
	for _, device := range devices {
		result = append(result, *byDevice[device])
	}
	return result, Sampled, nil
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
func (s *Store) ReadInterfaceThroughput(ctx context.Context, device string, measure Measure,
	from, to int64) ([]ThroughputPoint, error) {
	text, err := Statement("read_interface_counter")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, text, sql.Named("device", device),
		sql.Named("measure", string(measure)), sql.Named("from", from), sql.Named("to", to))
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

// IsFirewallAddress reports whether an address is one the firewall holds on any
// interface, from the addr4 and addr6 fields discovery reads.
func (s *Store) IsFirewallAddress(ctx context.Context, address string) (bool, error) {
	if _, err := netip.ParseAddr(address); err != nil {
		return false, nil
	}
	_, found, err := queryOptionalInt(ctx, s.db, "read_is_firewall_address",
		map[string]any{"address": address})
	return found, err
}
