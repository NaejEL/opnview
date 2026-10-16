package plugin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/store/plugindecl"
)

// What `opnview plugin test` runs a plugin through (internal/plugin/harness): one provider function
// called as its kind calls it, the plugin's push entries subscribed as the service subscribes them,
// and a function its script exports for its tests. The harness reaches the plugin through these and
// nothing else, so what a test exercises is the host's own code, and the host's internals stay its
// own.

// CallRefusal is a call the plugin cannot answer at all: a kind it does not provide, or a function
// the kind's contract does not have. A test is told so in Detail.
type CallRefusal struct{ Detail string }

func (r *CallRefusal) Error() string { return r.Detail }

// CallProvider calls one function of one kind the plugin provides, as the kind calls it -- the
// answer check included -- and returns what the kind would see: the checked answer under the
// contract's field names (a collect of a declared kind: its result, rows and state; a call that
// checked nothing: its result), and the call's error, its provider.ErrUnsupportedRead or its
// *scripthost.Failure. A live function is called as the service calls it, under the live cadence
// and reading only its live endpoints.
func (p *Plugin) CallProvider(ctx context.Context, session provider.Session, kind, function string,
	arguments []any) (map[string]any, error) {
	base, found := p.adapters[kind]
	if !found {
		return nil, &CallRefusal{Detail: fmt.Sprintf("the plugin provides no %s", kind)}
	}
	live := p.liveOf(kind, function)
	if kind == "measurement_sample" && (function == "sample" || live != nil) && len(arguments) >= 1 {
		if _, isView := arguments[0].(map[string]any); !isView {
			arguments[0] = DiscoveryView(provider.Discovery{})
		}
	}
	for index, argument := range arguments {
		if number, isNumber := argument.(int64); isNumber && !(kind == "measurement_sample" && index == 1) {
			arguments[index] = int(number)
		}
	}
	table := ""
	for _, declared := range base.contract.Functions {
		if declared.Name == function {
			table = declared.Returns
		}
	}
	if !provider.IsServiceKind(kind) {
		if _, core := provider.ContractOf(kind); !core {
			table = map[string]string{"probe": "probe_answer", "collect": "collect_answer"}[function]
		}
	}
	if live != nil {
		table = liveKinds[kind]
		base.invoker = liveInvoker{script: p.script, namespace: kind,
			maxAge: time.Duration(live.fallback) * time.Second, only: live.only}
	}
	if table == "" {
		return nil, &CallRefusal{Detail: fmt.Sprintf("the %s contract has no function %s", kind, function)}
	}
	switch {
	case provider.IsServiceKind(kind):
		checked, answerErr, failed := base.runService(ctx, session, function, table, arguments...)
		if failed != nil {
			return checked, failed
		}
		return checked, answerErr
	case function == "collect":
		state := map[string]string{}
		if len(arguments) > 0 {
			if given, isMap := arguments[0].(map[string]any); isMap {
				for key, value := range given {
					state[key] = fmt.Sprint(value)
				}
			}
		}
		declared := declaredKindAdapter{adapter: base, tables: map[string]plugindecl.PluginTable{}}
		for _, table := range p.OwnTables {
			declared.tables[table.Table] = table
		}
		pass, result, err := declared.Collect(ctx, session, state)
		return map[string]any{"result": resultMap(result), "rows": pass.Rows, "state": pass.State}, err
	default:
		checked, result, err := base.run(ctx, session, function, table, arguments...)
		if checked == nil {
			checked = map[string]any{"result": resultMap(result)}
		}
		return checked, err
	}
}

// resultMap is a provider.Result as a test reads it.
func resultMap(result provider.Result) map[string]any {
	return map[string]any{"state": string(result.State), "outcome": string(result.Outcome),
		"probe": result.Probe.Path, "detail": result.Detail, "separable": result.Separable}
}

// Provides reports whether the plugin provides a kind.
func (p *Plugin) Provides(kind string) bool {
	_, found := p.adapters[kind]
	return found
}

// ExportedForTests returns a value the plugin's script exports for its tests, by namespace and
// name. A plugin with no script has none.
func (p *Plugin) ExportedForTests(namespace, name string) (any, error) {
	if p.script == nil {
		return nil, errors.New("the plugin has no script")
	}
	return p.script.ExportedValue(namespace, name)
}

// PushSubscription is one push entry of the plugin, subscribed as the service subscribes it.
type PushSubscription struct {
	// Entry is the push entry's name in the manifest.
	Entry        string
	Subscription push.Subscription
}

// PushSubscriptions subscribes every push entry of the plugin into an environment.
func (p *Plugin) PushSubscriptions(environment PushEnvironment) []PushSubscription {
	subscriptions := make([]PushSubscription, 0, len(p.pushes))
	for _, binding := range p.pushes {
		subscriptions = append(subscriptions, PushSubscription{Entry: binding.declaration.Name,
			Subscription: pushSubscriptionOf(p, binding, environment)})
	}
	return subscriptions
}

// StoreAndRead stores one prepared batch through the sink a push subscription was given, as the
// service stores it, and returns what was stored as a test reads it: a table's rows with instants
// as epoch seconds and addresses as text, or a core kind's records under its contract's field
// names. A table sink with no store reads back without storing.
func StoreAndRead(ctx context.Context, sink push.Sink, prepared push.Prepared) ([]any, error) {
	if table, isTable := sink.(*tableSink); isTable && table.environment.Store != nil {
		if err := sink.Store(ctx, prepared); err != nil {
			return nil, err
		}
	}
	var rows []any
	switch payload := prepared.Payload.(type) {
	case tableBatch:
		for _, row := range payload.rows {
			rows = append(rows, readableRow(row))
		}
	case kindBatch:
		if kind, isKind := sink.(*kindSink); isKind {
			if err := sink.Store(ctx, prepared); err != nil {
				return nil, err
			}
			rows = append(rows, pushKinds[kind.binding.declaration.Kind].view(payload.records)...)
		}
	}
	return rows, nil
}

// readableRow is a stored row as a test reads it: instants as epoch seconds, addresses as text.
func readableRow(row map[string]any) map[string]any {
	converted := make(map[string]any, len(row))
	for name, value := range row {
		switch typed := value.(type) {
		case time.Time:
			converted[name] = epochSeconds(typed)
		case netip.Addr:
			converted[name] = typed.String()
		case net.HardwareAddr:
			converted[name] = typed.String()
		default:
			converted[name] = typed
		}
	}
	return converted
}
