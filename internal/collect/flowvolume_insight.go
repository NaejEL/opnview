package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The firewall's own volume and telemetry — one implementation of the flow_volume kind, and
// today the only one.
//
// IT SAMPLES BECAUSE NOTHING UPSTREAM KEEPS PER-PAIR HISTORY.
// /api/diagnostics/netflow/top, netflow/get_metadata and netflow/aggregate all answered 404
// on a live 26.7.3_11; the per-pair data is at /api/diagnostics/traffic/top/<interface names>
// and it is a LIVE RATE SNAPSHOT. Measured cumulative values were hundreds of bytes. So no
// endpoint answers "what did these two talk about last Tuesday", and opnview builds that
// history by sampling. That is architectural rather than a collector detail.
//
// IT ALSO READS THE FIREWALL'S OWN GAUGES, because a gauge and a sampled pair volume are the
// same five facts — a subject, a measure, a unit, a value and an instant — and one table
// carries both.
//
// AND IT REFUSES THE WRONG ARGUMENT. traffic/top takes interface NAMES, the configuration
// keys. A DEVICE name returns an empty array with HTTP 200, which is indistinguishable from
// an absence of traffic, so a wrong argument here would not fail: it would quietly record
// silence, and every screen downstream would show a working, quiet network.

func init() { registerMeasurementSource(insightVolume{}) }

// insightVolume reads the volume snapshot and the system telemetry.
type insightVolume struct{}

// providerKey is the registry row this implementation answers for.
func (insightVolume) providerKey() string { return ProviderInsight }

// ErrDeviceNameArgument is returned when the per-pair sampler is about to send a network
// device name to an endpoint that takes interface names.
//
// It is the one guard in this package that refuses to make a call at all, and the reason is
// that the failure it prevents is invisible: the endpoint answers HTTP 200 with an empty
// array, which every other part of opnview would read as a source that is working and quiet.
var ErrDeviceNameArgument = errors.New(
	"collect: refusing to send a network device name to an endpoint that takes interface names")

// The candidate field names of the telemetry endpoints.
//
// UNVERIFIED: every list below. The survey establishes that these endpoints answer on a live
// firewall and does NOT establish their field names — it says so in as many words. A single
// name asserted here would be a claim about a response shape nobody has read, so each reading
// tries the plausible keys and reports the reading as ABSENT when none of them answers.
// Confirming or correcting these lists against a real firewall is on the list for the end of
// cycle 4B, which is the first cycle that can talk to one.
var (
	// unverifiedMemoryTotalKeys and unverifiedMemoryUsedKeys build the memory ratio.
	unverifiedMemoryTotalKeys = []string{"total", "memory_total", "total_bytes", "physmem"}
	// unverifiedMemoryUsedKeys is the used half of the same ratio.
	unverifiedMemoryUsedKeys = []string{"used", "memory_used", "used_bytes", "active"}
	// unverifiedMemoryRatioKeys is a percentage the endpoint may report directly.
	unverifiedMemoryRatioKeys = []string{"used_percent", "usage", "memory_usage_percent"}
	// unverifiedCPUKeys is a processor figure, reported as a percentage.
	unverifiedCPUKeys = []string{"cpu", "cpu_usage", "used", "total"}
	// unverifiedTemperatureKeys is one sensor's reading.
	unverifiedTemperatureKeys = []string{"temperature", "temp", "value"}
	// unverifiedSensorNameKeys labels the sensor a reading came from.
	unverifiedSensorNameKeys = []string{"device", "device_seq", "type", "name"}
	// unverifiedUptimeKeys is how long the machine has been up, in seconds.
	unverifiedUptimeKeys = []string{"uptime", "uptime_seconds", "seconds"}
	// unverifiedLoadKeys is a load average.
	unverifiedLoadKeys = []string{"loadavg", "load_average", "load"}
	// unverifiedDiskRatioKeys is a filesystem's used fraction, as a percentage.
	unverifiedDiskRatioKeys = []string{"used_pct", "capacity", "used_percent", "percent"}
	// unverifiedMountKeys labels the filesystem a reading came from.
	unverifiedMountKeys = []string{"mountpoint", "mount", "filesystem", "device"}
	// unverifiedDiskWrapperKeys are the keys the disk response may hold its list under.
	unverifiedDiskWrapperKeys = []string{"devices", "rows", "filesystems"}
	// unverifiedBytesInKeys and the three below are the per-interface counters.
	unverifiedBytesInKeys = []string{"bytes received", "bytes_received", "bytes_in", "ibytes", "in_bytes"}
	// unverifiedBytesOutKeys is the outbound byte counter.
	unverifiedBytesOutKeys = []string{"bytes transmitted", "bytes_transmitted", "bytes_out", "obytes", "out_bytes"}
	// unverifiedPacketsInKeys is the inbound packet counter.
	unverifiedPacketsInKeys = []string{"packets received", "packets_received", "packets_in", "ipackets", "in_packets"}
	// unverifiedPacketsOutKeys is the outbound packet counter.
	unverifiedPacketsOutKeys = []string{"packets transmitted", "packets_transmitted", "packets_out", "opackets", "out_packets"}
	// unverifiedPeerAddressKeys names the peer in a traffic/top details entry.
	unverifiedPeerAddressKeys = []string{"address", "ip", "addr", "peer"}
)

// probe reads whether the firewall keeps volume data itself.
//
// is_enabled returns {netflow, local}, and the survey's decisive finding is that local data
// exists only when `local` is 1: a firewall exporting to an external collector reports
// netflow 1, local 0 and keeps nothing here. That is a configuration state, recorded as
// present-but-disabled, never as a flat zero line.
func (insightVolume) probe(ctx context.Context, host session) (probeResult, error) {
	result := probeResult{probe: opnsense.NetflowIsEnabled, state: store.StateUnavailable}

	response, err := host.call(ctx, opnsense.NetflowIsEnabled, opnsense.RequestOptions{})
	if err != nil && response.Outcome == "" {
		return result, err
	}
	if !response.OK() {
		result.detail = response.Detail
		return result, nil
	}

	var body decode.Object
	if err := json.Unmarshal(response.Body, &body); err != nil {
		result.detail = "the is_enabled body could not be read"
		return result, nil
	}
	local, hasLocal := decode.Int(body, "local")

	result.separable = true
	switch {
	case !hasLocal:
		result.state = store.StatePresentButDisabled
		result.detail = "is_enabled did not report whether local collection is on"
	case local == 0:
		result.state = store.StatePresentButDisabled
		result.detail = "local collection is off, so the firewall exports elsewhere and keeps " +
			"nothing here"
	default:
		result.state = store.StateReachable
	}
	return result, nil
}

// sample reads the per-pair snapshot and the firewall's gauges.
func (insightVolume) sample(ctx context.Context, host session, snapshot Discovery,
	providerID, now int64) ([]store.MeasurementSample, []string, probeResult, error) {
	result := probeResult{probe: opnsense.TrafficTop, state: store.StateReachable}

	readings, pairDetail, pairErr := samplePairVolume(ctx, host, snapshot, providerID, now)
	result.detail = pairDetail
	if pairErr != nil {
		// The volume half could not be read, so the volume source reads as unavailable. The
		// gauges are read anyway: they come from different endpoints and a refusal over the
		// per-pair argument says nothing about the firewall's temperature. Losing them here
		// would turn one unreadable source into two.
		result.state = store.StateUnavailable
		readings = nil
	}

	gauges, missing, err := sampleFirewallTelemetry(ctx, host, snapshot, now)
	if err != nil {
		return nil, nil, result, err
	}
	return append(readings, gauges...), missing, result, nil
}

// guardInterfaceNames turns the discovered interface identifiers into the argument traffic/top
// takes, refusing if any of them is a network device name.
//
// The check is not a tautology over types. It compares the names about to be sent against the
// DEVICE set discovery read from the firewall, so if anything upstream ever put a device where
// an identifier belongs — a refactor, a decoder reading the wrong key — the call is refused
// instead of returning an empty array that looks like an absence of traffic.
func guardInterfaceNames(snapshot Discovery) ([]opnsense.InterfaceName, error) {
	names := make([]opnsense.InterfaceName, 0, len(snapshot.Identifiers))
	for _, name := range snapshot.Identifiers {
		asString := string(name)
		if _, isDevice := snapshot.Devices[asString]; isDevice {
			if _, isIdentifier := snapshot.InterfaceIDByIdentifier[asString]; !isIdentifier {
				return nil, fmt.Errorf("%w: %q is a device name", ErrDeviceNameArgument, asString)
			}
		}
		names = append(names, name)
	}
	// Sorted so the request is byte-identical between passes, which makes a recorded request
	// comparable in a test and a log line comparable by eye.
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	return names, nil
}

// samplePairVolume reads /api/diagnostics/traffic/top and returns the pair readings.
func samplePairVolume(ctx context.Context, host session, snapshot Discovery,
	providerID, now int64) ([]store.MeasurementSample, string, error) {
	names, err := guardInterfaceNames(snapshot)
	if err != nil {
		return nil, err.Error(), err
	}
	if len(names) == 0 {
		// Discovery has not run, or the firewall reported no interface. Calling with no
		// argument would reach a different command on the router.
		return nil, "no interface has been discovered yet, so nothing was sampled", nil
	}

	argument := make([]string, 0, len(names))
	for _, name := range names {
		argument = append(argument, string(name))
	}

	response, err := host.call(ctx, opnsense.TrafficTop, opnsense.RequestOptions{
		// One comma-separated path element, which is how the OPNsense router passes a
		// positional list. UNVERIFIED: whether this endpoint wants one element or several; the
		// survey gives the path and the argument KIND and not the separator. It matters,
		// because the wrong shape returns an empty array with HTTP 200 — so if a live firewall
		// returns nothing here, this line is the first thing to check, not the network.
		Arguments: []string{strings.Join(argument, ",")},
	})
	if err != nil && response.Outcome == "" {
		return nil, "", err
	}
	if !response.OK() {
		return nil, response.Detail, fmt.Errorf("collect: the traffic snapshot answered %s",
			response.Outcome)
	}

	var body any
	if err := json.Unmarshal(response.Body, &body); err != nil {
		return nil, "the traffic snapshot body could not be read",
			fmt.Errorf("collect: reading the traffic snapshot: %w", err)
	}

	var readings []store.MeasurementSample
	pairs := 0
	for _, entry := range perInterface(body) {
		for _, record := range recordsOf(entry) {
			localAddress, present := firstString(record, unverifiedPeerAddressKeys...)
			if !present {
				continue
			}
			for _, detail := range decode.Objects(record["details"]) {
				peerAddress, present := firstString(detail, unverifiedPeerAddressKeys...)
				if !present || peerAddress == localAddress {
					continue
				}
				subjectKey := canonicalPairKey(localAddress, peerAddress)
				wrote := false
				for _, reading := range []struct {
					key     string
					measure store.Measure
					unit    store.Unit
				}{
					{"cumulative_bytes_in", store.MeasureCumulativeBytesIn, store.UnitByte},
					{"cumulative_bytes_out", store.MeasureCumulativeBytesOut, store.UnitByte},
					{"rate_bits_in", store.MeasureRateBitsIn, store.UnitBitPerSecond},
					{"rate_bits_out", store.MeasureRateBitsOut, store.UnitBitPerSecond},
				} {
					value, present := decode.Float(detail, reading.key)
					if !present {
						continue
					}
					readings = append(readings, store.MeasurementSample{
						ProviderID:  &providerID,
						SubjectKind: store.SubjectEndpointPair,
						SubjectKey:  subjectKey,
						Measure:     reading.measure,
						Unit:        reading.unit,
						Value:       value,
						SampledAt:   now,
					})
					wrote = true
				}
				if wrote {
					pairs++
				}
			}
		}
	}

	if pairs == 0 {
		return readings, "the traffic snapshot named no endpoint pair, which is not an absence " +
			"of traffic; a wrong interface argument answers exactly this way", nil
	}
	return readings, fmt.Sprintf("sampled %d endpoint pairs from a live snapshot; no endpoint "+
		"exposes a per-pair aggregate over a past window, so this history is opnview's own",
		pairs), nil
}

// canonicalPairKey renders two addresses in lexicographic order, joined by a space. It is the
// same canonical ordering pair_volume_observation enforces with endpoint_low and
// endpoint_high, so a pair sampled from either end collapses to one subject instead of two.
func canonicalPairKey(first, second string) string {
	if first > second {
		first, second = second, first
	}
	return first + " " + second
}

// sampleFirewallTelemetry reads the six telemetry endpoints and returns the readings, plus the
// readings that did NOT answer.
//
// Nothing here writes a zero for a reading it could not take. That is the whole difference
// between "the processor is idle" and "this endpoint did not tell us", and the second is what
// the returned list records.
//
// The readings carry NO provider. The firewall's own telemetry is not an implementation of any
// external contract — it is the machine reporting on itself — and attributing it to
// the volume provider would say the volume source measured the temperature. That the model has
// no kind for it is a gap worth recording rather than papering over with a foreign key that
// means something else.
func sampleFirewallTelemetry(ctx context.Context, host session, snapshot Discovery, now int64) (
	[]store.MeasurementSample, []string, error) {
	var (
		readings []store.MeasurementSample
		missing  []string
	)
	add := func(subjectKey string, measure store.Measure, unit store.Unit, value float64) {
		readings = append(readings, store.MeasurementSample{
			ProviderID:  nil,
			SubjectKind: store.SubjectFirewall,
			SubjectKey:  subjectKey,
			Measure:     measure,
			Unit:        unit,
			Value:       value,
			SampledAt:   now,
		})
	}

	// Memory: either a ratio the endpoint computed, or a total and a used figure to divide.
	if body, ok, err := host.readObject(ctx, opnsense.SystemResources); err != nil {
		return nil, nil, err
	} else if !ok {
		missing = append(missing, "memory (the endpoint did not answer)")
	} else if ratio, present := memoryRatio(body); !present {
		missing = append(missing, "memory (the endpoint answered with no figure this code could read)")
	} else {
		add("", store.MeasureMemoryUseRatio, store.UnitRatio, ratio)
	}

	// The processor.
	if body, ok, err := host.readObject(ctx, opnsense.Activity); err != nil {
		return nil, nil, err
	} else if !ok {
		missing = append(missing, "processor (the endpoint did not answer)")
	} else if value, _, present := decode.FirstFloat(body, unverifiedCPUKeys...); !present {
		missing = append(missing, "processor (the endpoint answered with no figure this code could read)")
	} else {
		add("", store.MeasureCPUUseRatio, store.UnitRatio, percentToRatio(value))
	}

	// Temperature, one reading per sensor. A firewall with no sensor answers with nothing, and
	// that is a fact about the hardware rather than a temperature of zero.
	if sensors, ok, err := host.readCollection(ctx, opnsense.SystemTemperature); err != nil {
		return nil, nil, err
	} else if !ok {
		missing = append(missing, "temperature (the endpoint did not answer)")
	} else {
		written := 0
		for index, sensor := range sensors {
			value, _, present := decode.FirstFloat(sensor, unverifiedTemperatureKeys...)
			if !present {
				continue
			}
			label, hasLabel := firstString(sensor, unverifiedSensorNameKeys...)
			if !hasLabel {
				label = fmt.Sprintf("sensor-%d", index)
			}
			add(label, store.MeasureTemperatureCelsius, store.UnitCelsius, value)
			written++
		}
		if written == 0 {
			missing = append(missing, "temperature (this firewall reports no sensor)")
		}
	}

	// Uptime and load.
	if body, ok, err := host.readObject(ctx, opnsense.SystemTime); err != nil {
		return nil, nil, err
	} else if !ok {
		missing = append(missing, "uptime (the endpoint did not answer)")
	} else {
		// The live answer is text, "17 days, 23:24:10" and "0.07, 0.14, 0.15", as the
		// survey's systemTime table records; a number under one of the keys is read too.
		if value, _, present := decode.FirstFloat(body, unverifiedUptimeKeys...); present {
			add("", store.MeasureUptimeSeconds, store.UnitSecond, value)
		} else if text, present := decode.String(body, "uptime"); present && uptimeReadable(text) {
			seconds, _ := decode.UptimeSeconds(text)
			add("", store.MeasureUptimeSeconds, store.UnitSecond, float64(seconds))
		} else {
			missing = append(missing, "uptime (the endpoint answered with no figure this code could read)")
		}
		if value, _, present := decode.FirstFloat(body, unverifiedLoadKeys...); present {
			add("", store.MeasureLoadAverage, store.UnitDimensionless, value)
		} else if text, present := decode.String(body, "loadavg"); present {
			if value, err := decode.FirstLoadAverage(text); err == nil {
				add("", store.MeasureLoadAverage, store.UnitDimensionless, value)
			} else {
				missing = append(missing, "load average (the endpoint answered with no figure this code could read)")
			}
		} else {
			missing = append(missing, "load average (the endpoint answered with no figure this code could read)")
		}
	}

	// Disk, one reading per filesystem.
	if filesystems, ok, err := host.readCollection(ctx, opnsense.SystemDisk,
		unverifiedDiskWrapperKeys...); err != nil {
		return nil, nil, err
	} else if !ok {
		missing = append(missing, "disk (the endpoint did not answer)")
	} else {
		written := 0
		for index, filesystem := range filesystems {
			value, _, present := decode.FirstFloat(filesystem, unverifiedDiskRatioKeys...)
			if !present {
				continue
			}
			label, hasLabel := firstString(filesystem, unverifiedMountKeys...)
			if !hasLabel {
				label = fmt.Sprintf("filesystem-%d", index)
			}
			add(label, store.MeasureDiskUseRatio, store.UnitRatio, percentToRatio(value))
			written++
		}
		if written == 0 {
			missing = append(missing, "disk (the endpoint answered with no figure this code could read)")
		}
	}

	// The per-interface counters. Their subject is the network DEVICE name, the token
	// interface_map keys by, so a reading joins to an interface without a foreign key a
	// discovery refresh could break.
	if body, ok, err := host.readObject(ctx, opnsense.TrafficInterface); err != nil {
		return nil, nil, err
	} else if !ok {
		missing = append(missing, "interface counters (the endpoint did not answer)")
	} else {
		written := 0
		for identifier, entry := range perInterface(body) {
			counters, isObject := entry.(decode.Object)
			if !isObject {
				continue
			}
			device := deviceOf(snapshot, identifier)
			if device == "" {
				// The reading names an interface discovery does not know. Storing it under a key
				// nothing can join to would be a row no screen could read.
				continue
			}
			for _, reading := range []struct {
				keys    []string
				measure store.Measure
				unit    store.Unit
			}{
				{unverifiedBytesInKeys, store.MeasureBytesIn, store.UnitByte},
				{unverifiedBytesOutKeys, store.MeasureBytesOut, store.UnitByte},
				{unverifiedPacketsInKeys, store.MeasurePacketsIn, store.UnitPacket},
				{unverifiedPacketsOutKeys, store.MeasurePacketsOut, store.UnitPacket},
			} {
				value, _, present := decode.FirstFloat(counters, reading.keys...)
				if !present {
					continue
				}
				readings = append(readings, store.MeasurementSample{
					SubjectKind: store.SubjectInterface,
					SubjectKey:  device,
					Measure:     reading.measure,
					Unit:        reading.unit,
					Value:       value,
					SampledAt:   now,
				})
				written++
			}
		}
		if written == 0 {
			missing = append(missing, "interface counters (the endpoint answered with no figure this code could read)")
		}
	}

	return readings, missing, nil
}

// memoryRatio computes the memory ratio from whichever shape the endpoint used.
func memoryRatio(body decode.Object) (float64, bool) {
	container := body
	if nested, present := decode.Nested(body, "memory"); present {
		container = nested
	}
	if value, _, present := decode.FirstFloat(container, unverifiedMemoryRatioKeys...); present {
		return percentToRatio(value), true
	}
	total, _, hasTotal := decode.FirstFloat(container, unverifiedMemoryTotalKeys...)
	used, _, hasUsed := decode.FirstFloat(container, unverifiedMemoryUsedKeys...)
	if hasTotal && hasUsed && total > 0 {
		return used / total, true
	}
	return 0, false
}

// percentToRatio turns a figure that may be a percentage into the 0-to-1 ratio the unit
// promises. A value above 1 is read as a percentage, which is the only interpretation that
// makes both shapes storable under one unit; a value at or below 1 is already a ratio.
func percentToRatio(value float64) float64 {
	if value > 1 {
		return value / 100
	}
	return value
}

// perInterface returns a decoded body as a map from interface identifier to its entry. Both
// shapes OPNsense uses are handled: a map keyed by the identifier, and an envelope holding one.
func perInterface(body any) map[string]any {
	object, isObject := body.(decode.Object)
	if !isObject {
		return nil
	}
	for _, wrapper := range []string{"interfaces", "records", "rows"} {
		if nested, present := object[wrapper]; present {
			if asObject, ok := nested.(decode.Object); ok {
				return asObject
			}
		}
	}
	return object
}

// recordsOf returns the records of one interface's entry.
func recordsOf(entry any) []decode.Object {
	object, isObject := entry.(decode.Object)
	if !isObject {
		return decode.Objects(entry)
	}
	if records, present := object["records"]; present {
		return decode.Objects(records)
	}
	return decode.Objects(entry)
}

// deviceOf returns the network device an interface identifier runs on, or the empty string when
// discovery does not know the identifier.
func deviceOf(snapshot Discovery, identifier string) string {
	id, known := snapshot.InterfaceIDByIdentifier[identifier]
	if !known {
		return ""
	}
	for device, deviceID := range snapshot.InterfaceIDByDevice {
		if deviceID == id {
			return device
		}
	}
	return ""
}

// firstString returns the first of several candidate keys that holds a non-empty string. Like
// decode.FirstFloat, it exists only for the endpoints whose field names the survey does not establish.
func firstString(object decode.Object, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, present := decode.String(object, key); present {
			return value, true
		}
	}
	return "", false
}

// uptimeReadable says whether an uptime text is one decode reads.
func uptimeReadable(text string) bool {
	_, err := decode.UptimeSeconds(text)
	return err == nil
}
