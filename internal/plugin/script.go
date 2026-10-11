package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/collect/dhcplease"
	"github.com/NaejEL/opnview/internal/collect/dnslookup"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// The JavaScript a manifest cannot express, run inside the binary.
//
// THE INTERPRETER IS goja (github.com/dop251/goja): a pure-Go ECMAScript 5.1 interpreter with
// most of ES2015 and later, an embedded library and not a process or a service, so one script
// runs on every architecture the binary is built for, with no compilation and no binary per
// platform (the maintainer's decision of 10 October 2026; docs/plugins.md, "Why JavaScript").
//
// EVERY CALL IS A FRESH RUNTIME. The program is compiled once, at load; each call of an exported
// function creates a runtime, runs the program so it defines its exports, and calls the one
// function. No state survives from one call to the next except what the host stores for the
// plugin, so a script cannot accumulate memory across passes, and a call that failed leaves
// nothing behind for the next one.
//
// WHAT THE SCRIPT IS GIVEN. Its first argument is the host object (hostObject below): the
// firewall endpoints the manifest declares, by name, and nothing else of opnview's -- no store,
// no discovery snapshot beyond what a kind hands it, no availability writer. The global
// `opnview` holds pure helpers: the field readers of internal/decode, which encode the measured
// facts about how OPNsense writes a field, and the normalisations a kind publishes for its
// implementations. There is no sandbox beyond that and none is claimed (Decisions of
// specs/SPEC-backend-completion-plugins.md): the script runs with the service's privileges, and
// its firewall calls go through opnview's client, whose refusal of mutating commands applies.
//
// FAILURES ARE ISOLATED, NEVER FATAL. A thrown error, a panic in a host function, a call that runs
// past CallTimeout and an answer the kind's contract refuses each end the call with a closed code
// (script_failed, script_timeout, records_invalid), which the kind records on the provider's
// availability row; nothing of that answer is stored, and the service and every other plugin carry
// on.

// DefaultCallTimeout bounds one call of a script function. The longest built-in call is the
// measurement sampler's: nine firewall reads, one of them a stream bounded at five seconds, each
// bounded by the client's thirty-second timeout in the worst case. Two minutes covers that with
// room and is still far below the shortest interval a pass runs at that would let two passes of
// one source overlap for long.
const DefaultCallTimeout = 2 * time.Minute

// Logger receives what a script logs, prefixed with its plugin's name. It writes to standard error
// by default; a test replaces it.
var Logger = func(plugin, message string) {
	fmt.Fprintf(os.Stderr, "opnview: plugin %s: %s\n", plugin, message)
}

// The failure codes of a call, which are availability outcomes.
const (
	failureScript  = store.OutcomeScriptFailed
	failureTimeout = store.OutcomeScriptTimeout
	failureRecords = store.OutcomeRecordsInvalid
)

// failure is a call that did not answer usably: its closed code and what happened.
type failure struct {
	code   store.AvailabilityOutcome
	detail string
	// cause is what ended the call, when it was not the script: a rerun's signal.
	cause error
}

func (f *failure) Error() string { return fmt.Sprintf("plugin: %s: %s", f.code, f.detail) }

// script is one plugin's compiled program and what its calls need.
type script struct {
	plugin    string
	file      string
	program   *goja.Program
	endpoints map[string]opnsense.Endpoint
	timeout   time.Duration
	// extra is what the script may reach beyond the firewall, nil for none.
	extra *extensions
	// version is the plugin's version, which the broker records reads under; cadences are the
	// cadences its providers of a kind of its own declare, the max age of their calls.
	version  string
	cadences map[string]time.Duration
	// observe is handed every host function's answer; nil but under `opnview plugin test`.
	observe func(function string, answer any)
	// routes are each endpoint's routes by version; versions and variants are the main script's
	// range and the script files of other ranges (A19, versions.go).
	routes   map[string][]route
	versions *VersionRange
	variants []scriptVariant
}

// compile compiles a plugin's script.
func compile(plugin, file, source string, endpoints map[string]opnsense.Endpoint, timeout time.Duration) (
	*script, error) {
	program, err := goja.Compile(file, source, false)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = DefaultCallTimeout
	}
	return &script{plugin: plugin, file: file, program: program, endpoints: endpoints, timeout: timeout}, nil
}

// exportsOf runs the program once, in a runtime of its own, and returns what it exports: the
// load-time check that the script defines what its kinds' contracts require.
func (s *script) exportsOf() (map[string]map[string]string, error) {
	vm, exports := s.newRuntime(nil, nil)
	if _, err := vm.RunProgram(s.program); err != nil {
		return nil, err
	}
	shape := map[string]map[string]string{}
	for _, name := range exports.Keys() {
		namespace := exports.Get(name)
		object, isObject := namespace.(*goja.Object)
		if !isObject {
			continue
		}
		members := map[string]string{}
		for _, member := range object.Keys() {
			value := object.Get(member)
			if _, callable := goja.AssertFunction(value); callable {
				members[member] = "function"
			} else {
				members[member] = "value"
			}
		}
		shape[name] = members
	}
	return shape, nil
}

// exportedValue returns a plain value a namespace exports, such as dns_lookup.page_bound.
func (s *script) exportedValue(namespace, name string) (any, error) {
	vm, exports := s.newRuntime(nil, nil)
	if _, err := vm.RunProgram(s.program); err != nil {
		return nil, err
	}
	object, isObject := exports.Get(namespace).(*goja.Object)
	if !isObject {
		return nil, fmt.Errorf("the script exports no %s", namespace)
	}
	value := object.Get(name)
	if value == nil || goja.IsUndefined(value) {
		return nil, fmt.Errorf("the script exports no %s.%s", namespace, name)
	}
	return value.Export(), nil
}

// call runs one exported function and returns its answer -- an object -- exported to Go values,
// with the host errors the answer may name.
func (s *script) call(ctx context.Context, session provider.Session, namespace, function string,
	arguments ...any) (any, []error, *failure) {
	return s.run(ctx, session, namespace, function, false, arguments...)
}

// callAny runs one exported function and returns whatever it returns, exported to Go values.
func (s *script) callAny(ctx context.Context, session provider.Session, namespace, function string,
	arguments ...any) (any, []error, *failure) {
	return s.run(ctx, session, namespace, function, true, arguments...)
}

// callLive runs a provider's live pass (live.go): its requests carry the live cadence as their
// max age, and it may read only the endpoints its declaration names.
func (s *script) callLive(ctx context.Context, session provider.Session, namespace, function string,
	maxAge time.Duration, only map[string]bool, arguments ...any) (any, []error, *failure) {
	return s.runWith(ctx, session, namespace, function, runOptions{maxAge: maxAge, only: only}, arguments...)
}

// runOptions are what a live pass changes of a call: its max age and the endpoints it may read.
type runOptions struct {
	plain  bool
	maxAge time.Duration
	only   map[string]bool
}

// maxRuns bounds how many times one call runs: once, and once more per endpoint whose answer the
// mirror did not hold a field of.
const maxRuns = 8

// run is call and callAny. A call whose script read a field the mirror did not hold is run again,
// that endpoint answered from the firewall, as many times as it takes and no more than maxRuns: a
// call's records are kept only once it returns, so a rerun stores nothing twice (A10, "No missing
// field because of the mirror").
func (s *script) run(ctx context.Context, session provider.Session, namespace, function string, plain bool,
	arguments ...any) (any, []error, *failure) {
	return s.runWith(ctx, session, namespace, function, runOptions{plain: plain}, arguments...)
}

// runWith is run with the options of a live pass.
func (s *script) runWith(ctx context.Context, session provider.Session, namespace, function string,
	options runOptions, arguments ...any) (any, []error, *failure) {
	plain := options.plain
	requester, maxAge, gate := s.requesterOf(session, namespace, function)
	if options.maxAge > 0 {
		maxAge = options.maxAge
	}
	if scoped, scopable := session.(interface {
		For(string, time.Duration) *provider.Host
	}); scopable {
		session = scoped.For(requester.Name, maxAge)
	}
	version, known := gate.FirewallVersion()
	program, unsupported := s.forVersion(version, known)
	if unsupported != "" {
		return nil, nil, &failure{code: failureVersionUnsupported, detail: unsupported}
	}
	fresh := map[string]bool{}
	for attempt := 0; ; attempt++ {
		calls := &hostCalls{session: session, requester: requester, maxAge: maxAge, gate: gate, fresh: fresh,
			program: program, only: options.only}
		answer, hostErrors, failed := s.runOnce(ctx, calls, namespace, function, plain, arguments...)
		var rerun *rerunSignal
		if failed != nil && errors.As(failed.cause, &rerun) {
			if attempt+1 < maxRuns && !fresh[rerun.endpoint] {
				fresh[rerun.endpoint] = true
				continue
			}
			return nil, nil, &failure{code: failureScript, detail: fmt.Sprintf("%s.%s kept reading %s, which the "+
				"mirror does not hold, after its answer came from the firewall", namespace, function, rerun.path)}
		}
		if failed == nil && gate != nil {
			calls.report(ctx)
		}
		return answer, hostErrors, failed
	}
}

// requesterOf is who a call asks as, how old an answer it accepts, and the broker it asks: the
// plugin and its version; the probe round's max age for a probe, the kind's interval for a core
// kind, the provider's cadence for a kind of the plugin's own.
func (s *script) requesterOf(session provider.Session, namespace, function string) (broker.Requester,
	time.Duration, *broker.Broker) {
	requester := broker.Requester{Name: s.plugin, Version: s.version}
	maxAge := s.cadences[namespace]
	if reader, reads := session.(interface {
		KindMaxAge(kind, function string) time.Duration
	}); reads {
		if kindMaxAge := reader.KindMaxAge(namespace, function); kindMaxAge > 0 && (maxAge == 0 || function == "probe") {
			maxAge = kindMaxAge
		}
	}
	var gate *broker.Broker
	if host, hosted := session.(interface{ Gate() provider.Gate }); hosted {
		gate, _ = host.Gate().(*broker.Broker)
	}
	return requester, maxAge, gate
}

// report tells the broker what the call read of each answer and each stream.
func (h *hostCalls) report(ctx context.Context) {
	h.mutex.Lock()
	answers, streams := h.answers, h.streamReads
	h.mutex.Unlock()
	for _, read := range answers {
		h.gate.Done(ctx, read.served, h.requester, read.maxAge, read.reads)
	}
	for _, read := range streams {
		h.gate.StreamDone(ctx, h.requester, read.endpoint, opnsense.RequestOptions{}, read.reads)
	}
}

// runOnce runs a call once.
func (s *script) runOnce(ctx context.Context, calls *hostCalls, namespace, function string, plain bool,
	arguments ...any) (answer any, hostErrors []error, failed *failure) {
	deadline := time.Now().Add(s.timeout)
	callCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	calls.ctx = callCtx
	session := calls.session
	vm, exports := s.newRuntime(calls, session)

	// The interrupt is what ends a script that never returns; the call's context is what ends
	// a host function waiting on the firewall. Both fire at the same deadline.
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-callCtx.Done():
			vm.Interrupt(errCallTimeout)
		case <-finished:
		}
	}()

	defer func() {
		if recovered := recover(); recovered != nil {
			if signal, unsupported := recovered.(*versionSignal); unsupported {
				answer, hostErrors = nil, nil
				failed = &failure{code: failureVersionUnsupported, detail: signal.detail, cause: signal}
				return
			}
			answer, hostErrors = nil, nil
			failed = &failure{code: failureScript, detail: fmt.Sprintf("%s.%s panicked: %v", namespace, function,
				recovered)}
		}
	}()

	calls.exports = exports
	if _, err := vm.RunProgram(calls.program); err != nil {
		return nil, nil, s.runFailure(namespace, function, err)
	}
	object, isObject := exports.Get(namespace).(*goja.Object)
	if !isObject {
		return nil, nil, &failure{code: failureScript, detail: fmt.Sprintf("the script exports no %s", namespace)}
	}
	callable, isFunction := goja.AssertFunction(object.Get(function))
	if !isFunction {
		return nil, nil, &failure{code: failureScript, detail: fmt.Sprintf("the script exports no function %s.%s",
			namespace, function)}
	}
	// A kind's function is handed the host first; a helper a plugin exports for its own tests is
	// handed its arguments alone.
	var values []goja.Value
	if !plain {
		host := calls.hostObject(vm, s)
		calls.extend(vm, host, s.extra)
		if s.observe != nil {
			calls.observed(vm, host, s.observe)
		}
		values = append(values, host)
	}
	for _, argument := range arguments {
		values = append(values, vm.ToValue(argument))
	}
	result, err := callable(goja.Undefined(), values...)
	if err != nil {
		return nil, nil, s.runFailure(namespace, function, err)
	}
	if plain {
		if result == nil || goja.IsUndefined(result) {
			return nil, calls.errors(), nil
		}
		return result.Export(), calls.errors(), nil
	}
	exported, convertErr := exportAnswer(vm, result, calls)
	if convertErr != nil {
		return nil, nil, &failure{code: failureRecords, detail: convertErr.Error()}
	}
	return exported, calls.errors(), nil
}

// errCallTimeout is the value a call is interrupted with at its deadline.
var errCallTimeout = errors.New("the call ran past its time bound")

// runFailure names what ended a call.
func (s *script) runFailure(namespace, function string, err error) *failure {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		if signal, isRerun := interrupted.Value().(*rerunSignal); isRerun {
			return &failure{code: failureScript, detail: signal.Error(), cause: signal}
		}
		return &failure{code: failureTimeout, detail: fmt.Sprintf("%s.%s ran past %s and was stopped", namespace,
			function, s.timeout)}
	}
	var exception *goja.Exception
	if errors.As(err, &exception) {
		return &failure{code: failureScript, detail: fmt.Sprintf("%s.%s threw: %s", namespace, function,
			strings.TrimSpace(exception.Error()))}
	}
	return &failure{code: failureScript, detail: fmt.Sprintf("%s.%s failed: %v", namespace, function, err)}
}

// newRuntime creates a runtime with the globals every script sees: exports, module, console and
// opnview. calls is nil for the load-time check, which has no host.
func (s *script) newRuntime(calls *hostCalls, session provider.Session) (*goja.Runtime, *goja.Object) {
	vm := goja.New()
	exports := vm.NewObject()
	module := vm.NewObject()
	_ = module.Set("exports", exports)
	_ = vm.Set("exports", exports)
	_ = vm.Set("module", module)
	console := vm.NewObject()
	_ = console.Set("log", func(call goja.FunctionCall) goja.Value {
		parts := make([]string, 0, len(call.Arguments))
		for _, argument := range call.Arguments {
			parts = append(parts, argument.String())
		}
		Logger(s.plugin, strings.Join(parts, " "))
		return goja.Undefined()
	})
	_ = vm.Set("console", console)
	_ = vm.Set("opnview", helpers(vm))
	return vm, exports
}

// hostCalls is one call's host: its context, its session and the host errors its functions met,
// which a script hands back by reference.
type hostCalls struct {
	ctx     context.Context
	session provider.Session

	// requester is the plugin, by name and version, as the broker knows it; maxAge is how old an
	// answer its calls accept unless a call says otherwise; gate is the broker, nil where the
	// session reaches none.
	requester broker.Requester
	maxAge    time.Duration
	gate      *broker.Broker
	// fresh are the endpoints a rerun answers from the firewall: the script read a field of their
	// answer the mirror did not hold.
	fresh map[string]bool

	mutex      sync.Mutex
	hostErrors []error
	// answers are the answers the call read, with what it read of each, reported to the broker
	// when the call returns.
	answers []readAnswer
	// streamReads are what it read of each stream's events.
	streamReads []readStream
	// program is the script file that runs on the firewall's version; exports is what it
	// exports, for a route's adapter; renames are the field-name mappings of the routes called,
	// by the route's path (versions.go).
	program  *goja.Program
	exports  *goja.Object
	renames  map[string]map[string]string
	adapters map[string]string
	// only, when not nil, is every endpoint the call may read: a live pass reads only the live
	// endpoints its declaration names (live.go).
	only map[string]bool
}

// readAnswer is one answer and what the script read of it.
type readAnswer struct {
	served *broker.Answer
	reads  *answer.Reads
	maxAge time.Duration
}

// readStream is one stream and what the script read of its events.
type readStream struct {
	endpoint opnsense.Endpoint
	reads    *answer.Reads
}

// hostError keeps a Go error and returns the JS value that names it: {message, host_error}. A
// script returns that value as its answer's `error`, and the kind then reports the very error the
// host met -- an absent base URL, a cancelled pass -- rather than its text.
func (h *hostCalls) hostError(vm *goja.Runtime, err error) goja.Value {
	if err == nil {
		return goja.Null()
	}
	h.mutex.Lock()
	h.hostErrors = append(h.hostErrors, err)
	index := len(h.hostErrors) - 1
	h.mutex.Unlock()
	object := vm.NewObject()
	_ = object.Set("message", err.Error())
	_ = object.Set("host_error", index)
	return object
}

func (h *hostCalls) errors() []error {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return append([]error(nil), h.hostErrors...)
}

// throw raises a JS TypeError for a script that called a host function wrongly: a programming
// error of the script, which ends the call as script_failed.
func throw(vm *goja.Runtime, format string, arguments ...any) {
	panic(vm.NewTypeError(fmt.Sprintf(format, arguments...)))
}

// hostObject is the first argument of every call: the firewall, by the endpoint names the
// manifest declares, and the clock.
func (h *hostCalls) hostObject(vm *goja.Runtime, s *script) *goja.Object {
	host := vm.NewObject()
	endpoint := func(call goja.FunctionCall) opnsense.Endpoint {
		name := call.Argument(0).String()
		declared, found := s.endpoints[name]
		if !found {
			throw(vm, "the endpoint %q is not declared in the plugin's manifest", name)
		}
		if h.only != nil && !h.only[name] {
			throw(vm, "the live pass reads only the endpoints its declaration names, and %q is not one", name)
		}
		return h.routeOf(s, name, declared).endpoint
	}
	h.firewall(vm, host)

	_ = host.Set("call", func(call goja.FunctionCall) goja.Value {
		declared := endpoint(call)
		options, err := requestOptions(vm, call.Argument(1))
		if err != nil {
			throw(vm, "the options of a call to %s: %v", declared.Path, err)
		}
		maxAge, after := h.callBroker(vm, call.Argument(1))
		served, err := h.request(declared, options, maxAge, after)
		return h.adapt(vm, call.Argument(0).String(), h.answerObject(vm, call.Argument(0).String(), declared, served, maxAge, err))
	})
	_ = host.Set("stream", func(call goja.FunctionCall) goja.Value {
		declared := endpoint(call)
		bound := opnsense.StreamBound{Events: 1, Within: time.Second}
		if object, isObject := call.Argument(1).(*goja.Object); isObject {
			if events := object.Get("events"); events != nil && !goja.IsUndefined(events) {
				bound.Events = int(events.ToInteger())
			}
			if within := object.Get("within_seconds"); within != nil && !goja.IsUndefined(within) {
				bound.Within = time.Duration(within.ToFloat() * float64(time.Second))
			}
		}
		response, err := h.session.Stream(h.ctx, declared, bound)
		result := vm.NewObject()
		_ = result.Set("endpoint", call.Argument(0).String())
		_ = result.Set("outcome", string(response.Outcome))
		_ = result.Set("status_code", response.StatusCode)
		_ = result.Set("detail", response.Detail)
		_ = result.Set("ended_by", string(response.EndedBy))
		h.setEvents(vm, result, declared, response.Events)
		if err != nil && response.Outcome == "" {
			_ = result.Set("error", h.hostError(vm, err))
		} else {
			_ = result.Set("error", goja.Null())
		}
		return result
	})
	_ = host.Set("events", func(call goja.FunctionCall) goja.Value {
		declared := endpoint(call)
		result := vm.NewObject()
		if h.gate == nil {
			_ = result.Set("error", h.hostError(vm, errors.New("plugin: no broker holds this stream")))
			return result
		}
		after := int64(0)
		if cursor := call.Argument(1); !goja.IsUndefined(cursor) && !goja.IsNull(cursor) {
			after = cursor.ToInteger()
		}
		events, connected := h.gate.EventsAfter(h.requester, declared, opnsense.RequestOptions{}, after)
		var data [][]byte
		cursor := after
		for _, event := range events {
			data = append(data, event.Data)
			cursor = event.Sequence
		}
		h.setEvents(vm, result, declared, data)
		_ = result.Set("cursor", cursor)
		_ = result.Set("connected", connected)
		from := h.session.Now() - 86400
		if len(events) > 0 {
			from = events[0].ReceivedAt
		}
		gaps, err := h.gate.StreamGaps(h.ctx, declared, opnsense.RequestOptions{}, from, h.session.Now())
		_ = result.Set("gaps", vm.ToValue(gapsOf(gaps)))
		_ = result.Set("error", h.hostError(vm, err))
		return result
	})
	_ = host.Set("serviceState", func(call goja.FunctionCall) goja.Value {
		declared := endpoint(call)
		running, state, outcome, detail, err := h.serviceState(vm, declared)
		result := vm.NewObject()
		_ = result.Set("running", running)
		_ = result.Set("state", string(state))
		_ = result.Set("outcome", string(outcome))
		_ = result.Set("detail", detail)
		_ = result.Set("error", h.hostError(vm, err))
		return result
	})
	_ = host.Set("readObject", func(call goja.FunctionCall) goja.Value {
		declared := endpoint(call)
		object, fetchedAt, ok, err := h.readObject(vm, declared)
		result := vm.NewObject()
		if ok {
			_ = result.Set("object", vm.ToValue(map[string]any(object)))
		} else {
			_ = result.Set("object", goja.Null())
		}
		_ = result.Set("ok", ok)
		_ = result.Set("fetched_at", fetchedAt)
		_ = result.Set("error", h.hostError(vm, err))
		return result
	})
	_ = host.Set("readCollection", func(call goja.FunctionCall) goja.Value {
		declared := endpoint(call)
		var wrappers []string
		for _, argument := range call.Arguments[1:] {
			wrappers = append(wrappers, argument.String())
		}
		rows, ok, err := h.readCollection(vm, declared, wrappers...)
		result := vm.NewObject()
		_ = result.Set("rows", vm.ToValue(objectsToAny(rows)))
		_ = result.Set("ok", ok)
		_ = result.Set("error", h.hostError(vm, err))
		return result
	})
	_ = host.Set("catalogue", func(goja.FunctionCall) goja.Value {
		if h.gate == nil {
			return goja.Null()
		}
		catalogue, err := h.gate.Catalogue(h.ctx)
		if err != nil {
			return goja.Null()
		}
		encoded, _ := json.Marshal(catalogue)
		var plain any
		_ = json.Unmarshal(encoded, &plain)
		return vm.ToValue(plain)
	})
	_ = host.Set("normaliser", func(goja.FunctionCall) goja.Value {
		normaliser, err := h.session.Normaliser(h.ctx)
		result := vm.NewObject()
		_ = result.Set("error", h.hostError(vm, err))
		reading := func(read func(string) (int64, error)) func(goja.FunctionCall) goja.Value {
			return func(call goja.FunctionCall) goja.Value {
				value, err := read(call.Argument(0).String())
				answer := vm.NewObject()
				if err != nil {
					_ = answer.Set("value", goja.Null())
					_ = answer.Set("error", err.Error())
				} else {
					_ = answer.Set("value", value)
					_ = answer.Set("error", goja.Null())
				}
				return answer
			}
		}
		_ = result.Set("filterLog", reading(normaliser.NormaliseFilterLogTimestamp))
		_ = result.Set("iso", reading(normaliser.NormaliseISO))
		_ = result.Set("syslog", reading(normaliser.NormaliseSyslog))
		return result
	})
	_ = host.Set("now", func(goja.FunctionCall) goja.Value { return vm.ToValue(h.session.Now()) })
	_ = host.Set("pagination", func(call goja.FunctionCall) goja.Value {
		form := opnsense.Pagination(int(call.Argument(0).ToInteger()), int(call.Argument(1).ToInteger()))
		object := vm.NewObject()
		for key, values := range form {
			_ = object.Set(key, values[0])
		}
		return object
	})
	_ = host.Set("log", func(call goja.FunctionCall) goja.Value {
		Logger(s.plugin, call.Argument(0).String())
		return goja.Undefined()
	})
	return host
}

// requestOptions reads a call's options object: arguments, query, form and json.
func requestOptions(vm *goja.Runtime, value goja.Value) (opnsense.RequestOptions, error) {
	var options opnsense.RequestOptions
	object, isObject := value.(*goja.Object)
	if !isObject {
		return options, nil
	}
	if arguments := object.Get("arguments"); arguments != nil && !goja.IsUndefined(arguments) && !goja.IsNull(arguments) {
		list, isList := arguments.Export().([]any)
		if !isList {
			return options, errors.New("arguments is not a list")
		}
		for _, argument := range list {
			text, isText := argument.(string)
			if !isText {
				return options, errors.New("an argument is not a string")
			}
			options.Arguments = append(options.Arguments, text)
		}
	}
	values := func(key string) (url.Values, error) {
		field := object.Get(key)
		if field == nil || goja.IsUndefined(field) || goja.IsNull(field) {
			return nil, nil
		}
		exported, isMap := field.Export().(map[string]any)
		if !isMap {
			return nil, fmt.Errorf("%s is not an object", key)
		}
		result := url.Values{}
		for name, raw := range exported {
			switch typed := raw.(type) {
			case []any:
				for _, element := range typed {
					result.Add(name, fmt.Sprint(element))
				}
			default:
				result.Set(name, fmt.Sprint(typed))
			}
		}
		return result, nil
	}
	var err error
	if options.Query, err = values("query"); err != nil {
		return options, err
	}
	if options.Form, err = values("form"); err != nil {
		return options, err
	}
	if body := object.Get("json"); body != nil && !goja.IsUndefined(body) {
		options.JSON = body.Export()
	}
	_ = vm
	return options, nil
}

// objectsToAny turns decoded rows into a list a script iterates.
func objectsToAny(rows []decode.Object) []any {
	list := make([]any, 0, len(rows))
	for _, row := range rows {
		list = append(list, map[string]any(row))
	}
	return list
}

// exportAnswer exports a function's answer to Go values. The answer is an object; its `error`,
// when present, is either a host error the call's host kept, or the text of whatever else the
// script put there.
func exportAnswer(vm *goja.Runtime, value goja.Value, calls *hostCalls) (any, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, errors.New("the function returned nothing; it returns an object holding at least `result`")
	}
	object, isObject := value.(*goja.Object)
	if !isObject {
		return nil, fmt.Errorf("the function returned %s, not an object", value.String())
	}
	exported, isMap := object.Export().(map[string]any)
	if !isMap {
		return nil, errors.New("the function's answer is not an object")
	}
	answer := make(map[string]any, len(exported))
	for key, field := range exported {
		answer[key] = plainExport(field)
	}
	if errValue := object.Get("error"); errValue != nil && !goja.IsUndefined(errValue) && !goja.IsNull(errValue) {
		answer["error"] = errorOf(errValue, calls)
	}
	return answer, nil
}

// errorOf turns the `error` of an answer into a Go error: the very host error a {message,
// host_error} value names, or the text of anything else.
func errorOf(value goja.Value, calls *hostCalls) error {
	if object, isObject := value.(*goja.Object); isObject {
		if index := object.Get("host_error"); index != nil && !goja.IsUndefined(index) {
			hostErrors := calls.errors()
			if position := int(index.ToInteger()); position >= 0 && position < len(hostErrors) {
				return hostErrors[position]
			}
		}
		if message := object.Get("message"); message != nil && !goja.IsUndefined(message) {
			return errors.New(message.String())
		}
	}
	return errors.New(value.String())
}

// helpers is the global `opnview`: the field readers of internal/decode and the normalisations
// the kinds publish for their implementations. Each is the Go function itself, so a script reads
// a field exactly as the core does.
func helpers(vm *goja.Runtime) *goja.Object {
	root := vm.NewObject()
	readers := vm.NewObject()
	// objectArgument is a helper's object argument as internal/decode reads it. An observed answer is
	// read for the keys the helper reads and no other, so the mirror stores exactly those; with no key
	// named, the object is read whole.
	objectArgument := func(call goja.FunctionCall, index int, keys ...string) decode.Object {
		if observed, endpoint, calls, isObserved := observedOf(call.Argument(index)); isObserved {
			return calls.fieldsOf(vm, observed, endpoint, keys)
		}
		object, _ := jsonShape(call.Argument(index).Export()).(map[string]any)
		return object
	}
	key := func(call goja.FunctionCall) string { return call.Argument(1).String() }
	optional := func(value any, present bool) goja.Value {
		if !present {
			return goja.Null()
		}
		return vm.ToValue(value)
	}
	pointer := func(value any) goja.Value {
		switch typed := value.(type) {
		case *string:
			if typed == nil {
				return goja.Null()
			}
			return vm.ToValue(*typed)
		case *int64:
			if typed == nil {
				return goja.Null()
			}
			return vm.ToValue(*typed)
		case *bool:
			if typed == nil {
				return goja.Null()
			}
			return vm.ToValue(*typed)
		}
		return goja.Null()
	}
	path := func(call goja.FunctionCall, from int) []string {
		var steps []string
		for _, argument := range call.Arguments[from:] {
			if list, isList := argument.Export().([]any); isList {
				for _, step := range list {
					steps = append(steps, fmt.Sprint(step))
				}
				continue
			}
			steps = append(steps, argument.String())
		}
		return steps
	}
	set := func(object *goja.Object, name string, function func(goja.FunctionCall) goja.Value) {
		_ = object.Set(name, function)
	}
	set(readers, "string", func(call goja.FunctionCall) goja.Value {
		value, present := decode.String(objectArgument(call, 0, key(call)), key(call))
		return optional(value, present)
	})
	set(readers, "rawString", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(decode.RawString(objectArgument(call, 0, key(call)), key(call)))
	})
	set(readers, "int", func(call goja.FunctionCall) goja.Value {
		value, present := decode.Int(objectArgument(call, 0, key(call)), key(call))
		return optional(value, present)
	})
	set(readers, "float", func(call goja.FunctionCall) goja.Value {
		value, present := decode.Float(objectArgument(call, 0, key(call)), key(call))
		return optional(value, present)
	})
	set(readers, "firstFloat", func(call goja.FunctionCall) goja.Value {
		value, from, present := decode.FirstFloat(objectArgument(call, 0, path(call, 1)...), path(call, 1)...)
		if !present {
			return goja.Null()
		}
		answer := vm.NewObject()
		_ = answer.Set("value", value)
		_ = answer.Set("key", from)
		return answer
	})
	set(readers, "flag", func(call goja.FunctionCall) goja.Value {
		return pointer(decode.Flag(objectArgument(call, 0, key(call)), key(call)))
	})
	set(readers, "epoch", func(call goja.FunctionCall) goja.Value {
		value, present := decode.Epoch(objectArgument(call, 0, key(call)), key(call))
		return optional(value, present)
	})
	set(readers, "epochPointer", func(call goja.FunctionCall) goja.Value {
		return pointer(decode.EpochPointer(objectArgument(call, 0, key(call)), key(call)))
	})
	set(readers, "port", func(call goja.FunctionCall) goja.Value {
		return pointer(decode.Port(objectArgument(call, 0, key(call)), key(call)))
	})
	set(readers, "textField", func(call goja.FunctionCall) goja.Value {
		text, shape := decode.TextField(objectArgument(call, 0, key(call)), key(call))
		answer := vm.NewObject()
		_ = answer.Set("text", text)
		_ = answer.Set("shape", shapeName(shape))
		return answer
	})
	set(readers, "nested", func(call goja.FunctionCall) goja.Value {
		if observed, endpoint, calls, isObserved := observedOf(call.Argument(0)); isObserved {
			return calls.nestedOf(vm, observed, endpoint, path(call, 1))
		}
		object, present := decode.Nested(objectArgument(call, 0), path(call, 1)...)
		if !present {
			return goja.Null()
		}
		return vm.ToValue(map[string]any(object))
	})
	set(readers, "nestedString", func(call goja.FunctionCall) goja.Value {
		value, present := decode.NestedString(nestedArgument(vm, call, path(call, 1), objectArgument), path(call, 1)...)
		return optional(value, present)
	})
	set(readers, "nestedFlag", func(call goja.FunctionCall) goja.Value {
		return pointer(decode.NestedFlag(nestedArgument(vm, call, path(call, 1), objectArgument), path(call, 1)...))
	})
	set(readers, "objects", func(call goja.FunctionCall) goja.Value {
		if observed, endpoint, calls, isObserved := observedOf(call.Argument(0)); isObserved {
			return calls.objectsOf(vm, observed, endpoint)
		}
		return vm.ToValue(objectsToAny(decode.Objects(jsonShape(call.Argument(0).Export()))))
	})
	set(readers, "rows", func(call goja.FunctionCall) goja.Value {
		if observed, endpoint, calls, isObserved := observedOf(call.Argument(0)); isObserved {
			return calls.rowsOf(vm, observed, endpoint)
		}
		rows, err := decode.Rows([]byte(call.Argument(0).String()))
		if err != nil {
			return goja.Null()
		}
		return vm.ToValue(objectsToAny(rows))
	})
	set(readers, "nonEmpty", func(call goja.FunctionCall) goja.Value {
		if observed, endpoint, calls, isObserved := observedOf(call.Argument(0)); isObserved {
			return vm.ToValue(calls.nonEmpty(vm, observed, endpoint))
		}
		return vm.ToValue(decode.NonEmpty(jsonShape(call.Argument(0).Export())))
	})
	set(readers, "lower", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(decode.LowerASCII(call.Argument(0).String()))
	})
	set(readers, "uptimeSeconds", func(call goja.FunctionCall) goja.Value {
		seconds, err := decode.UptimeSeconds(call.Argument(0).String())
		return optional(seconds, err == nil)
	})
	set(readers, "firstLoadAverage", func(call goja.FunctionCall) goja.Value {
		value, err := decode.FirstLoadAverage(call.Argument(0).String())
		return optional(value, err == nil)
	})
	set(readers, "parseFloat", func(call goja.FunctionCall) goja.Value {
		value, err := strconv.ParseFloat(call.Argument(0).String(), 64)
		return optional(value, err == nil)
	})
	_ = root.Set("decode", readers)

	set(root, "sha256Hex", func(call goja.FunctionCall) goja.Value {
		sum := sha256.Sum256([]byte(call.Argument(0).String()))
		return vm.ToValue(hex.EncodeToString(sum[:]))
	})
	set(root, "base64", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(base64.StdEncoding.EncodeToString([]byte(call.Argument(0).String())))
	})
	set(root, "quoteForDetail", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(provider.QuoteForDetail(call.Argument(0).String()))
	})
	_ = root.Set("authenticationFailed", provider.AuthenticationFailed)

	leases := vm.NewObject()
	set(leases, "decodeLease", func(call goja.FunctionCall) goja.Value {
		observation, ok := dhcplease.DecodeLease(objectArgument(call, 0))
		if !ok {
			return goja.Null()
		}
		return vm.ToValue(leaseRecord(observation))
	})
	set(leases, "normaliseMAC", func(call goja.FunctionCall) goja.Value {
		return pointer(dhcplease.MACPointer(call.Argument(0).String()))
	})
	_ = root.Set("dhcp_lease", leases)

	lookups := vm.NewObject()
	set(lookups, "normaliseAction", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(dnslookup.NormaliseAction(call.Argument(0).String()))
	})
	_ = root.Set("dns_lookup", lookups)

	samples := vm.NewObject()
	set(samples, "interfaceEndpointKey", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(store.InterfaceEndpointKey(call.Argument(0).String(), call.Argument(1).String()))
	})
	set(samples, "interfaceEndpointPairKey", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(store.InterfaceEndpointPairKey(call.Argument(0).String(), call.Argument(1).String(),
			call.Argument(2).String()))
	})
	_ = samples.Set("sampleSeconds", store.SampleSeconds)
	_ = root.Set("measurement_sample", samples)
	return root
}

// leaseRecord is a decoded lease as a script reads it: the dhcp_lease_record fields.
func leaseRecord(observation dhcplease.Observation) map[string]any {
	text := func(value *string) any {
		if value == nil {
			return nil
		}
		return *value
	}
	number := func(value *int64) any {
		if value == nil {
			return nil
		}
		return *value
	}
	return map[string]any{
		"address": observation.Address, "mac": text(observation.MAC), "hostname": text(observation.Hostname),
		"dhcp_client_id": text(observation.DHCPClientID), "duid": text(observation.DUID),
		"iaid": text(observation.IAID), "vendor_hint": text(observation.VendorHint),
		"lease_state": observation.LeaseState, "interface_identifier": observation.InterfaceIdentifier,
		"interface_device": observation.InterfaceDevice, "starts_at": number(observation.StartsAt),
		"expires_at": number(observation.ExpiresAt),
	}
}

// shapeName is a field shape as a script reads it.
func shapeName(shape decode.Shape) string {
	switch shape {
	case decode.ShapeNull:
		return "null"
	case decode.ShapeString:
		return "string"
	case decode.ShapeOther:
		return "other"
	default:
		return "absent"
	}
}

// shapeOf is the decode.Shape of a script's ttl_shape.
func shapeOf(name string) (decode.Shape, bool) {
	switch name {
	case "absent":
		return decode.ShapeAbsent, true
	case "null":
		return decode.ShapeNull, true
	case "string":
		return decode.ShapeString, true
	case "other":
		return decode.ShapeOther, true
	}
	return decode.ShapeAbsent, false
}

// jsonShape returns a value as encoding/json decodes it -- every number a float64 -- which is
// the shape internal/decode reads. A value the firewall answered is already in that shape and is
// returned as it is; a value a script built may hold integers, which are converted.
func jsonShape(value any) any {
	if !needsJSONShape(value) {
		return value
	}
	switch typed := value.(type) {
	case map[string]any:
		converted := make(map[string]any, len(typed))
		for key, element := range typed {
			converted[key] = jsonShape(element)
		}
		return converted
	case []any:
		converted := make([]any, len(typed))
		for index, element := range typed {
			converted[index] = jsonShape(element)
		}
		return converted
	case int64:
		return float64(typed)
	case int:
		return float64(typed)
	case int32:
		return float64(typed)
	}
	return value
}

// needsJSONShape reports whether a value holds a number that is not a float64.
func needsJSONShape(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, element := range typed {
			if needsJSONShape(element) {
				return true
			}
		}
	case []any:
		for _, element := range typed {
			if needsJSONShape(element) {
				return true
			}
		}
	case int64, int, int32:
		return true
	}
	return false
}

// sortedKeys returns a map's keys, sorted.
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
