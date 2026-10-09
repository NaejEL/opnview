package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The pf filter log — one implementation of the firewall_log kind, and today the only one.
//
// Everything in this file is a fact about pf and about the way OPNsense exposes its log.
// What the KIND does with the records — resolving the device and the rule, composing a
// client identity, detecting a gap — is in firewalllog.go and is written once.

func init() { registerFirewallLogSource(pfFilterLog{}) }

// pfFilterLog reads /api/diagnostics/firewall/log.
type pfFilterLog struct{}

// providerKey is the registry row this implementation answers for.
func (pfFilterLog) providerKey() string { return ProviderPf }

// firewallLogQuery builds the query string for one poll, asking for `limit` lines. The limit
// is the page size the operator configured, not a constant of this file: the survey's
// `limit` defaults to 1000 and is coerced back to 1000, and internal/sizing suggests a page
// from the rate this installation measures.
//
// `digest` is deliberately absent. The survey's inferred text treated it as the incremental
// primitive; measured twice on a live firewall, passing it returned byte-identical output
// and the supplied digest did not appear in the response. Sending it would suggest a resume
// guarantee that does not exist and would hide the gap detection that replaces it.
func firewallLogQuery(limit int) url.Values {
	return url.Values{"limit": []string{strconv.Itoa(limit)}}
}

// probe asks whether the log can be read.
//
// The detection rule is the survey's: an empty or NON-ARRAY body, or 401/403, means
// unavailable — backend, credentials or ACL — while an empty array with a healthy body means
// reachable but silent. The second is a warning state and not an absence of traffic, which
// is why the body's SHAPE is checked and not only its length.
func (pfFilterLog) probe(ctx context.Context, host session) (probeResult, error) {
	result := probeResult{probe: opnsense.FirewallLog}

	response, err := host.call(ctx, opnsense.FirewallLog, opnsense.RequestOptions{
		Query: firewallLogQuery(1),
	})
	if err != nil && response.Outcome == "" {
		return result, err
	}
	if !response.OK() {
		result.state = store.StateUnavailable
		result.detail = response.Detail
		return result, nil
	}

	var records []json.RawMessage
	if json.Unmarshal(response.Body, &records) != nil {
		result.state = store.StateUnavailable
		result.detail = "HTTP 200 with a body that is not an array, which the survey names as a " +
			"backend failure rather than an absence of traffic"
		return result, nil
	}

	result.state = store.StateReachable
	result.separable = true
	if len(records) == 0 {
		result.detail = "reachable but silent: the log returned no record, which is not an " +
			"absence of traffic"
	}
	return result, nil
}

// records reads one page, newest first.
func (pfFilterLog) records(ctx context.Context, host session, pageSize int) ([]logRecord, probeResult, error) {
	result := probeResult{probe: opnsense.FirewallLog, state: store.StateUnavailable}

	response, err := host.call(ctx, opnsense.FirewallLog, opnsense.RequestOptions{
		Query: firewallLogQuery(pageSize),
	})
	if err != nil && response.Outcome == "" {
		return nil, result, err
	}
	if !response.OK() {
		result.detail = response.Detail
		return nil, result, fmt.Errorf("collect: the filter log answered %s", response.Outcome)
	}

	// A JSON array, newest record first, not a paginated envelope.
	var rows []decode.Object
	if err := json.Unmarshal(response.Body, &rows); err != nil {
		result.detail = "HTTP 200 with a body that is not an array of records"
		return nil, result, fmt.Errorf("collect: reading the filter log: %w", err)
	}

	result.state = store.StateReachable
	result.separable = true
	if len(rows) == 0 {
		result.detail = "reachable but silent: the log returned no record, which is not an " +
			"absence of traffic"
		return nil, result, nil
	}

	normaliser, err := host.normaliser(ctx)
	if err != nil {
		return nil, result, err
	}
	records := make([]logRecord, 0, len(rows))
	for _, row := range rows {
		digest, present := decode.String(row, "__digest__")
		if !present {
			// Without the identity there is no way to avoid storing the record twice, and a
			// duplicated flow would inflate every volume that sums it.
			continue
		}
		stamp, present := decode.String(row, "__timestamp__")
		if !present {
			continue
		}
		// The two shapes, handled here because which shapes a timestamp arrives in is a
		// fact about this log and its syslog format rather than about the kind.
		observedAt, err := normaliser.NormaliseFilterLogTimestamp(stamp)
		if err != nil {
			return nil, result, fmt.Errorf("collect: normalising a filter-log timestamp: %w", err)
		}

		ipVersion, present := decode.Int(row, "ipversion")
		if !present || (ipVersion != 4 && ipVersion != 6) {
			return nil, result, fmt.Errorf(
				"collect: a filter-log record reported no usable ipversion")
		}
		packetBytes, present := decode.Int(row, "length")
		if !present || packetBytes < 0 {
			// The column is NOT NULL and non-negative, and a length the record did not give
			// must not become a zero that a volume sums.
			return nil, result, fmt.Errorf(
				"collect: a filter-log record reported no usable length")
		}
		protocol, present := decode.String(row, "protoname")
		if !present {
			// protonum is the numeric twin and is not stored; when the name is absent the
			// honest value is the empty string, which no query interprets.
			protocol = ""
		}

		records = append(records, logRecord{
			Digest:      digest,
			ObservedAt:  observedAt,
			Device:      decode.RawString(row, "interface"),
			SrcAddress:  decode.RawString(row, "src"),
			DstAddress:  decode.RawString(row, "dst"),
			SrcPort:     decode.Port(row, "srcport"),
			DstPort:     decode.Port(row, "dstport"),
			Protocol:    protocol,
			IPVersion:   ipVersion,
			Action:      normaliseFilterAction(decode.RawString(row, "action")),
			Direction:   normaliseFlowDirection(decode.RawString(row, "dir")),
			LogReason:   decode.StringPointer(row, "reason"),
			PacketBytes: packetBytes,
			Rid:         decode.StringPointer(row, "rid"),
			IPID:        pairingField(row, "id", ipVersion == 4, 65535),
			TCPSeq:      pairingField(row, "seq", true, 4294967295),
		})
	}
	return records, result, nil
}

// pairingField reads one of the two fields that pair the records of one connection:
// `id`, the IPv4 identification, and `seq`, the TCP sequence number. Both are written
// by filterlog at fixed positions -- "ipversion, tos, ecn, ttl, id, ..." and, on TCP,
// "srcport, dstport, datalen, flags, seq, ..." (opnsense/ports 26.7.3,
// opnsense/filterlog/files/description.txt) -- and read_log.py names them `id` and
// `seq` at the same positions (opnsense/core 26.7.3, src/opnsense/scripts/filter/
// read_log.py, fields_ipv4 and fields_ipv4_tcp). Its names drift only past `ack`: the
// window is filed under `urp` and the urgent pointer under `tcpopts`, neither of which
// is read. A field the record does not carry -- `id` on IPv6, `seq` off TCP -- or that
// is not a whole number within its width is stored as no value, never as a zero a
// pairing could match on.
func pairingField(row decode.Object, key string, applies bool, maximum int64) *int64 {
	if !applies {
		return nil
	}
	value, present := decode.Int(row, key)
	if !present || value < 0 || value > maximum {
		return nil
	}
	return &value
}

// normaliseFilterAction maps pf's action onto the closed vocabulary the schema constrains.
//
// An action outside it is `unknown`, never silently folded into `pass` or `block`: the
// survey says the field can also carry a translation action, and rendering one of those as
// a permission or a denial would attribute a decision nothing made.
func normaliseFilterAction(raw string) string {
	switch decode.LowerASCII(raw) {
	case "pass":
		return "pass"
	case "block":
		return "block"
	case "reject":
		return "reject"
	default:
		return "unknown"
	}
}

// normaliseFlowDirection maps pf's `dir` onto the closed vocabulary.
//
// Anything else is `unknown`: on a blocked record the direction is what tells a reader
// which end is the machine inside, so guessing it would be worse than admitting it.
func normaliseFlowDirection(raw string) string {
	switch decode.LowerASCII(raw) {
	case "in":
		return "in"
	case "out":
		return "out"
	default:
		return "unknown"
	}
}
