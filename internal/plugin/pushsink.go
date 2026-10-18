package plugin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/plugin/scripthost"
	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// How a push entry's records become rows (PS3, PS8): its regular expression -- the fast path, no
// script -- its field mapping, or its parse function, called on a batch; then its own table, in one
// transaction per batch.

// The counters a push sink adds to its subscription's (docs/plugins.md, "Push sources").
const (
	pushCounterPatternUnmatched = "pattern_unmatched"
	pushCounterSkipped          = "skipped"
	pushCounterRowsRefused      = "rows_refused"
	pushCounterScriptCalls      = "script_calls"
	pushCounterScriptFailed     = "script_failed"
	pushCounterScriptTimeout    = "script_timeout"
	pushCounterRecordsInvalid   = "records_invalid"
)

// ParseBatch bounds the messages one call of a parse function is handed: N messages are at most
// ceil(N / ParseBatch) calls, plus the flushes the time bound makes.
const ParseBatch = 500

// pushHost is what a load's push entries share: the hub, once the service has started one.
type pushHost struct {
	hub atomic.Pointer[push.Hub]
}

// AttachPushHub gives the loaded plugins the hub their push entries are routed by, so their
// availability reads its listeners and their liveness.
func (l *Loaded) AttachPushHub(hub *push.Hub) {
	if l.push != nil {
		l.push.hub.Store(hub)
	}
}

// PushEnvironment is what the push entries' sinks need of the service.
type PushEnvironment struct {
	// Store is where rows go.
	Store *store.Store
	// Session is the session a parse function runs in: its broker says the firewall's version, which
	// picks the script of the version's range (A19).
	Session provider.Session
	// Kinds receives the records of the entries that deliver a core kind (pushkind.go); nil refuses
	// them.
	Kinds provider.PushReceiver
	// Report is told what goes wrong outside any batch: a condition of a stream that could not be
	// recorded. Nil reports nothing.
	Report func(error)
}

// PushSubscriptions returns the subscription of every push entry of every loaded plugin, each with
// its sink, for the hub to route to.
func (l *Loaded) PushSubscriptions(environment PushEnvironment) ([]push.Subscription, error) {
	var subscriptions []push.Subscription
	for _, plugin := range l.Members {
		for _, binding := range plugin.pushes {
			if binding.table.Table == "" && environment.Kinds == nil {
				return nil, fmt.Errorf("plugin: %s delivers %s records and no receiver of them is wired",
					binding.ID(), binding.declaration.Kind)
			}
			subscriptions = append(subscriptions, pushSubscriptionOf(plugin, binding, environment))
		}
	}
	return subscriptions, nil
}

// pushSubscriptionOf is one push entry's subscription, with its sink.
func pushSubscriptionOf(plugin *Plugin, binding *pushBinding, environment PushEnvironment) push.Subscription {
	var sink push.Sink
	if binding.table.Table != "" {
		sink = &tableSink{binding: binding, plugin: plugin, environment: environment}
	} else {
		sink = &kindSink{binding: binding, plugin: plugin, environment: environment}
	}
	return push.Subscription{
		ID: binding.ID(), Protocol: binding.declaration.Protocol, Selector: binding.selector,
		Transports: binding.transports, Sink: sink, Liveness: binding.liveness,
		Describe: push.SubscriptionDescription{Plugin: plugin.Name, Entry: binding.declaration.Name,
			OutputTable: binding.declaration.OutputTable, Kind: binding.declaration.Kind,
			Columns: binding.columns(), Replaces: binding.declaration.Replaces},
	}
}

// PushProviders maps every push entry's subscription to the provider its availability and gaps
// belong to.
func (l *Loaded) PushProviders() map[string][2]string {
	providers := map[string][2]string{}
	for _, plugin := range l.Members {
		for _, binding := range plugin.pushes {
			providers[binding.ID()] = [2]string{binding.providerKind, binding.providerKey}
		}
	}
	return providers
}

// PushEntries returns a plugin's push entries, as the listing reports them.
func (p *Plugin) PushEntries() []PushEntry {
	entries := []PushEntry{}
	for _, binding := range p.pushes {
		entries = append(entries, binding.listing())
	}
	return entries
}

// tableSink writes a push entry's rows into one of its plugin's tables.
type tableSink struct {
	binding     *pushBinding
	plugin      *Plugin
	environment PushEnvironment
}

// tableBatch is what Prepare made: the rows to insert.
type tableBatch struct {
	rows []map[string]any
	// span is the reception instants of the batch's messages, for the derivations it runs.
	span batchSpan
}

func (s *tableSink) Prepare(ctx context.Context, records []push.Record) push.Prepared {
	prepared := push.Prepared{Counts: map[string]int64{}}
	var rows []map[string]any
	if s.binding.declaration.Decode != "" {
		// One call per message: each answers a list of the rows the message held.
		for _, record := range records {
			answers, failed := callDecode(ctx, s.plugin, s.environment.Session, s.binding.declaration.Decode, record,
				prepared.Counts)
			if failed != "" {
				prepared.Failed++
				prepared.FailedReason = push.GapScriptFailed
				continue
			}
			for _, answer := range answers {
				object, isObject := answer.(map[string]any)
				if !isObject {
					prepared.Counts[pushCounterRecordsInvalid]++
					continue
				}
				if row, ok := s.convert(object, record, prepared.Counts); ok {
					rows = append(rows, row)
				}
			}
		}
	} else if s.binding.declaration.Parse != "" {
		for start := 0; start < len(records); start += ParseBatch {
			chunk := records[start:min(len(records), start+ParseBatch)]
			answers, failed := callParse(ctx, s.plugin, s.environment.Session, s.binding.declaration.Parse, chunk,
				prepared.Counts)
			if failed != "" {
				prepared.Failed += len(chunk)
				prepared.FailedReason = push.GapScriptFailed
				continue
			}
			for index, answer := range answers {
				if answer == nil {
					prepared.Counts[pushCounterSkipped]++
					continue
				}
				object, isObject := answer.(map[string]any)
				if !isObject {
					prepared.Counts[pushCounterRecordsInvalid]++
					continue
				}
				if row, ok := s.convert(object, chunk[index], prepared.Counts); ok {
					rows = append(rows, row)
				}
			}
		}
	} else {
		for _, record := range records {
			values, matched := s.extract(record)
			if !matched {
				prepared.Counts[pushCounterPatternUnmatched]++
				continue
			}
			if row, ok := s.convert(values, record, prepared.Counts); ok {
				rows = append(rows, row)
			}
		}
	}
	prepared.Rows = len(rows)
	if len(rows) > 0 {
		prepared.Payload = tableBatch{rows: rows, span: spanOf(records)}
	}
	return prepared
}

// extract reads a record's values by the entry's pattern and field mapping.
func (s *tableSink) extract(record push.Record) (map[string]any, bool) {
	values := map[string]any{}
	for column, sources := range s.binding.declaration.Fields {
		for _, source := range sources {
			if value, present := record.Fields[source]; present && value != nil {
				values[column] = value
				break
			}
		}
	}
	if pattern := s.binding.pattern; pattern != nil {
		match := pattern.FindStringSubmatch(record.Text)
		if match == nil {
			return nil, false
		}
		for index, group := range pattern.SubexpNames() {
			if group != "" && index < len(match) && match[index] != "" {
				values[group] = match[index]
			}
		}
	}
	return values, true
}

// convert types a row by its table, filling the time column with the record's instant when nothing
// else did. A value that does not convert is null in a nullable column and refuses the row in a NOT
// NULL one, counted push_value_invalid either way; a NOT NULL column left empty refuses the row.
func (s *tableSink) convert(values map[string]any, record push.Record, counts map[string]int64) (map[string]any, bool) {
	table := s.binding.table
	row := make(map[string]any, len(table.Columns))
	for name := range values {
		if _, isColumn := tableColumn(table, name); !isColumn {
			counts[pushCounterRowsRefused]++
			return nil, false
		}
	}
	for _, column := range table.Columns {
		value := values[column.Name]
		if value == nil && column.Name == table.TimeColumn && !record.Instant.IsZero() {
			value = record.Instant
		}
		typed, err := push.Convert(column.Type, value)
		if err != nil {
			counts[push.CounterValueInvalid]++
			if !column.Nullable {
				counts[pushCounterRowsRefused]++
				return nil, false
			}
			typed = nil
		}
		if typed == nil && !column.Nullable {
			counts[pushCounterRowsRefused]++
			return nil, false
		}
		row[column.Name] = typed
	}
	return row, true
}

func (s *tableSink) Store(ctx context.Context, prepared push.Prepared) error {
	batch, ok := prepared.Payload.(tableBatch)
	if !ok || len(batch.rows) == 0 {
		return nil
	}
	if s.environment.Store == nil {
		return errors.New("plugin: no store is wired for the push entries")
	}
	if _, err := s.environment.Store.InsertPluginRows(ctx, s.binding.table, batch.rows); err != nil {
		return err
	}
	s.plugin.notePush(s.binding.declaration.Name, len(batch.rows), batch.span.from, batch.span.to, batch.span.to)
	return nil
}

// batchSpan is the reception instants a batch spans.
type batchSpan struct{ from, to time.Time }

// spanOf is the reception instants of a batch's records.
func spanOf(records []push.Record) batchSpan {
	var span batchSpan
	for index, record := range records {
		if index == 0 || record.ReceivedAt.Before(span.from) {
			span.from = record.ReceivedAt
		}
		if index == 0 || record.ReceivedAt.After(span.to) {
			span.to = record.ReceivedAt
		}
	}
	return span
}

// callParse calls a parse function on a batch, returning one answer per message. A call that throws,
// times out or answers with anything but a list of the batch's length fails the whole batch: its
// reason, counted.
func callParse(ctx context.Context, plugin *Plugin, session provider.Session, function string, records []push.Record,
	counts map[string]int64) ([]any, string) {
	messages := make([]any, 0, len(records))
	for _, record := range records {
		messages = append(messages, messageObject(record))
	}
	counts[pushCounterScriptCalls]++
	if plugin.script == nil {
		counts[pushCounterScriptFailed]++
		return nil, pushCounterScriptFailed
	}
	answer, _, failed := plugin.script.RunWith(ctx, session, pushParseNamespace, function, scripthost.RunOptions{Plain: true},
		messages)
	if failed != nil {
		reason := pushCounterScriptFailed
		switch failed.Code {
		case scripthost.FailureTimeout:
			reason = pushCounterScriptTimeout
		case scripthost.FailureVersionUnsupported:
			reason = string(scripthost.FailureVersionUnsupported)
		}
		counts[reason]++
		return nil, reason
	}
	list, isList := answer.([]any)
	if !isList || len(list) != len(records) {
		counts[pushCounterRecordsInvalid]++
		return nil, pushCounterRecordsInvalid
	}
	return list, ""
}

// callDecode calls a decode function on one message of a script-decoded protocol (PA7), returning what it
// held: a list, possibly empty. A call that throws, times out or answers with anything but a list loses the
// message: its reason, counted.
func callDecode(ctx context.Context, plugin *Plugin, session provider.Session, function string, record push.Record,
	counts map[string]int64) ([]any, string) {
	counts[pushCounterScriptCalls]++
	if plugin.script == nil {
		counts[pushCounterScriptFailed]++
		return nil, pushCounterScriptFailed
	}
	answer, _, failed := plugin.script.RunWith(ctx, session, pushParseNamespace, function, scripthost.RunOptions{Plain: true},
		decodedMessage(record))
	if failed != nil {
		reason := pushCounterScriptFailed
		switch failed.Code {
		case scripthost.FailureTimeout:
			reason = pushCounterScriptTimeout
		case scripthost.FailureVersionUnsupported:
			reason = string(scripthost.FailureVersionUnsupported)
		}
		counts[reason]++
		return nil, reason
	}
	if answer == nil {
		return nil, ""
	}
	list, isList := answer.([]any)
	if !isList {
		counts[pushCounterRecordsInvalid]++
		return nil, pushCounterRecordsInvalid
	}
	return list, ""
}

// decodedMessage is one message of a script-decoded protocol as a decode function receives it: its octets,
// each a number from 0 to 255, its text (the octets read as UTF-8, an invalid sequence replaced), the
// sender's address, the transport and the reception instant as UTC epoch seconds with its fraction.
func decodedMessage(record push.Record) map[string]any {
	payload, _ := record.Fields[push.PayloadField].([]byte)
	octets := make([]any, len(payload))
	for index, octet := range payload {
		octets[index] = int64(octet)
	}
	return map[string]any{
		"bytes":       octets,
		"text":        strings.ToValidUTF8(string(payload), "�"),
		"sender":      record.Sender.String(),
		"transport":   record.Transport,
		"received_at": epochSeconds(record.ReceivedAt),
	}
}

// messageObject is one decoded message as a parse function receives it: the header's fields, every
// instant as UTC epoch seconds with its fraction, the sender's address and the transport.
func messageObject(record push.Record) map[string]any {
	object := make(map[string]any, len(record.Fields)+3)
	for name, value := range record.Fields {
		object[name] = scriptValue(value)
	}
	object["sender"] = record.Sender.String()
	object["transport"] = record.Transport
	object["received_at"] = epochSeconds(record.ReceivedAt)
	return object
}

// scriptValue is a decoded value as JavaScript reads it.
func scriptValue(value any) any {
	switch typed := value.(type) {
	case time.Time:
		return epochSeconds(typed)
	case nil:
		return nil
	case string, bool, int64, float64:
		return typed
	case uint64:
		return float64(typed)
	}
	converted, err := push.Convert(push.TypeText, value)
	if err != nil {
		return nil
	}
	return converted
}

// epochSeconds is an instant as UTC epoch seconds with its fraction.
func epochSeconds(at time.Time) float64 {
	return float64(at.UnixNano()) / 1e9
}

// pushProviderImplementation is a provider of a kind of the plugin's own that its push entries feed: its
// probe and its pass read the hub, never the firewall. Its availability is listener_not_configured
// while no listener of its protocol is open, no_message_received while one is and nothing has arrived
// within the liveness span, and healthy while a message has.
type pushProviderImplementation struct {
	plugin *Plugin
	kind   string
	key    string
}

// Call answers the probe and the pass alike: the hub's state, and no row.
func (a pushProviderImplementation) Call(context.Context, provider.Session, string, ...any) (provider.Answer,
	provider.Result, error) {
	return provider.Answer{}, a.result(), nil
}

func (a pushProviderImplementation) result() provider.Result {
	return a.plugin.pushAvailability(a.kind, a.key)
}

// pushAvailability is the availability of the push entries feeding one provider of the plugin:
// listener_not_configured while no listener of their protocol is open, no_message_received while one
// is and nothing arrived within the liveness span, and healthy while a message has.
func (p *Plugin) pushAvailability(kind, key string) provider.Result {
	result := provider.Result{State: vocab.StateUnavailable, Outcome: vocab.OutcomeListenerNotConfigured,
		Probe: scriptEndpoint(p.Name, contract.ManifestFile), Separable: true,
		Detail: "no push listener of this provider's protocol is configured: start opnview with --push-listen"}
	var hub *push.Hub
	if p.pushHost != nil {
		hub = p.pushHost.hub.Load()
	}
	if hub == nil {
		return result
	}
	listening := false
	for _, binding := range p.pushes {
		if binding.providerKind != kind || binding.providerKey != key {
			continue
		}
		state, found := hub.SubscriptionState(binding.ID())
		if !found || state.ListenerState != push.ListenerListening {
			continue
		}
		listening = true
		if state.Liveness.Alive {
			result.State, result.Outcome = vocab.StateReachable, vocab.OutcomeHealthy
			result.Detail = fmt.Sprintf("the push entry %s is receiving", binding.declaration.Name)
			return result
		}
	}
	if listening {
		result.Outcome = vocab.OutcomeNoMessageReceived
		result.Detail = noMessageDetail(hub.Senders().State())
	}
	return result
}

// SequenceAsserted judges a loss the sender's sequence numbers show by the entry's declared bound.
func (s *tableSink) SequenceAsserted(ctx context.Context, problem push.Problem) bool {
	return s.binding.sequenceAsserted(ctx, s.environment, problem)
}

// sequenceAsserted judges a loss its sender's sequence numbers show over the problem's span (NJ7, as PA7 of
// the plugin-API amendment restates it): an entry declaring no sequence bound asserts every loss; one
// declaring a bound asserts it only when the value it names, recorded for its provider, held one non-zero
// bound over both ends (Store.SequenceBoundAsserted). A bound that cannot be read asserts nothing: the loss
// is counted, never recorded as a gap the configuration could not vouch for.
func (b *pushBinding) sequenceAsserted(ctx context.Context, environment PushEnvironment, problem push.Problem) bool {
	bound := b.declaration.SequenceBound
	if bound == nil {
		return true
	}
	if environment.Store == nil {
		return false
	}
	providerID, err := environment.Store.ProviderID(ctx, b.providerKind, b.providerKey)
	if err != nil {
		return false
	}
	asserted, err := environment.Store.SequenceBoundAsserted(ctx, providerID, bound.Value, problem.From.Unix(),
		problem.To.Unix())
	return err == nil && asserted
}

// SequenceBound is one push entry taking its sequence bound from a value its provider records: the hub
// bounds the decoders of the entry's protocol by it.
type SequenceBound struct {
	Subscription string
	Protocol     string
	Kind, Key    string
	Value        string
}

// SequenceBounds lists every loaded push entry that declares a sequence bound.
func (l *Loaded) SequenceBounds() []SequenceBound {
	var bounds []SequenceBound
	for _, plugin := range l.Members {
		for _, binding := range plugin.pushes {
			if bound := binding.declaration.SequenceBound; bound != nil {
				bounds = append(bounds, SequenceBound{Subscription: binding.ID(), Protocol: binding.declaration.Protocol,
					Kind: binding.providerKind, Key: binding.providerKey, Value: bound.Value})
			}
		}
	}
	return bounds
}

// feedsByPush reports whether a push entry of the plugin feeds one of its providers.
func (p *Plugin) feedsByPush(kind, key string) bool {
	for _, binding := range p.pushes {
		if binding.providerKind == kind && binding.providerKey == key {
			return true
		}
	}
	return false
}

// withPushAvailability is a polled provider's result once the push entries feeding the same provider
// are taken into account: a read that succeeded is not the provider's whole availability when its
// records arrive by push, so a healthy read of a provider whose entries are not receiving takes their
// outcome and detail. A failed read keeps its own, which names the firewall's answer. The result stays
// separable, so the probe round still activates the provider and its configuration is still read.
func (p *Plugin) withPushAvailability(kind, key string, result provider.Result) provider.Result {
	if result.Outcome != vocab.OutcomeHealthy || !p.feedsByPush(kind, key) {
		return result
	}
	pushed := p.pushAvailability(kind, key)
	if pushed.Outcome == vocab.OutcomeHealthy {
		return result
	}
	result.State, result.Outcome = pushed.State, pushed.Outcome
	if result.Detail == "" {
		result.Detail = pushed.Detail
	} else {
		result.Detail = pushed.Detail + "; " + result.Detail
	}
	return result
}

// noMessageDetail says why a listening push provider has received nothing. When senders were refused,
// that is the likeliest cause and it is named first, with the addresses and their counts: a network
// that rewrites source addresses (Docker Desktop's port publishing) shows here, without logging in.
func noMessageDetail(senders push.SendersState) string {
	const silent = "the listener is open and no message of this provider arrived within its liveness span"
	if len(senders.Refused) == 0 && senders.RefusedUnlisted == 0 {
		return silent + ": the firewall's export is off, filtered, or sent elsewhere"
	}
	var refused strings.Builder
	for i, sender := range senders.Refused {
		if i > 0 {
			refused.WriteString(", ")
		}
		fmt.Fprintf(&refused, "%s (%d)", sender.Address, sender.Count)
	}
	if senders.RefusedUnlisted > 0 {
		if refused.Len() > 0 {
			refused.WriteString(", ")
		}
		fmt.Fprintf(&refused, "%d more from unlisted addresses", senders.RefusedUnlisted)
	}
	return silent + "; messages were refused from senders that are not the firewall: " + refused.String() +
		". A network that rewrites source addresses refuses everything the firewall sends: run opnview " +
		"with host networking, or name the rewritten address with --push-extra-senders and filter the port " +
		"to the firewall's address"
}
