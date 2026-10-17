package plugin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NaejEL/opnview/internal/collect/dhcplease"
	"github.com/NaejEL/opnview/internal/collect/dnslookup"
	"github.com/NaejEL/opnview/internal/collect/firewalllog"
	"github.com/NaejEL/opnview/internal/collect/flowrecord"
	"github.com/NaejEL/opnview/internal/collect/ipgeolocation"
	"github.com/NaejEL/opnview/internal/collect/measurement"
	"github.com/NaejEL/opnview/internal/collect/pluginkind"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/collect/reconciledstate"
	"github.com/NaejEL/opnview/internal/collect/recorddump"
	"github.com/NaejEL/opnview/internal/collect/registrabledomain"
	"github.com/NaejEL/opnview/internal/collect/resolvercache"
	"github.com/NaejEL/opnview/internal/collect/resolverlocaldata"
	"github.com/NaejEL/opnview/internal/collect/securityevent"
	"github.com/NaejEL/opnview/internal/collect/sources"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/plugin/scripthost"
	"github.com/NaejEL/opnview/internal/store/plugindecl"
	"github.com/NaejEL/opnview/internal/store/plugintables"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The kind adapters: each turns a plugin into the Source interface of the kind it implements, so
// the kind's coordinator in internal/collect runs it exactly as it ran the compiled
// implementations it replaces. An adapter calls the plugin -- its script, or its declarative
// form -- checks the answer against the kind's contract, and converts it. Nothing else.

// invoker is a plugin's way of answering a function of a kind: a script, or a declarative form.
type invoker interface {
	invoke(ctx context.Context, session provider.Session, function string, arguments ...any) (
		answer any, hostErrors []error, failed *scripthost.Failure)
}

// scriptInvoker calls a script's exported functions under one kind's namespace.
type scriptInvoker struct {
	script    *scripthost.Script
	namespace string
}

func (i scriptInvoker) invoke(ctx context.Context, session provider.Session, function string,
	arguments ...any) (any, []error, *scripthost.Failure) {
	return i.script.Call(ctx, session, i.namespace, function, arguments...)
}

// adapter is what every kind adapter shares.
type adapter struct {
	plugin    string
	contract  provider.Contract
	endpoints map[string]opnsense.Endpoint
	invoker   invoker
	// scriptProbe names the plugin in an availability row when no endpoint decided the
	// outcome: a script that threw or ran too long.
	scriptProbe opnsense.Endpoint
}

// run calls one function and checks its answer against the answer table. It returns the checked
// answer, the result, and the error the kind should see: the failure of the call, the answer's
// own error, or provider.ErrUnsupportedRead with the answer's reason.
func (a adapter) run(ctx context.Context, session provider.Session, function, table string,
	arguments ...any) (map[string]any, provider.Result, error) {
	answer, _, failed := a.invoker.invoke(ctx, session, function, arguments...)
	if failed != nil {
		return nil, a.failed(failed), failed
	}
	object, isObject := answer.(map[string]any)
	if !isObject {
		invalid := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: fmt.Sprintf("%s returned no object", function)}
		return nil, a.failed(invalid), invalid
	}
	answerErr, _ := object["error"].(error)
	delete(object, "error")
	checked, err := scripthost.CheckRecord(a.contract, table, object, function)
	if err != nil {
		invalid := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: err.Error()}
		return nil, a.failed(invalid), invalid
	}
	result, err := scripthost.ResultOf(checked["result"].(map[string]any), a.endpoints)
	if err != nil {
		invalid := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: err.Error()}
		return nil, a.failed(invalid), invalid
	}
	if answerErr != nil {
		return checked, result, answerErr
	}
	if unsupported := scripthost.Text(checked, "unsupported"); unsupported != "" {
		return checked, result, fmt.Errorf("%w: %s", provider.ErrUnsupportedRead, unsupported)
	}
	return checked, result, nil
}

// failed is the result a failed call is recorded with.
func (a adapter) failed(failed *scripthost.Failure) provider.Result {
	return provider.Result{State: vocab.StateUnavailable, Outcome: failed.Code, Probe: a.scriptProbe,
		Detail: fmt.Sprintf("the plugin %s: %s", a.plugin, failed.Detail)}
}

// probe is the probe every kind shares.
func (a adapter) probe(ctx context.Context, session provider.Session) (provider.Result, error) {
	_, result, err := a.run(ctx, session, "probe", "probe_answer")
	return result, err
}

// firewallLogAdapter is a plugin as a firewall_log source.
type firewallLogAdapter struct{ adapter }

func (a firewallLogAdapter) Probe(ctx context.Context, session provider.Session) (provider.Result, error) {
	return a.probe(ctx, session)
}

func (a firewallLogAdapter) Records(ctx context.Context, session provider.Session, pageSize int) (
	[]firewalllog.Record, provider.Result, error) {
	checked, result, err := a.run(ctx, session, "records", "records_answer", pageSize)
	if err != nil {
		return nil, result, err
	}
	var converted []firewalllog.Record
	for _, record := range scripthost.Records(checked, "records") {
		converted = append(converted, firewallLogRecord(record))
	}
	if invalid := firewallLogRecordsInvalid(converted); invalid != "" {
		return a.invalid(invalid)
	}
	return converted, result, nil
}

// firewallLogRecord converts one checked firewall_log record, pulled or pushed.
func firewallLogRecord(record map[string]any) firewalllog.Record {
	return firewalllog.Record{
		Digest: scripthost.Text(record, "digest"), ObservedAt: scripthost.Integer(record, "observed_at"),
		Device: scripthost.Text(record, "device"), SrcAddress: scripthost.Text(record, "src_address"),
		DstAddress: scripthost.Text(record, "dst_address"), SrcPort: scripthost.IntegerPointer(record, "src_port"),
		DstPort: scripthost.IntegerPointer(record, "dst_port"), Protocol: scripthost.Text(record, "protocol"),
		IPVersion: scripthost.Integer(record, "ip_version"), Action: scripthost.Text(record, "action"),
		Direction: scripthost.Text(record, "direction"), LogReason: scripthost.TextPointer(record, "log_reason"),
		PacketBytes: scripthost.Integer(record, "packet_bytes"), Rid: scripthost.TextPointer(record, "rid"),
		IPID: scripthost.IntegerPointer(record, "ip_id"), TCPSeq: scripthost.IntegerPointer(record, "tcp_seq"),
	}
}

// firewallLogRecordsInvalid checks what the contract's types cannot: the ranges.
func firewallLogRecordsInvalid(converted []firewalllog.Record) string {
	for index, record := range converted {
		switch {
		case record.IPVersion != 4 && record.IPVersion != 6:
			return fmt.Sprintf("records[%d].ip_version is %d, not 4 or 6", index, record.IPVersion)
		case record.PacketBytes < 0:
			return fmt.Sprintf("records[%d].packet_bytes is negative", index)
		case !portValid(record.SrcPort) || !portValid(record.DstPort):
			return fmt.Sprintf("records[%d] carries a port outside 0 to 65535", index)
		}
	}
	return ""
}

func portValid(port *int64) bool { return port == nil || (*port >= 0 && *port <= 65535) }

// invalid answers a read whose records the contract's ranges refuse.
func (a firewallLogAdapter) invalid(detail string) ([]firewalllog.Record, provider.Result, error) {
	failed := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: detail}
	return nil, a.failed(failed), failed
}

// securityEventAdapter is a plugin as a security_event source.
type securityEventAdapter struct{ adapter }

func (a securityEventAdapter) Probe(ctx context.Context, session provider.Session) (provider.Result, error) {
	return a.probe(ctx, session)
}

func (a securityEventAdapter) Rotation(ctx context.Context, session provider.Session) (
	securityevent.Rotation, provider.Result, error) {
	view := securityevent.Rotation{Present: map[string]struct{}{}}
	checked, result, err := a.run(ctx, session, "rotation", "rotation_answer")
	if err != nil {
		return view, result, err
	}
	view.Known = scripthost.Flag(checked, "known", false)
	for _, file := range scripthost.Texts(checked, "present") {
		view.Present[file] = struct{}{}
	}
	view.HighestSequence = scripthost.IntegerPointer(checked, "highest_sequence")
	return view, result, nil
}

func (a securityEventAdapter) Alerts(ctx context.Context, session provider.Session, pageSize int) (
	[]securityevent.Alert, provider.Result, error) {
	checked, result, err := a.run(ctx, session, "alerts", "alerts_answer", pageSize)
	if err != nil {
		return nil, result, err
	}
	var converted []securityevent.Alert
	for index, record := range scripthost.Records(checked, "alerts") {
		alert := securityevent.Alert{
			FileID: scripthost.Text(record, "file_id"), ByteOffset: scripthost.Integer(record, "byte_offset"),
			OccurredAt: scripthost.Integer(record, "occurred_at"), RuleIdentity: scripthost.Text(record, "rule_identity"),
			Signature: scripthost.Text(record, "signature"), EventAction: scripthost.Text(record, "event_action"),
			NormalisedSeverity: scripthost.TextPointer(record, "normalised_severity"),
			SrcAddress:         scripthost.Text(record, "src_address"), DstAddress: scripthost.Text(record, "dst_address"),
			SrcPort: scripthost.IntegerPointer(record, "src_port"), DstPort: scripthost.IntegerPointer(record, "dst_port"),
			Protocol: scripthost.TextPointer(record, "protocol"), InInterfaceDevice: scripthost.TextPointer(record, "in_interface_device"),
		}
		if !portValid(alert.SrcPort) || !portValid(alert.DstPort) {
			failed := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: fmt.Sprintf("alerts[%d] carries a port outside 0 to "+
				"65535", index)}
			return nil, a.failed(failed), failed
		}
		converted = append(converted, alert)
	}
	return converted, result, nil
}

// dhcpLeaseAdapter is a plugin as a dhcp_lease source.
type dhcpLeaseAdapter struct{ adapter }

func (a dhcpLeaseAdapter) Probe(ctx context.Context, session provider.Session) (provider.Result, error) {
	return a.probe(ctx, session)
}

func (a dhcpLeaseAdapter) Leases(ctx context.Context, session provider.Session, pageSize int) (
	[]dhcplease.Observation, provider.Result, error) {
	checked, result, err := a.run(ctx, session, "leases", "leases_answer", pageSize)
	if err != nil {
		return nil, result, err
	}
	leases := scripthost.Records(checked, "leases")
	if leases == nil {
		return nil, result, nil
	}
	converted := make([]dhcplease.Observation, 0, len(leases))
	for _, record := range leases {
		converted = append(converted, dhcplease.Observation{
			Address: scripthost.Text(record, "address"), MAC: dhcplease.MACPointer(scripthost.Text(record, "mac")),
			Hostname: scripthost.TextPointer(record, "hostname"), DHCPClientID: scripthost.TextPointer(record, "dhcp_client_id"),
			DUID: scripthost.TextPointer(record, "duid"), IAID: scripthost.TextPointer(record, "iaid"),
			VendorHint: scripthost.TextPointer(record, "vendor_hint"), LeaseState: scripthost.Text(record, "lease_state"),
			InterfaceIdentifier: scripthost.Text(record, "interface_identifier"),
			InterfaceDevice:     scripthost.Text(record, "interface_device"),
			StartsAt:            scripthost.IntegerPointer(record, "starts_at"), ExpiresAt: scripthost.IntegerPointer(record, "expires_at"),
		})
	}
	return converted, result, nil
}

// dnsLookupAdapter is a plugin as a dns_lookup source.
type dnsLookupAdapter struct {
	adapter
	pageBound int
	span      int64
}

func (a dnsLookupAdapter) Probe(ctx context.Context, session provider.Session) (provider.Result, error) {
	return a.probe(ctx, session)
}

func (a dnsLookupAdapter) PageBound() int { return a.pageBound }

func (a dnsLookupAdapter) RequestedSpanSeconds() int64 { return a.span }

func (a dnsLookupAdapter) Lookups(ctx context.Context, session provider.Session, page int) (
	[]dnslookup.Record, provider.Result, error) {
	checked, result, err := a.run(ctx, session, "lookups", "lookups_answer", page)
	if err != nil {
		return nil, result, err
	}
	lookups := scripthost.Records(checked, "lookups")
	converted := make([]dnslookup.Record, 0, len(lookups))
	for index, record := range lookups {
		family := scripthost.IntegerPointer(record, "client_family")
		if family != nil && *family != 4 && *family != 6 {
			failed := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: fmt.Sprintf("lookups[%d].client_family is %d, not 4 or "+
				"6", index, *family)}
			return nil, a.failed(failed), failed
		}
		converted = append(converted, dnslookup.Record{
			DeduplicationKey: scripthost.Text(record, "deduplication_key"), ClientAddress: scripthost.Text(record, "client_address"),
			ClientFamily: family, Domain: scripthost.Text(record, "domain"), Action: scripthost.Text(record, "action"),
			AnswerSource: scripthost.TextPointer(record, "answer_source"), Rcode: scripthost.TextPointer(record, "rcode"),
			DNSSECStatus: scripthost.TextPointer(record, "dnssec_status"), BlocklistName: scripthost.Text(record, "blocklist_name"),
			LookedUpAt: scripthost.Integer(record, "looked_up_at"),
		})
	}
	return converted, result, nil
}

// dumpAdapter is a plugin as a resolver_cache or resolver_local_data source; the two kinds
// share the dump.
type dumpAdapter struct {
	adapter
	function string
}

func (a dumpAdapter) Probe(ctx context.Context, session provider.Session) (provider.Result, error) {
	return a.probe(ctx, session)
}

func (a dumpAdapter) Cache(ctx context.Context, session provider.Session) (recorddump.Dump, provider.Result, error) {
	return a.dump(ctx, session)
}

func (a dumpAdapter) LocalData(ctx context.Context, session provider.Session) (
	recorddump.Dump, provider.Result, error) {
	return a.dump(ctx, session)
}

func (a dumpAdapter) dump(ctx context.Context, session provider.Session) (recorddump.Dump, provider.Result, error) {
	checked, result, err := a.run(ctx, session, a.function, "dump_answer")
	if err != nil {
		return recorddump.Dump{}, result, err
	}
	rows := scripthost.Records(checked, "records")
	dump := recorddump.Dump{ResponseBytes: int(scripthost.Integer(checked, "response_bytes")),
		Records: make([]recorddump.Record, 0, len(rows))}
	for index, record := range rows {
		shape, known := scripthost.ShapeOf(scripthost.Text(record, "ttl_shape"))
		if !known {
			failed := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: fmt.Sprintf("records[%d].ttl_shape is not a shape",
				index)}
			return recorddump.Dump{}, a.failed(failed), failed
		}
		dump.Records = append(dump.Records, recorddump.Record{OwnerName: scripthost.Text(record, "owner_name"),
			TTL: scripthost.Text(record, "ttl"), TTLShape: shape, RRType: scripthost.Text(record, "rrtype"), Value: scripthost.Text(record, "value")})
	}
	for _, count := range scripthost.Records(checked, "not_records") {
		dump.NotRecords = append(dump.NotRecords, recorddump.NotRecordCount{Count: int(scripthost.Integer(count, "count")),
			Wording: scripthost.Text(count, "wording")})
	}
	return dump, result, nil
}

// measurementAdapter is a plugin as a measurement_sample source.
type measurementAdapter struct{ adapter }

func (a measurementAdapter) Probe(ctx context.Context, session provider.Session) (provider.Result, error) {
	return a.probe(ctx, session)
}

func (a measurementAdapter) Sample(ctx context.Context, session provider.Session, snapshot provider.Discovery,
	providerID, now int64) ([]vocab.MeasurementSample, []string, provider.Result, error) {
	return a.sampleWith(ctx, session, "sample", snapshot, providerID, now)
}

// sampleWith runs a function answering a sample_answer: sample, or a live pass's function.
func (a measurementAdapter) sampleWith(ctx context.Context, session provider.Session, function string,
	snapshot provider.Discovery, providerID, now int64) ([]vocab.MeasurementSample, []string, provider.Result, error) {
	checked, result, err := a.run(ctx, session, function, "sample_answer", DiscoveryView(snapshot), now)
	if err != nil {
		return nil, nil, result, err
	}
	var readings []vocab.MeasurementSample
	for index, record := range scripthost.Records(checked, "readings") {
		reading := vocab.MeasurementSample{
			SubjectKind: vocab.SubjectKind(scripthost.Text(record, "subject_kind")), SubjectKey: scripthost.Text(record, "subject_key"),
			Measure: vocab.Measure(scripthost.Text(record, "measure")), Unit: vocab.Unit(scripthost.Text(record, "unit")),
			Value: scripthost.Number(record, "value"), SampledAt: scripthost.Integer(record, "sampled_at"),
		}
		if !provider.TermShapeValid(string(reading.SubjectKind)) || !provider.TermShapeValid(string(reading.Measure)) ||
			!provider.TermShapeValid(string(reading.Unit)) {
			failed := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: fmt.Sprintf("readings[%d] has a subject kind, measure "+
				"or unit that is not a lower-case word with no space", index)}
			return nil, nil, a.failed(failed), failed
		}
		if scripthost.Flag(record, "attributed", true) {
			id := providerID
			reading.ProviderID = &id
		}
		readings = append(readings, reading)
	}
	return readings, scripthost.Texts(checked, "missing"), result, nil
}

// DiscoveryView is the discovery snapshot as a sampling script reads it: the identifiers it may
// pass to an endpoint taking interface names, the devices it must never pass there, and the
// device each identifier runs on.
func DiscoveryView(snapshot provider.Discovery) map[string]any {
	identifiers := make([]any, 0, len(snapshot.Identifiers))
	for _, identifier := range snapshot.Identifiers {
		identifiers = append(identifiers, string(identifier))
	}
	devices := make([]any, 0, len(snapshot.Devices))
	for _, device := range scripthost.SortedKeys(snapshot.Devices) {
		devices = append(devices, device)
	}
	return map[string]any{
		"identifiers": identifiers,
		"devices":     devices,
		"isDevice": func(name string) bool {
			_, found := snapshot.Devices[name]
			return found
		},
		"isIdentifier": func(name string) bool {
			_, found := snapshot.InterfaceIDByIdentifier[name]
			return found
		},
		// deviceOf is the network device an identifier runs on, or "" when discovery does not
		// know the identifier.
		"deviceOf": func(identifier string) string {
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
		},
	}
}

// reconciledStateAdapter is a plugin as a reconciled_state source.
type reconciledStateAdapter struct{ adapter }

func (a reconciledStateAdapter) Probe(ctx context.Context, session provider.Session) (provider.Result, error) {
	return a.probe(ctx, session)
}

func (a reconciledStateAdapter) Snapshot(ctx context.Context, session provider.Session) (
	[]reconciledstate.Set, provider.Result, error) {
	checked, result, err := a.run(ctx, session, "snapshot", "snapshot_answer")
	if err != nil {
		return nil, result, err
	}
	var sets []reconciledstate.Set
	for index, record := range scripthost.Records(checked, "sets") {
		set := reconciledstate.Set{SetKey: scripthost.Text(record, "set_key")}
		if !provider.TermShapeValid(set.SetKey) {
			failed := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: fmt.Sprintf("sets[%d].set_key is not a lower-case "+
				"word with no space", index)}
			return nil, a.failed(failed), failed
		}
		for _, item := range scripthost.Records(record, "items") {
			set.Items = append(set.Items, vocab.StateItem{ItemKey: scripthost.Text(item, "item_key"),
				Attributes: scripthost.TextPointer(item, "attributes"), ValidUntilAt: scripthost.IntegerPointer(item, "valid_until_at")})
		}
		sets = append(sets, set)
	}
	return sets, result, nil
}

// declaredKindAdapter is a plugin as the source of a kind it declares, writing into its own tables.
type declaredKindAdapter struct {
	adapter
	tables map[string]plugindecl.PluginTable
}

// declaredContract is the contract of a declared kind: probe and collect, whose answer holds the
// rows of the plugin's tables and its durable state.
func declaredContract(kind string) provider.Contract {
	return provider.Contract{
		Kind: kind,
		Records: []provider.Record{
			{Name: "probe_answer", Fields: []provider.Field{
				{Name: "result", Type: provider.FieldRecord, Of: "result", Required: true},
				{Name: "error", Type: provider.FieldText},
			}},
			{Name: "collect_answer", Fields: []provider.Field{
				{Name: "result", Type: provider.FieldRecord, Of: "result", Required: true},
				{Name: "rows", Type: provider.FieldObject},
				{Name: "state", Type: provider.FieldObject},
				{Name: "error", Type: provider.FieldText},
				{Name: "unsupported", Type: provider.FieldText},
			}},
		},
	}
}

func (a declaredKindAdapter) Probe(ctx context.Context, session provider.Session) (provider.Result, error) {
	return a.probe(ctx, session)
}

func (a declaredKindAdapter) Collect(ctx context.Context, session provider.Session, state map[string]string) (
	pluginkind.Pass, provider.Result, error) {
	stateArgument := map[string]any{}
	for key, value := range state {
		stateArgument[key] = value
	}
	answer, _, failed := a.invoker.invoke(ctx, session, "collect", stateArgument)
	if failed != nil {
		return pluginkind.Pass{}, a.failed(failed), failed
	}
	object, isObject := answer.(map[string]any)
	if !isObject {
		invalid := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: "collect returned no object"}
		return pluginkind.Pass{}, a.failed(invalid), invalid
	}
	rows, state2, err := a.rowsAndState(object)
	if err != nil {
		invalid := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: err.Error()}
		return pluginkind.Pass{}, a.failed(invalid), invalid
	}
	delete(object, "rows")
	delete(object, "state")
	answerErr, _ := object["error"].(error)
	delete(object, "error")
	checked, err := scripthost.CheckRecord(a.contract, "collect_answer", object, "collect")
	if err != nil {
		invalid := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: err.Error()}
		return pluginkind.Pass{}, a.failed(invalid), invalid
	}
	result, err := scripthost.ResultOf(checked["result"].(map[string]any), a.endpoints)
	if err != nil {
		invalid := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: err.Error()}
		return pluginkind.Pass{}, a.failed(invalid), invalid
	}
	if answerErr != nil {
		return pluginkind.Pass{}, result, answerErr
	}
	if unsupported := scripthost.Text(checked, "unsupported"); unsupported != "" {
		return pluginkind.Pass{}, result, fmt.Errorf("%w: %s", provider.ErrUnsupportedRead, unsupported)
	}
	return pluginkind.Pass{Rows: rows, State: state2}, result, nil
}

// rowsAndState checks the rows and the state of a collect answer.
func (a declaredKindAdapter) rowsAndState(object map[string]any) (plugintables.PluginRows, map[string]string, error) {
	rows, err := scripthost.CheckRows(a.tables, object["rows"])
	if err != nil {
		return nil, nil, err
	}
	state, err := scripthost.CheckState(object["state"])
	if err != nil {
		return nil, nil, err
	}
	return rows, state, nil
}

// errNoAdapter is a kind no adapter serves.
var errNoAdapter = errors.New("plugin: no adapter serves this kind")

// addToSet adds a plugin's provider to a source set, through the adapter of its kind.
func addToSet(set *sources.Set, manifest provider.Manifest, base adapter, plugin *Plugin,
	declaration contract.ProviderDeclaration) error {
	cadence := cadenceOf(declaration)
	switch manifest.Kind {
	case firewalllog.Kind:
		return set.AddFirewallLog(manifest, firewallLogAdapter{base})
	case securityevent.Kind:
		return set.AddSecurityEvent(manifest, securityEventAdapter{base})
	case dhcplease.Kind:
		return set.AddDHCPLease(manifest, dhcpLeaseAdapter{base})
	case dnslookup.Kind:
		pageBound, span, err := plugin.lookupBounds(declaration)
		if err != nil {
			return err
		}
		return set.AddDNSLookup(manifest, dnsLookupAdapter{adapter: base, pageBound: pageBound, span: span})
	case resolvercache.Kind:
		return set.AddResolverCache(manifest, dumpAdapter{adapter: base, function: "cache"})
	case resolverlocaldata.Kind:
		return set.AddResolverLocalData(manifest, dumpAdapter{adapter: base, function: "local_data"})
	case measurement.Kind:
		if live := plugin.lives[manifest.Key]; live != nil {
			return set.AddMeasurement(manifest, liveMeasurementAdapter{measurementAdapter: measurementAdapter{base},
				script: plugin.script, live: live})
		}
		return set.AddMeasurement(manifest, measurementAdapter{base})
	case ipgeolocation.Kind:
		return set.AddIPGeolocation(manifest, ipGeolocationAdapter{base})
	case registrabledomain.Kind:
		return set.AddRegistrableDomain(manifest, registrableDomainAdapter{base})
	case flowrecord.Kind:
		return set.AddFlowRecord(manifest, flowRecordAdapter{adapter: base, plugin: plugin, kind: manifest.Kind,
			key: manifest.Key})
	case reconciledstate.Kind:
		return set.AddReconciledState(manifest, reconciledstate.Registration{Plugin: plugin.Name, Cadence: cadence,
			MinCadence: time.Duration(declaration.MinCadenceSeconds) * time.Second, Triggers: declaration.TriggeredBy,
			Source: reconciledStateAdapter{base}})
	}
	if _, core := provider.ContractOf(manifest.Kind); core {
		return fmt.Errorf("%w: %s", errNoAdapter, manifest.Kind)
	}
	tables := map[string]plugindecl.PluginTable{}
	for _, table := range plugin.OwnTables {
		tables[table.Table] = table
	}
	return set.AddDeclared(manifest, pluginkind.Registration{Plugin: plugin.Name, Cadence: cadence,
		MinCadence: time.Duration(declaration.MinCadenceSeconds) * time.Second,
		Triggers:   declaration.TriggeredBy,
		Source:     declaredKindAdapter{adapter: base, tables: tables}})
}

// lookupBounds reads the two values a dns_lookup implementation states: from its script's exports,
// or, for a declarative read, one page and no requested window.
func (p *Plugin) lookupBounds(declaration contract.ProviderDeclaration) (int, int64, error) {
	if declaration.Read != nil {
		return 1, 0, nil
	}
	bound, err := p.script.ExportedValue(dnslookup.Kind, "page_bound")
	if err != nil {
		return 0, 0, err
	}
	span, err := p.script.ExportedValue(dnslookup.Kind, "requested_span_seconds")
	if err != nil {
		return 0, 0, err
	}
	pageBound, ok := scripthost.IntegerOf(bound)
	if !ok || pageBound < 1 {
		return 0, 0, fmt.Errorf("dns_lookup.page_bound is %v, not a whole number of 1 or more", bound)
	}
	seconds, ok := scripthost.IntegerOf(span)
	if !ok || seconds < 0 {
		return 0, 0, fmt.Errorf("dns_lookup.requested_span_seconds is %v, not a whole number of 0 or more", span)
	}
	return int(pageBound), seconds, nil
}

// scriptEndpoint is the probe an availability row names when the plugin itself, and no endpoint,
// decided the outcome.
func scriptEndpoint(plugin, file string) opnsense.Endpoint {
	if file == "" {
		file = contract.ManifestFile
	}
	return opnsense.Endpoint{Method: "plugin", Path: plugin + "/" + file}
}

// flowRecordAdapter is a plugin as a flow_record source: what it reads by polling, the exporter's
// capture configuration. Its records arrive through its push entry (pushkind.go), never here, so its
// availability is the configuration read's and its push entry's together (withPushAvailability): a
// readable configuration with nothing received is not healthy.
type flowRecordAdapter struct {
	adapter
	plugin    *Plugin
	kind, key string
}

func (a flowRecordAdapter) Probe(ctx context.Context, session provider.Session) (provider.Result, error) {
	result, err := a.probe(ctx, session)
	return a.plugin.withPushAvailability(a.kind, a.key, result), err
}

func (a flowRecordAdapter) Capture(ctx context.Context, session provider.Session) (
	flowrecord.Configuration, provider.Result, error) {
	configuration, result, err := a.capture(ctx, session)
	return configuration, a.plugin.withPushAvailability(a.kind, a.key, result), err
}

func (a flowRecordAdapter) capture(ctx context.Context, session provider.Session) (
	flowrecord.Configuration, provider.Result, error) {
	checked, result, err := a.run(ctx, session, "capture", "capture_answer")
	if err != nil {
		return flowrecord.Configuration{}, result, err
	}
	captured, present := checked["capture"].(map[string]any)
	if !present {
		failed := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: "capture returned neither a capture nor an error"}
		return flowrecord.Configuration{}, a.failed(failed), failed
	}
	configuration := flowrecord.Configuration{Enabled: scripthost.Flag(captured, "enabled", false),
		ExportVersion:          scripthost.Text(captured, "export_version"),
		ActiveTimeoutSeconds:   scripthost.IntegerPointer(captured, "active_timeout_seconds"),
		InactiveTimeoutSeconds: scripthost.IntegerPointer(captured, "inactive_timeout_seconds")}
	seen := map[string]bool{}
	for index, row := range scripthost.Records(captured, "interfaces") {
		identifier := scripthost.Text(row, "identifier")
		if identifier == "" || seen[identifier] {
			failed := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: fmt.Sprintf("capture.interfaces[%d] names an empty or "+
				"repeated identifier", index)}
			return flowrecord.Configuration{}, a.failed(failed), failed
		}
		seen[identifier] = true
		captured := flowrecord.Interface{Identifier: identifier, Ingress: scripthost.Flag(row, "ingress", false),
			Egress: scripthost.Flag(row, "egress", false), Exported: scripthost.Text(row, "exported"),
			NotExportedReason: scripthost.Text(row, "not_exported_reason"), ParentOf: scripthost.Texts(row, "parent_of")}
		if captured.Exported == "" {
			// A provider that does not say whether the interface is exported has not established it.
			captured.Exported = flowrecord.ExportedUnknown
		}
		if (captured.Exported == flowrecord.ExportedNo) != (captured.NotExportedReason != "") {
			failed := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: fmt.Sprintf("capture.interfaces[%d] "+
				"names a not_exported_reason exactly when exported is not \"no\"", index)}
			return flowrecord.Configuration{}, a.failed(failed), failed
		}
		for _, of := range captured.ParentOf {
			if of == "" || of == identifier {
				failed := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: fmt.Sprintf("capture.interfaces[%d] "+
					"lists an empty identifier, or itself, in parent_of", index)}
				return flowrecord.Configuration{}, a.failed(failed), failed
			}
		}
		configuration.Interfaces = append(configuration.Interfaces, captured)
	}
	for _, timeout := range []*int64{configuration.ActiveTimeoutSeconds, configuration.InactiveTimeoutSeconds} {
		if timeout != nil && *timeout <= 0 {
			failed := &scripthost.Failure{Code: scripthost.FailureRecords, Detail: "capture states a timeout that is not positive"}
			return flowrecord.Configuration{}, a.failed(failed), failed
		}
	}
	return configuration, result, nil
}
