package plugin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/push"
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
	serving, found := p.implementations[kind]
	if !found {
		return nil, &CallRefusal{Detail: fmt.Sprintf("the plugin provides no %s", kind)}
	}
	kindContract, core := provider.ContractOf(kind)
	if !core {
		kindContract = declaredContract(kind)
	}
	live := p.liveOf(kind, function)
	declared, known := kindContract.FunctionOf(function)
	if !known && live == nil {
		return nil, &CallRefusal{Detail: fmt.Sprintf("the %s contract has no function %s", kind, function)}
	}
	if live != nil {
		// A live function is called with the arguments of the function returning its answer.
		for _, candidate := range kindContract.Functions {
			if candidate.Returns == kindContract.LiveAnswer {
				declared = candidate
			}
		}
	}
	// Each argument is converted as the kind calls the function: `discovery` is the discovery view, a
	// test's own map kept; `state` is text by key; `now` stays an instant; any other whole number is
	// passed as JavaScript reads a page number.
	names := strings.Split(declared.Arguments, ", ")
	for index, argument := range arguments {
		name := ""
		if index+1 < len(names) {
			name = names[index+1]
		}
		switch {
		case name == "discovery":
			if _, isView := argument.(map[string]any); !isView {
				arguments[index] = provider.DiscoveryView(provider.Discovery{})
			}
		case name == "state":
			state := map[string]any{}
			if given, isMap := argument.(map[string]any); isMap {
				for key, value := range given {
					state[key] = fmt.Sprint(value)
				}
			}
			arguments[index] = state
		case name != "now":
			if number, isNumber := argument.(int64); isNumber {
				arguments[index] = int(number)
			}
		}
	}
	var (
		checked provider.Answer
		result  provider.Result
		err     error
	)
	if livePass, isLive := serving.(provider.LiveImplementation); isLive && live != nil {
		checked, result, err = livePass.CallLive(ctx, session, time.Duration(live.fallback)*time.Second, arguments...)
	} else {
		checked, result, err = serving.Call(ctx, session, function, arguments...)
	}
	if checked == nil {
		checked = map[string]any{"result": resultMap(result)}
	} else if !core && function == "collect" {
		checked["result"] = resultMap(result)
	}
	return checked, err
}

// resultMap is a provider.Result as a test reads it.
func resultMap(result provider.Result) map[string]any {
	return map[string]any{"state": string(result.State), "outcome": string(result.Outcome),
		"probe": result.Probe.Path, "detail": result.Detail, "separable": result.Separable}
}

// RunDerivation runs one of the plugin's derivations now, as a test runs it: woken as a cadence tick would
// wake it, over whatever was noted for it, and written as the collector writes it. It returns whether a run
// happened and how many rows it changed.
func (p *Plugin) RunDerivation(ctx context.Context, session provider.Session, name string, now int64) (
	provider.DerivationOutcome, error) {
	for _, d := range p.derivations {
		if d.declaration.Name != name {
			continue
		}
		d.Note(provider.DerivationChange{Trigger: provider.TriggerCadence, At: now})
		return d.Run(ctx, session, now)
	}
	return provider.DerivationOutcome{}, &CallRefusal{Detail: fmt.Sprintf("the plugin declares no derivation %s", name)}
}

// Provides reports whether the plugin provides a kind.
func (p *Plugin) Provides(kind string) bool {
	_, found := p.implementations[kind]
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
			rows = append(rows, ReadableRow(row))
		}
	case kindBatch:
		if kind, isKind := sink.(*kindSink); isKind {
			if err := sink.Store(ctx, prepared); err != nil {
				return nil, err
			}
			for _, record := range payload.records {
				rows = append(rows, contractView(kind.binding.declaration.Kind, record))
			}
		}
	}
	return rows, nil
}

// ReadableRow is a stored row as a test reads it: instants as epoch seconds, addresses as text.
func ReadableRow(row map[string]any) map[string]any {
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
