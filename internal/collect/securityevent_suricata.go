package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// Suricata — one implementation of the security_event kind, and today the only one.
//
// Everything here is a fact about this engine and about the one endpoint that opens its
// event log. What the KIND does — keeping the watermark, detecting a lost rotation,
// resolving a client — is in securityevent.go.

func init() { registerSecurityEventSource(suricataAlerts{}) }

// suricataAlerts reads /api/ids/service/query_alerts and its companions.
type suricataAlerts struct{}

// providerKey is the registry row this implementation answers for.
func (suricataAlerts) providerKey() string { return ProviderSuricata }

// The page an alert read asks for is the operator's page size, passed in by the kind; the
// endpoint's own default of 9999 is its ceiling, which internal/sizing records.

// probe separates absent from installed-but-stopped from running.
//
// The survey names each: 404, 401 or 403 is absent or not permitted; a status of stopped,
// disabled or unknown is present but disabled; running is reachable. A reachable engine with
// a narrow ruleset and nothing to report is a NORMAL state — the verified section measured
// exactly that on the maintainer's own firewall — and distinguishing it from stopped and
// from "we could not ask" is the whole point of the availability table.
func (suricataAlerts) probe(ctx context.Context, host session) (probeResult, error) {
	result := probeResult{probe: opnsense.IDSStatus, state: store.StateUnavailable}

	response, err := host.call(ctx, opnsense.IDSStatus, opnsense.RequestOptions{})
	if err != nil && response.Outcome == "" {
		return result, err
	}
	if !response.OK() {
		result.detail = response.Detail
		return result, nil
	}

	var body decode.Object
	if err := json.Unmarshal(response.Body, &body); err != nil {
		result.detail = "the status body could not be read"
		return result, nil
	}
	status, _ := decode.String(body, "status")

	// The implementation is present either way, so it is the one opnview reads for this
	// kind. Whether it has anything to say is the availability state's business, not
	// activation's.
	result.separable = true
	if decode.LowerASCII(status) == "running" {
		result.state = store.StateReachable
		return result, nil
	}
	result.state = store.StatePresentButDisabled
	result.detail = "the service reported " + quoteForDetail(status)
	return result, nil
}

// rotation enumerates the rotated event files.
//
// `fileid` is how query_alerts names a file and `sequence` is how get_alert_logs enumerates
// those values, so the two are joined on that token — compared as a number, because the
// survey establishes the field and not its rendering.
func (suricataAlerts) rotation(ctx context.Context, host session) (
	rotationView, probeResult, error) {
	view := rotationView{present: map[string]struct{}{}}
	result := probeResult{probe: opnsense.AlertLogs, state: store.StateUnavailable}

	response, err := host.call(ctx, opnsense.AlertLogs, opnsense.RequestOptions{})
	if err != nil && response.Outcome == "" {
		return view, result, err
	}
	if !response.OK() {
		// The rotation view is unavailable, so a lost file cannot be told from a present one.
		// known stays false, and the kind declares nothing lost: concluding otherwise would
		// invent a permanent gap out of a failed call.
		result.detail = response.Detail + "; the rotated-file list did not answer, so a lost " +
			"rotation cannot be detected this pass"
		return view, result, nil
	}

	rows, err := decode.Rows(response.Body)
	if err != nil {
		result.detail = "the rotated-file list could not be read"
		return view, result, nil
	}
	for _, row := range rows {
		sequence, ok := decode.Int(row, "sequence")
		if !ok {
			continue
		}
		view.present[strconv.FormatInt(sequence, 10)] = struct{}{}
		if view.highestSequence == nil || sequence > *view.highestSequence {
			value := sequence
			view.highestSequence = &value
		}
	}
	view.known = true
	result.state = store.StateReachable
	result.separable = true
	return view, result, nil
}

// alerts reads the most recent events, newest first.
//
// The backend returns a record only if it carries a top-level `alert` key, and it has
// already overwritten that key with the signature text, so category, severity and rule
// metadata are gone before opnview sees them. That is why NormalisedSeverity is nil on
// every record below: severity is resolved through the per-provider rule-info cache and
// from nowhere else.
func (suricataAlerts) alerts(ctx context.Context, host session, pageSize int) (
	[]alertRecord, probeResult, error) {
	result := probeResult{probe: opnsense.QueryAlerts, state: store.StateUnavailable}

	response, err := host.call(ctx, opnsense.QueryAlerts, opnsense.RequestOptions{
		Form: opnsense.Pagination(1, pageSize),
	})
	if err != nil && response.Outcome == "" {
		return nil, result, err
	}
	if !response.OK() {
		result.detail = response.Detail
		return nil, result, fmt.Errorf("collect: the alert feed answered %s", response.Outcome)
	}

	rows, err := decode.Rows(response.Body)
	if err != nil {
		result.detail = "the alert feed body could not be read"
		return nil, result, fmt.Errorf("collect: reading the alert feed: %w", err)
	}

	result.state = store.StateReachable
	result.separable = true
	if len(rows) == 0 {
		result.detail = "reachable with nothing to report, which for a narrow ruleset is a true " +
			"reading and not a fault"
		return nil, result, nil
	}

	normaliser, err := host.normaliser(ctx)
	if err != nil {
		return nil, result, err
	}
	records := make([]alertRecord, 0, len(rows))
	for _, row := range rows {
		fileID, present := decode.String(row, "fileid")
		if !present {
			// Without the file the offset names nothing, so the record cannot be given the
			// stable key the de-duplication rests on.
			continue
		}
		filepos, present := decode.Int(row, "filepos")
		if !present {
			continue
		}
		stamp, present := decode.String(row, "timestamp")
		if !present {
			return nil, result, fmt.Errorf("collect: an alert record carried no timestamp")
		}
		occurredAt, err := normaliser.NormaliseISO(stamp)
		if err != nil {
			return nil, result, fmt.Errorf("collect: normalising an alert timestamp: %w", err)
		}
		ruleIdentity, present := decode.String(row, "alert_sid")
		if !present {
			return nil, result, fmt.Errorf("collect: an alert record carried no signature id")
		}
		signature, present := decode.String(row, "alert")
		if !present {
			// This key is the whole of what survives the backend's overwrite. Empty means the
			// record arrived without it, and the column is NOT NULL.
			signature = ""
		}

		records = append(records, alertRecord{
			FileID:             fileID,
			ByteOffset:         filepos,
			OccurredAt:         occurredAt,
			RuleIdentity:       ruleIdentity,
			Signature:          signature,
			EventAction:        normaliseEventAction(decode.RawString(row, "alert_action")),
			NormalisedSeverity: nil,
			SrcAddress:         decode.RawString(row, "src_ip"),
			DstAddress:         decode.RawString(row, "dest_ip"),
			SrcPort:            decode.Port(row, "src_port"),
			DstPort:            decode.Port(row, "dest_port"),
			Protocol:           decode.StringPointer(row, "proto"),
			InInterfaceDevice:  decode.StringPointer(row, "in_iface"),
		})
	}
	return records, result, nil
}

// normaliseEventAction maps the feed's `alert_action` onto the closed vocabulary.
//
// An action outside it is `unknown`: presenting an event as blocked when opnview cannot tell
// would claim the firewall stopped something it may have let through.
func normaliseEventAction(raw string) string {
	switch decode.LowerASCII(raw) {
	case "blocked", "drop", "dropped", "reject", "rejected":
		return "blocked"
	case "allowed", "alert", "pass", "passed":
		return "allowed"
	default:
		return "unknown"
	}
}
