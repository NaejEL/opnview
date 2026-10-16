package runtime

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/broker/mirror"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The firewall functions of the host, through the broker (A17 of the broker amendment to
// specs/SPEC-backend-completion-plugins.md).
//
// host.call, host.readObject, host.readCollection and host.serviceState go through the broker.
// A call may state max_age_seconds -- by default the endpoint's manifest default, then the kind's
// interval for a core kind, then the provider's cadence -- and an `after` cursor for a record
// stream. Its answer keeps every field it carried, and adds `value`, the observed answer whose
// reads are recorded field by field; `served_from`, `fetched_at` and `age_seconds`; and the gaps
// of a range read after a cursor. Reading `body` or calling `json()` reads the whole answer.

// callBroker reads the broker options of a call: max_age_seconds and after.
func (h *hostCalls) callBroker(vm *goja.Runtime, value goja.Value) (time.Duration, *float64) {
	maxAge := h.maxAge
	var after *float64
	object, isObject := value.(*goja.Object)
	if !isObject {
		return maxAge, after
	}
	if field := object.Get("max_age_seconds"); field != nil && !goja.IsUndefined(field) && !goja.IsNull(field) {
		seconds := field.ToFloat()
		if seconds < 0 {
			Throw(vm, "max_age_seconds is negative")
		}
		maxAge = time.Duration(seconds * float64(time.Second))
	}
	if field := object.Get("after"); field != nil && !goja.IsUndefined(field) && !goja.IsNull(field) {
		cursor := field.ToFloat()
		after = &cursor
	}
	return maxAge, after
}

// request asks for one answer: through the broker, or, where the session reaches none, through
// the session as it stands.
func (h *hostCalls) request(endpoint opnsense.Endpoint, options opnsense.RequestOptions, maxAge time.Duration,
	after *float64) (*broker.Answer, error) {
	if h.gate != nil {
		return h.gate.Call(h.ctx, broker.Request{Requester: h.requester, Endpoint: endpoint, Options: options,
			MaxAge: maxAge, After: after, Fresh: h.fresh[endpoint.Path]})
	}
	response, err := h.session.Call(h.ctx, endpoint, options)
	served := &broker.Answer{Response: response, Holding: answer.Everything, ServedFrom: broker.ServedFromFirewall,
		FetchedAt: h.session.Now()}
	if response.OK() {
		served.Value, _ = answer.Parse(response.Body)
	}
	return served, err
}

// record keeps an answer and what the script reads of it, for the report to the broker.
func (h *hostCalls) record(served *broker.Answer, maxAge time.Duration) *answer.Reads {
	reads := answer.NewReads()
	h.mutex.Lock()
	h.answers = append(h.answers, readAnswer{served: served, reads: reads, maxAge: maxAge})
	h.mutex.Unlock()
	return reads
}

// answerObject turns one answer into what a script reads.
func (h *hostCalls) answerObject(vm *goja.Runtime, name string, endpoint opnsense.Endpoint, served *broker.Answer,
	maxAge time.Duration, err error) goja.Value {
	result := vm.NewObject()
	if served == nil {
		served = &broker.Answer{Response: opnsense.Response{Endpoint: endpoint}}
	}
	response := served.Response
	_ = result.Set("endpoint", name)
	_ = result.Set("outcome", string(response.Outcome))
	_ = result.Set("ok", response.OK())
	_ = result.Set("status_code", response.StatusCode)
	_ = result.Set("detail", response.Detail)
	_ = result.Set("body_bytes", len(response.Body))
	_ = result.Set("availability_outcome", string(provider.ResponseOutcome(response)))
	_ = result.Set("served_from", served.ServedFrom)
	_ = result.Set("fetched_at", served.FetchedAt)
	_ = result.Set("age_seconds", served.AgeSeconds)
	_ = result.Set("gaps", vm.ToValue(gapsOf(served.Gaps)))
	if served.ReplacedBy != "" {
		_ = result.Set("replaced_by", served.ReplacedBy)
	} else {
		_ = result.Set("replaced_by", goja.Null())
	}
	var writeRefused *broker.ErrWriteRefused
	switch {
	case errors.As(err, &writeRefused):
		_ = result.Set("error", h.hostError(vm, err))
		_ = result.Set("refused", broker.CodeWriteRefused)
	case err != nil && response.Outcome == "":
		_ = result.Set("error", h.hostError(vm, err))
	default:
		_ = result.Set("error", goja.Null())
	}
	reads := h.record(served, maxAge)
	root := answer.Observe(served.Value, reads, served.Holding)
	whole := func() *answer.Value {
		value, wholeErr := root.Whole()
		if wholeErr != nil {
			h.notHeld(vm, endpoint.Path, wholeErr)
			return nil
		}
		return value
	}
	// Reading the body's text reads every field.
	_ = result.DefineAccessorProperty("body", vm.ToValue(func(goja.FunctionCall) goja.Value {
		if served.Value != nil && whole() == nil {
			return goja.Undefined()
		}
		return vm.ToValue(string(response.Body))
	}), nil, goja.FLAG_FALSE, goja.FLAG_TRUE)
	_ = result.Set("json", func(goja.FunctionCall) goja.Value {
		var decoded any
		if served.Value != nil {
			value := whole()
			if value == nil {
				return goja.Undefined()
			}
			return vm.ToValue(value.Plain())
		}
		if err := json.Unmarshal(response.Body, &decoded); err != nil {
			Throw(vm, "the answer of %s is not JSON: %v", name, err)
		}
		return vm.ToValue(decoded)
	})
	if served.Value != nil {
		_ = result.Set("value", h.observe(vm, root, endpoint.Path, false))
	} else {
		_ = result.Set("value", goja.Null())
	}
	// decodable says the body was JSON: a body that is the JSON null has the value null and is
	// decodable; a body that is not JSON has the value null and is not.
	_ = result.Set("decodable", served.Value != nil)
	return result
}

// gapsOf renders gaps for a script.
func gapsOf(gaps []mirror.Gap) []any {
	result := make([]any, 0, len(gaps))
	for _, gap := range gaps {
		result = append(result, map[string]any{"from": gap.From, "to": gap.To, "reason": gap.Reason})
	}
	return result
}

// setEvents sets a stream read's events: `events`, their text, which reads each event whole, and
// `values`, each event as an observed object whose fields read are recorded.
func (h *hostCalls) setEvents(vm *goja.Runtime, result *goja.Object, endpoint opnsense.Endpoint, events [][]byte) {
	reads := answer.NewReads()
	h.mutex.Lock()
	h.streamReads = append(h.streamReads, readStream{endpoint: endpoint, reads: reads})
	h.mutex.Unlock()
	texts := make([]any, 0, len(events))
	values := make([]any, 0, len(events))
	for _, event := range events {
		texts = append(texts, string(event))
		parsed, err := answer.Parse(event)
		if err != nil {
			values = append(values, nil)
			continue
		}
		values = append(values, h.observe(vm, answer.Observe(parsed, reads, nil), endpoint.Path, false))
	}
	_ = result.DefineAccessorProperty("events", vm.ToValue(func(goja.FunctionCall) goja.Value {
		reads.Whole("")
		return vm.ToValue(texts)
	}), nil, goja.FLAG_FALSE, goja.FLAG_TRUE)
	_ = result.Set("values", values)
}

// serviceState probes a service status endpoint, reading its status field.
func (h *hostCalls) serviceState(vm *goja.Runtime, endpoint opnsense.Endpoint) (bool, vocab.AvailabilityState,
	vocab.AvailabilityOutcome, string, error) {
	if h.gate == nil {
		return h.session.ServiceState(h.ctx, endpoint)
	}
	served, err := h.request(endpoint, opnsense.RequestOptions{}, h.maxAge, nil)
	if served == nil || (err != nil && served.Response.Outcome == "") {
		return false, vocab.StateUnavailable, vocab.OutcomeRequestNotMade, "", err
	}
	if !served.Response.OK() {
		return false, vocab.StateUnavailable, provider.ResponseOutcome(served.Response), served.Response.Detail, nil
	}
	if served.Value == nil || served.Value.Kind != answer.Object {
		return false, vocab.StateUnavailable, vocab.OutcomeUndecodable, "the status body could not be read", nil
	}
	root := answer.Observe(served.Value, h.record(served, h.maxAge), served.Holding)
	status, present, readErr := root.Get("status")
	if readErr != nil {
		h.notHeld(vm, endpoint.Path, readErr)
		return false, vocab.StateUnavailable, vocab.OutcomeRequestNotMade, "", nil
	}
	text, isText := "", false
	if present {
		text, isText = decode.String(decode.Object{"status": status.Peek().Plain()}, "status")
	}
	if !present || !isText {
		return false, vocab.StateUnavailable, vocab.OutcomeUndecodable, "the status body reported no status", nil
	}
	if decode.LowerASCII(text) == "running" {
		return true, vocab.StateReachable, vocab.OutcomeHealthy, "", nil
	}
	return false, vocab.StatePresentButDisabled, vocab.OutcomeServiceNotRunning,
		"the service reported " + provider.QuoteForDetail(text), nil
}

// readObject reads an endpoint's object whole.
// It also returns the instant the answer was fetched: the firewall's answer may be one the mirror held.
func (h *hostCalls) readObject(vm *goja.Runtime, endpoint opnsense.Endpoint) (decode.Object, int64, bool, error) {
	if h.gate == nil {
		object, ok, err := h.session.ReadObject(h.ctx, endpoint)
		return object, h.session.Now(), ok, err
	}
	served, err := h.request(endpoint, opnsense.RequestOptions{}, h.maxAge, nil)
	if served == nil || (err != nil && served.Response.Outcome == "") {
		return nil, 0, false, err
	}
	if !served.Response.OK() || served.Value == nil || served.Value.Kind != answer.Object {
		return nil, served.FetchedAt, false, nil
	}
	value, wholeErr := answer.Observe(served.Value, h.record(served, h.maxAge), served.Holding).Whole()
	if wholeErr != nil {
		h.notHeld(vm, endpoint.Path, wholeErr)
		return nil, served.FetchedAt, false, nil
	}
	object, _ := value.Plain().(map[string]any)
	return object, served.FetchedAt, true, nil
}

// readCollection reads an endpoint's rows whole.
func (h *hostCalls) readCollection(vm *goja.Runtime, endpoint opnsense.Endpoint, wrapperKeys ...string) (
	[]decode.Object, bool, error) {
	if h.gate == nil {
		return h.session.ReadCollection(h.ctx, endpoint, wrapperKeys...)
	}
	served, err := h.request(endpoint, opnsense.RequestOptions{}, h.maxAge, nil)
	if served == nil || (err != nil && served.Response.Outcome == "") {
		return nil, false, err
	}
	if !served.Response.OK() || served.Value == nil {
		return nil, false, nil
	}
	value, wholeErr := answer.Observe(served.Value, h.record(served, h.maxAge), served.Holding).Whole()
	if wholeErr != nil {
		h.notHeld(vm, endpoint.Path, wholeErr)
		return nil, false, nil
	}
	rows, ok := provider.CollectionRows(value.Plain(), value.Marshal(), wrapperKeys...)
	return rows, ok, nil
}
