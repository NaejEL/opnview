package plugin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NaejEL/opnview/internal/collect/dhcplease"
	"github.com/NaejEL/opnview/internal/collect/dnslookup"
	"github.com/NaejEL/opnview/internal/collect/firewalllog"
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
	"github.com/NaejEL/opnview/internal/store"
)

// The kind adapters: each turns a plugin into the Source interface of the kind it implements, so
// the kind's coordinator in internal/collect runs it exactly as it ran the compiled
// implementations it replaces. An adapter calls the plugin -- its script, or its declarative
// form -- checks the answer against the kind's contract, and converts it. Nothing else.

// invoker is a plugin's way of answering a function of a kind: a script, or a declarative form.
type invoker interface {
	invoke(ctx context.Context, session provider.Session, function string, arguments ...any) (
		answer any, hostErrors []error, failed *failure)
}

// scriptInvoker calls a script's exported functions under one kind's namespace.
type scriptInvoker struct {
	script    *script
	namespace string
}

func (i scriptInvoker) invoke(ctx context.Context, session provider.Session, function string,
	arguments ...any) (any, []error, *failure) {
	return i.script.call(ctx, session, i.namespace, function, arguments...)
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
		invalid := &failure{code: failureRecords, detail: fmt.Sprintf("%s returned no object", function)}
		return nil, a.failed(invalid), invalid
	}
	answerErr, _ := object["error"].(error)
	delete(object, "error")
	checked, err := checkRecord(a.contract, table, object, function)
	if err != nil {
		invalid := &failure{code: failureRecords, detail: err.Error()}
		return nil, a.failed(invalid), invalid
	}
	result, err := resultOf(checked["result"].(map[string]any), a.endpoints)
	if err != nil {
		invalid := &failure{code: failureRecords, detail: err.Error()}
		return nil, a.failed(invalid), invalid
	}
	if answerErr != nil {
		return checked, result, answerErr
	}
	if unsupported := text(checked, "unsupported"); unsupported != "" {
		return checked, result, fmt.Errorf("%w: %s", provider.ErrUnsupportedRead, unsupported)
	}
	return checked, result, nil
}

// failed is the result a failed call is recorded with.
func (a adapter) failed(failed *failure) provider.Result {
	return provider.Result{State: store.StateUnavailable, Outcome: failed.code, Probe: a.scriptProbe,
		Detail: fmt.Sprintf("the plugin %s: %s", a.plugin, failed.detail)}
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
	for _, record := range records(checked, "records") {
		converted = append(converted, firewalllog.Record{
			Digest: text(record, "digest"), ObservedAt: integer(record, "observed_at"),
			Device: text(record, "device"), SrcAddress: text(record, "src_address"),
			DstAddress: text(record, "dst_address"), SrcPort: integerPointer(record, "src_port"),
			DstPort: integerPointer(record, "dst_port"), Protocol: text(record, "protocol"),
			IPVersion: integer(record, "ip_version"), Action: text(record, "action"),
			Direction: text(record, "direction"), LogReason: textPointer(record, "log_reason"),
			PacketBytes: integer(record, "packet_bytes"), Rid: textPointer(record, "rid"),
			IPID: integerPointer(record, "ip_id"), TCPSeq: integerPointer(record, "tcp_seq"),
		})
	}
	if invalid := firewallLogRecordsInvalid(converted); invalid != "" {
		return a.invalid(invalid)
	}
	return converted, result, nil
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
	failed := &failure{code: failureRecords, detail: detail}
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
	view.Known = flag(checked, "known", false)
	for _, file := range texts(checked, "present") {
		view.Present[file] = struct{}{}
	}
	view.HighestSequence = integerPointer(checked, "highest_sequence")
	return view, result, nil
}

func (a securityEventAdapter) Alerts(ctx context.Context, session provider.Session, pageSize int) (
	[]securityevent.Alert, provider.Result, error) {
	checked, result, err := a.run(ctx, session, "alerts", "alerts_answer", pageSize)
	if err != nil {
		return nil, result, err
	}
	var converted []securityevent.Alert
	for index, record := range records(checked, "alerts") {
		alert := securityevent.Alert{
			FileID: text(record, "file_id"), ByteOffset: integer(record, "byte_offset"),
			OccurredAt: integer(record, "occurred_at"), RuleIdentity: text(record, "rule_identity"),
			Signature: text(record, "signature"), EventAction: text(record, "event_action"),
			NormalisedSeverity: textPointer(record, "normalised_severity"),
			SrcAddress:         text(record, "src_address"), DstAddress: text(record, "dst_address"),
			SrcPort: integerPointer(record, "src_port"), DstPort: integerPointer(record, "dst_port"),
			Protocol: textPointer(record, "protocol"), InInterfaceDevice: textPointer(record, "in_interface_device"),
		}
		if !portValid(alert.SrcPort) || !portValid(alert.DstPort) {
			failed := &failure{code: failureRecords, detail: fmt.Sprintf("alerts[%d] carries a port outside 0 to "+
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
	leases := records(checked, "leases")
	if leases == nil {
		return nil, result, nil
	}
	converted := make([]dhcplease.Observation, 0, len(leases))
	for _, record := range leases {
		converted = append(converted, dhcplease.Observation{
			Address: text(record, "address"), MAC: dhcplease.MACPointer(text(record, "mac")),
			Hostname: textPointer(record, "hostname"), DHCPClientID: textPointer(record, "dhcp_client_id"),
			DUID: textPointer(record, "duid"), IAID: textPointer(record, "iaid"),
			VendorHint: textPointer(record, "vendor_hint"), LeaseState: text(record, "lease_state"),
			InterfaceIdentifier: text(record, "interface_identifier"),
			InterfaceDevice:     text(record, "interface_device"),
			StartsAt:            integerPointer(record, "starts_at"), ExpiresAt: integerPointer(record, "expires_at"),
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
	lookups := records(checked, "lookups")
	converted := make([]dnslookup.Record, 0, len(lookups))
	for index, record := range lookups {
		family := integerPointer(record, "client_family")
		if family != nil && *family != 4 && *family != 6 {
			failed := &failure{code: failureRecords, detail: fmt.Sprintf("lookups[%d].client_family is %d, not 4 or "+
				"6", index, *family)}
			return nil, a.failed(failed), failed
		}
		converted = append(converted, dnslookup.Record{
			DeduplicationKey: text(record, "deduplication_key"), ClientAddress: text(record, "client_address"),
			ClientFamily: family, Domain: text(record, "domain"), Action: text(record, "action"),
			AnswerSource: textPointer(record, "answer_source"), Rcode: textPointer(record, "rcode"),
			DNSSECStatus: textPointer(record, "dnssec_status"), BlocklistName: text(record, "blocklist_name"),
			LookedUpAt: integer(record, "looked_up_at"),
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
	rows := records(checked, "records")
	dump := recorddump.Dump{ResponseBytes: int(integer(checked, "response_bytes")),
		Records: make([]recorddump.Record, 0, len(rows))}
	for index, record := range rows {
		shape, known := shapeOf(text(record, "ttl_shape"))
		if !known {
			failed := &failure{code: failureRecords, detail: fmt.Sprintf("records[%d].ttl_shape is not a shape",
				index)}
			return recorddump.Dump{}, a.failed(failed), failed
		}
		dump.Records = append(dump.Records, recorddump.Record{OwnerName: text(record, "owner_name"),
			TTL: text(record, "ttl"), TTLShape: shape, RRType: text(record, "rrtype"), Value: text(record, "value")})
	}
	for _, count := range records(checked, "not_records") {
		dump.NotRecords = append(dump.NotRecords, recorddump.NotRecordCount{Count: int(integer(count, "count")),
			Wording: text(count, "wording")})
	}
	return dump, result, nil
}

// measurementAdapter is a plugin as a measurement_sample source.
type measurementAdapter struct{ adapter }

func (a measurementAdapter) Probe(ctx context.Context, session provider.Session) (provider.Result, error) {
	return a.probe(ctx, session)
}

func (a measurementAdapter) Sample(ctx context.Context, session provider.Session, snapshot provider.Discovery,
	providerID, now int64) ([]store.MeasurementSample, []string, provider.Result, error) {
	checked, result, err := a.run(ctx, session, "sample", "sample_answer", DiscoveryView(snapshot), now)
	if err != nil {
		return nil, nil, result, err
	}
	var readings []store.MeasurementSample
	for index, record := range records(checked, "readings") {
		reading := store.MeasurementSample{
			SubjectKind: store.SubjectKind(text(record, "subject_kind")), SubjectKey: text(record, "subject_key"),
			Measure: store.Measure(text(record, "measure")), Unit: store.Unit(text(record, "unit")),
			Value: number(record, "value"), SampledAt: integer(record, "sampled_at"),
		}
		if !provider.TermShapeValid(string(reading.SubjectKind)) || !provider.TermShapeValid(string(reading.Measure)) ||
			!provider.TermShapeValid(string(reading.Unit)) {
			failed := &failure{code: failureRecords, detail: fmt.Sprintf("readings[%d] has a subject kind, measure "+
				"or unit that is not a lower-case word with no space", index)}
			return nil, nil, a.failed(failed), failed
		}
		if flag(record, "attributed", true) {
			id := providerID
			reading.ProviderID = &id
		}
		readings = append(readings, reading)
	}
	return readings, texts(checked, "missing"), result, nil
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
	for _, device := range sortedKeys(snapshot.Devices) {
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
	for index, record := range records(checked, "sets") {
		set := reconciledstate.Set{SetKey: text(record, "set_key")}
		if !provider.TermShapeValid(set.SetKey) {
			failed := &failure{code: failureRecords, detail: fmt.Sprintf("sets[%d].set_key is not a lower-case "+
				"word with no space", index)}
			return nil, a.failed(failed), failed
		}
		for _, item := range records(record, "items") {
			set.Items = append(set.Items, store.StateItem{ItemKey: text(item, "item_key"),
				Attributes: textPointer(item, "attributes"), ValidUntilAt: integerPointer(item, "valid_until_at")})
		}
		sets = append(sets, set)
	}
	return sets, result, nil
}

// declaredKindAdapter is a plugin as the source of a kind it declares, writing into its own tables.
type declaredKindAdapter struct {
	adapter
	tables map[string]store.PluginTable
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
		invalid := &failure{code: failureRecords, detail: "collect returned no object"}
		return pluginkind.Pass{}, a.failed(invalid), invalid
	}
	rows, state2, err := a.rowsAndState(object)
	if err != nil {
		invalid := &failure{code: failureRecords, detail: err.Error()}
		return pluginkind.Pass{}, a.failed(invalid), invalid
	}
	delete(object, "rows")
	delete(object, "state")
	answerErr, _ := object["error"].(error)
	delete(object, "error")
	checked, err := checkRecord(a.contract, "collect_answer", object, "collect")
	if err != nil {
		invalid := &failure{code: failureRecords, detail: err.Error()}
		return pluginkind.Pass{}, a.failed(invalid), invalid
	}
	result, err := resultOf(checked["result"].(map[string]any), a.endpoints)
	if err != nil {
		invalid := &failure{code: failureRecords, detail: err.Error()}
		return pluginkind.Pass{}, a.failed(invalid), invalid
	}
	if answerErr != nil {
		return pluginkind.Pass{}, result, answerErr
	}
	if unsupported := text(checked, "unsupported"); unsupported != "" {
		return pluginkind.Pass{}, result, fmt.Errorf("%w: %s", provider.ErrUnsupportedRead, unsupported)
	}
	return pluginkind.Pass{Rows: rows, State: state2}, result, nil
}

// rowsAndState checks the rows and the state of a collect answer.
func (a declaredKindAdapter) rowsAndState(object map[string]any) (store.PluginRows, map[string]string, error) {
	rows, err := checkRows(a.tables, object["rows"])
	if err != nil {
		return nil, nil, err
	}
	state, err := checkState(object["state"])
	if err != nil {
		return nil, nil, err
	}
	return rows, state, nil
}

// errNoAdapter is a kind no adapter serves.
var errNoAdapter = errors.New("plugin: no adapter serves this kind")

// addToSet adds a plugin's provider to a source set, through the adapter of its kind.
func addToSet(set *sources.Set, manifest provider.Manifest, base adapter, plugin *Plugin,
	declaration ProviderDeclaration) error {
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
		return set.AddMeasurement(manifest, measurementAdapter{base})
	case ipgeolocation.Kind:
		return set.AddIPGeolocation(manifest, ipGeolocationAdapter{base})
	case registrabledomain.Kind:
		return set.AddRegistrableDomain(manifest, registrableDomainAdapter{base})
	case reconciledstate.Kind:
		return set.AddReconciledState(manifest, reconciledstate.Registration{Plugin: plugin.Name, Cadence: cadence,
			MinCadence: time.Duration(declaration.MinCadenceSeconds) * time.Second, Triggers: declaration.TriggeredBy,
			Source: reconciledStateAdapter{base}})
	}
	if _, core := provider.ContractOf(manifest.Kind); core {
		return fmt.Errorf("%w: %s", errNoAdapter, manifest.Kind)
	}
	tables := map[string]store.PluginTable{}
	for _, table := range plugin.tables {
		tables[table.Table] = table
	}
	return set.AddDeclared(manifest, pluginkind.Registration{Plugin: plugin.Name, Cadence: cadence,
		MinCadence: time.Duration(declaration.MinCadenceSeconds) * time.Second,
		Triggers:   declaration.TriggeredBy,
		Source:     declaredKindAdapter{adapter: base, tables: tables}})
}

// lookupBounds reads the two values a dns_lookup implementation states: from its script's exports,
// or, for a declarative read, one page and no requested window.
func (p *Plugin) lookupBounds(declaration ProviderDeclaration) (int, int64, error) {
	if declaration.Read != nil {
		return 1, 0, nil
	}
	bound, err := p.script.exportedValue(dnslookup.Kind, "page_bound")
	if err != nil {
		return 0, 0, err
	}
	span, err := p.script.exportedValue(dnslookup.Kind, "requested_span_seconds")
	if err != nil {
		return 0, 0, err
	}
	pageBound, ok := integerOf(bound)
	if !ok || pageBound < 1 {
		return 0, 0, fmt.Errorf("dns_lookup.page_bound is %v, not a whole number of 1 or more", bound)
	}
	seconds, ok := integerOf(span)
	if !ok || seconds < 0 {
		return 0, 0, fmt.Errorf("dns_lookup.requested_span_seconds is %v, not a whole number of 0 or more", span)
	}
	return int(pageBound), seconds, nil
}

// scriptEndpoint is the probe an availability row names when the plugin itself, and no endpoint,
// decided the outcome.
func scriptEndpoint(plugin, file string) opnsense.Endpoint {
	if file == "" {
		file = ManifestFile
	}
	return opnsense.Endpoint{Method: "plugin", Path: plugin + "/" + file}
}
