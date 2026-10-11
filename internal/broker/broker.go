// Package broker is the only path from opnview to the firewall API (A9 to A18 of the broker
// amendment to specs/SPEC-backend-completion-plugins.md).
//
// EVERY REQUEST COMES HERE: from the plugins, from discovery, from the probe round, from the core
// coordinators. The broker applies the read-only rule (readonly.go); answers from the mirror when
// the mirror holds an answer young enough for the requester's max age, and otherwise queues one
// call with the scheduler; hands that one answer to every requester waiting for it; records what
// each requester read of it; and stores the fields read in the mirror. It is the only caller of
// the client's Call, Stream and Listen. The one exception is the settings surface's check of
// candidate credentials (internal/web/surfaces/settings/verify.go), which tests credentials that
// are not saved against a firewall that may not be the configured one, and whose answer must never
// enter the mirror.
package broker

import (
	"context"
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

	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/broker/infer"
	"github.com/NaejEL/opnview/internal/broker/mirror"
	"github.com/NaejEL/opnview/internal/broker/schedule"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
)

// The ways an answer was served.
const (
	ServedFromMirror   = "mirror"
	ServedFromFirewall = "firewall"
	// ServedFromStream is a polled endpoint a declared stream replaces while it runs (A14): no
	// call is made, and the reader reads the stream.
	ServedFromStream = "stream"
)

// Requester is who asks: a plugin, by name and version, or a core reader.
type Requester struct {
	Name    string
	Version string
}

// Core is a core reader: discovery, the probe round, a coordinator.
func Core(name string) Requester {
	return Requester{Name: mirror.CoreReaderPrefix + name, Version: "core"}
}

// IsCore reports whether the requester is the core.
func (r Requester) IsCore() bool { return strings.HasPrefix(r.Name, mirror.CoreReaderPrefix) }

// Request is one request.
type Request struct {
	Requester Requester
	Endpoint  opnsense.Endpoint
	Options   opnsense.RequestOptions
	// MaxAge is how old an answer may be and still be good enough.
	MaxAge time.Duration
	// After, for a record stream, asks for the rows past this cursor value, with the gaps.
	After *float64
	// Fresh asks for a firewall call whatever the mirror holds: the reader read a field the
	// mirror did not hold, and its call is answered again from the firewall.
	Fresh bool
	// Whole says the requester reads the whole answer (a Go reader of the body).
	Whole bool
}

// Answer is one answer.
type Answer struct {
	// Response is the answer as the client classifies it. Its Body is the firewall's body for
	// an answer the firewall gave, or the mirror's rebuilt one.
	Response opnsense.Response
	// Value is the parsed answer, nil when it was not OK or not JSON; Holding says what it holds.
	Value   *answer.Value
	Holding answer.Holding
	// ServedFrom, FetchedAt and AgeSeconds say how it was served (A11).
	ServedFrom string
	FetchedAt  int64
	AgeSeconds float64
	// Gaps are the intervals a record stream did not cover inside a range read after a cursor.
	Gaps []mirror.Gap
	// Datum is the mirror's datum, 0 when none.
	Datum int64
	// ReplacedBy names the stream that replaces the endpoint while it runs, for a stream answer.
	ReplacedBy string

	// stored is how a firewall answer was stored, so the fields a reader reads first are stored from
	// it at once rather than from the next call.
	stored *storedAnswer
}

// storedAnswer is what storing an answer again needs.
type storedAnswer struct {
	datum *mirror.Datum
	value *answer.Value
	shape infer.Shape
	fetch mirror.Fetch
}

// EndpointDeclaration is what a plugin's manifest declares of an endpoint for the broker.
type EndpointDeclaration struct {
	// MaxAge is the endpoint's default max age, 0 for none.
	MaxAge time.Duration
	// Floor is the freshness floor the manifest declares, 0 for none.
	Floor time.Duration
	// Corrections correct the inference.
	Corrections infer.Corrections
	// Discard is how many events a stream's connection begins with that are discarded.
	Discard int
	// Replaces names an endpoint, by path, a stream replaces while it runs.
	Replaces string
	// Stream is the manifest's flag, when it gave one.
	Stream *bool
	// UpstreamURL cites the endpoint; an endpoint a third party reads without one is uncited.
	UpstreamURL string
	// SurveySection is a built-in's citation.
	SurveySection string
}

// declared is what the loaded plugins declared of one endpoint.
type declared struct {
	corrections   infer.Corrections
	correctedBy   string
	floor         time.Duration
	maxAge        map[string]time.Duration
	discard       int
	replaces      map[string]string // reader → path replaced
	streamFlag    *bool
	uncited       map[string]bool
	conflicts     map[string]string // reader → code
	builtinOwner  bool
	surveySection string
}

// Options are what a broker needs beside the client and the store.
type Options struct {
	// Clock is opnview's clock; nil is the system's.
	Clock schedule.Clock
	// Settings are the scheduler's.
	Settings schedule.Settings
	// Firewall yields the configured base URL, from which the mirror's non-secret identity of
	// the firewall is derived; it is never stored or answered.
	Firewall func() string
	// Normaliser yields the timestamp normaliser of the firewall now.
	Normaliser func(ctx context.Context) (decode.Normaliser, error)
	// Vocabulary yields the installation's interface identifiers and devices, which mark an
	// object keyed by them as value-keyed.
	Vocabulary infer.Vocabulary
	// Log receives the broker's diagnostics; nil writes to standard error.
	Log func(message string)
	// Setting reads the setting table, for the operator's floor overrides; nil reads none.
	Setting SettingReader
}

// Broker is the broker.
type Broker struct {
	client    *opnsense.Client
	mirror    *mirror.Mirror
	scheduler *schedule.Scheduler
	clock     schedule.Clock
	options   Options
	streams   *streams

	mutex    sync.Mutex
	declared map[string]*declared // by endpoint path
	roles    map[int64]map[string]string
	honoured map[int64]map[string]*bool
	flights  map[int64]*flight
	datums   map[string]*mirror.Datum
	firewall string
	// version is the firewall's OPNsense version, read at discovery; empty until read.
	version string
	// lastRequests keeps each datum's last request, which its proactive refresh repeats.
	lastRequests map[int64]Request
	counters     Counters
	recorder     recorder
	// base is the context of the broker's own work -- subscriptions, recordings -- which Run
	// replaces with its own.
	base   context.Context
	cancel context.CancelFunc
}

// Counters are the broker's counters, which a test and the catalogue read.
type Counters struct {
	Calls        int
	MirrorHits   int
	WriteRefused int
	// PerEndpoint counts the firewall calls per endpoint path.
	PerEndpoint map[string]int
}

// flight is one call in progress, which every requester of its datum waits for.
type flight struct {
	done   chan struct{}
	answer *Answer
	err    error
}

// New returns a broker.
func New(client *opnsense.Client, mirrored *mirror.Mirror, options Options) *Broker {
	if options.Clock == nil {
		options.Clock = schedule.SystemClock{}
	}
	if options.Log == nil {
		options.Log = func(message string) { fmt.Fprintf(os.Stderr, "opnview: broker: %s\n", message) }
	}
	if options.Normaliser == nil {
		options.Normaliser = func(context.Context) (decode.Normaliser, error) {
			return decode.Normaliser{Reference: options.Clock.Now().UTC()}, nil
		}
	}
	broker := &Broker{client: client, mirror: mirrored, clock: options.Clock, options: options,
		scheduler: schedule.New(options.Clock, options.Settings), declared: map[string]*declared{},
		roles: map[int64]map[string]string{}, honoured: map[int64]map[string]*bool{},
		flights: map[int64]*flight{}, datums: map[string]*mirror.Datum{}, lastRequests: map[int64]Request{},
		counters: Counters{PerEndpoint: map[string]int{}},
		recorder: recorder{fields: map[string][]string{}, ready: map[string]map[string]bool{},
			kinds: map[string]map[string]string{}}}
	broker.base, broker.cancel = context.WithCancel(context.Background())
	broker.streams = newStreams(broker)
	return broker
}

// Close ends the broker's own work: every subscription is closed. A broker a test made for one
// call is closed when the call is done.
func (b *Broker) Close() {
	b.mutex.Lock()
	cancel := b.cancel
	b.mutex.Unlock()
	cancel()
}

// Scheduler returns the scheduler, for its settings and its counters.
func (b *Broker) Scheduler() *schedule.Scheduler { return b.scheduler }

// Mirror returns the mirror.
func (b *Broker) Mirror() *mirror.Mirror { return b.mirror }

// Run runs the scheduler and the stream subscriptions until the context ends.
func (b *Broker) Run(ctx context.Context) {
	go func() {
		<-ctx.Done()
		b.Close()
	}()
	go b.streams.run(b.baseContext())
	b.scheduler.Run(ctx)
}

// baseContext is the context of the broker's own work.
func (b *Broker) baseContext() context.Context {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.base
}

// Counters returns a copy of the counters.
func (b *Broker) Counters() Counters {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	copied := b.counters
	copied.PerEndpoint = map[string]int{}
	for path, count := range b.counters.PerEndpoint {
		copied.PerEndpoint[path] = count
	}
	return copied
}

// now is the broker's instant, in epoch seconds.
func (b *Broker) now() int64 { return b.clock.Now().Unix() }

// firewallIdentity is the mirror's identity of the configured firewall. A changed base URL is a
// new firewall: nothing of the previous one is fresh.
func (b *Broker) firewallIdentity() string {
	baseURL := ""
	if b.options.Firewall != nil {
		baseURL = b.options.Firewall()
	}
	identity := mirror.FirewallIdentity(baseURL)
	b.mutex.Lock()
	changed := b.firewall != "" && b.firewall != identity
	b.firewall = identity
	if changed {
		b.datums = map[string]*mirror.Datum{}
	}
	b.mutex.Unlock()
	if changed {
		b.scheduler.Forget()
	}
	return identity
}

// Declare records what a loaded plugin declares of an endpoint: its default max age, its floor,
// its corrections, a stream's discarded events and equivalence. Two plugins correcting one
// endpoint differently: the built-in wins, else the first loaded; the other is listed with
// mirror_correction_conflict, and still loads. It returns the conflict code, empty for none.
func (b *Broker) Declare(reader string, builtin bool, endpoint opnsense.Endpoint, declaration EndpointDeclaration) string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	held, found := b.declared[endpoint.Path]
	if !found {
		held = &declared{maxAge: map[string]time.Duration{}, replaces: map[string]string{},
			uncited: map[string]bool{}, conflicts: map[string]string{}}
		b.declared[endpoint.Path] = held
	}
	if declaration.MaxAge > 0 {
		held.maxAge[reader] = declaration.MaxAge
	}
	if declaration.Floor > held.floor {
		held.floor = declaration.Floor
	}
	if declaration.Discard > held.discard {
		held.discard = declaration.Discard
	}
	if declaration.Replaces != "" {
		held.replaces[reader] = declaration.Replaces
	}
	if declaration.Stream != nil {
		held.streamFlag = declaration.Stream
	}
	if declaration.SurveySection != "" {
		held.surveySection = declaration.SurveySection
	}
	if !builtin && declaration.UpstreamURL == "" {
		held.uncited[reader] = true
	}
	conflict := ""
	if !declaration.Corrections.Empty() {
		switch {
		case held.correctedBy == "":
			held.corrections, held.correctedBy, held.builtinOwner = declaration.Corrections, reader, builtin
		case correctionsEqual(held.corrections, declaration.Corrections):
		case builtin && !held.builtinOwner:
			held.conflicts[held.correctedBy] = CodeMirrorCorrectionConflict
			held.corrections, held.correctedBy, held.builtinOwner = declaration.Corrections, reader, builtin
		default:
			held.conflicts[reader] = CodeMirrorCorrectionConflict
			conflict = CodeMirrorCorrectionConflict
		}
	}
	return conflict
}

// CodeMirrorCorrectionConflict lists a plugin whose correction of an endpoint lost to another's.
const CodeMirrorCorrectionConflict = "mirror_correction_conflict"

func correctionsEqual(a, b infer.Corrections) bool {
	encodedA, _ := json.Marshal(a)
	encodedB, _ := json.Marshal(b)
	return string(encodedA) == string(encodedB)
}

// Conflicts returns the readers whose corrections lost, by endpoint path.
func (b *Broker) Conflicts() map[string]map[string]string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	result := map[string]map[string]string{}
	for path, held := range b.declared {
		if len(held.conflicts) > 0 {
			result[path] = map[string]string{}
			for reader, code := range held.conflicts {
				result[path][reader] = code
			}
		}
	}
	return result
}

// declarationOf returns what the loaded plugins declared of an endpoint.
func (b *Broker) declarationOf(path string) *declared {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.declared[path]
}

// parameter is one request parameter, wherever it is carried.
type parameter struct {
	name   string
	where  string // query, form, json
	values []string
	raw    any
}

// parametersOf lists a request's named parameters.
func parametersOf(options opnsense.RequestOptions) []parameter {
	var result []parameter
	for name, values := range options.Query {
		result = append(result, parameter{name: name, where: "query", values: values})
	}
	for name, values := range options.Form {
		result = append(result, parameter{name: name, where: "form", values: values})
	}
	if object, isObject := options.JSON.(map[string]any); isObject {
		for name, value := range object {
			result = append(result, parameter{name: name, where: "json", values: []string{fmt.Sprint(value)}, raw: value})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result
}

// role is a parameter's role for an endpoint: the correction's, else the inference's.
func (b *Broker) role(endpoint string, name string) string {
	if held := b.declarationOf(endpoint); held != nil {
		if role, corrected := held.corrections.Parameters[name]; corrected {
			return role
		}
	}
	return infer.ParameterRole(name)
}

// identityOf is a request's identifying parameters: its positional arguments and every parameter
// whose role is identifying. Window and page parameters identify no datum.
func (b *Broker) identityOf(request Request) map[string]any {
	identity := map[string]any{}
	if len(request.Options.Arguments) > 0 {
		identity["#arguments"] = strings.Join(request.Options.Arguments, "/")
	}
	for _, parameter := range parametersOf(request.Options) {
		if b.role(request.Endpoint.Path, parameter.name) != infer.RoleIdentifying {
			continue
		}
		key := parameter.where + ":" + parameter.name
		if parameter.raw != nil {
			identity[key] = parameter.raw
		} else if len(parameter.values) == 1 {
			identity[key] = parameter.values[0]
		} else {
			identity[key] = parameter.values
		}
	}
	return identity
}

// encodingOf names an endpoint's body encoding for the mirror.
func encodingOf(endpoint opnsense.Endpoint) string {
	switch endpoint.Encoding {
	case opnsense.FormBody:
		return "form"
	case opnsense.JSONBody:
		return "json"
	}
	return "none"
}

// datumOf returns the request's datum.
func (b *Broker) datumOf(ctx context.Context, request Request) (*mirror.Datum, string, error) {
	identity := b.identityOf(request)
	firewall := b.firewallIdentity()
	key := request.Endpoint.Method + " " + request.Endpoint.Path + " " + mirror.IdentityKey(identity) + " " + firewall
	b.mutex.Lock()
	cached, found := b.datums[key]
	b.mutex.Unlock()
	if found {
		return cached, key, nil
	}
	datum, err := b.mirror.Datum(ctx, mirror.Endpoint{Path: request.Endpoint.Path, Method: request.Endpoint.Method,
		Encoding: encodingOf(request.Endpoint)}, identity, firewall)
	if err != nil {
		return nil, key, err
	}
	b.mutex.Lock()
	if held, found := b.datums[key]; found {
		// Another request registered it meanwhile: one datum, one record of its state.
		datum = held
	} else {
		b.datums[key] = datum
	}
	b.mutex.Unlock()
	return datum, key, nil
}

// maxAgeOf is a request's max age: the request's, the manifest's default, or zero.
func (b *Broker) maxAgeOf(request Request) time.Duration {
	if request.MaxAge > 0 {
		return request.MaxAge
	}
	if held := b.declarationOf(request.Endpoint.Path); held != nil {
		if maxAge, found := held.maxAge[request.Requester.Name]; found {
			return maxAge
		}
	}
	return 0
}

// floorOf is an endpoint's freshness floor: the largest of its median latency and the floors
// declared.
func (b *Broker) floorOf(ctx context.Context, path string) time.Duration {
	floor := time.Duration(0)
	if held := b.declarationOf(path); held != nil {
		floor = held.floor
	}
	if median, found := b.medianLatency(ctx, path); found && median > floor {
		floor = median
	}
	if override := floorOverride(ctx, b.options.Setting, path); override > floor {
		floor = override
	}
	return floor
}

// ErrStreamRequested is a request for a stream through Call; streams are subscribed to.
var ErrStreamRequested = errors.New("broker: the endpoint is an event stream, read through a subscription")

// Call answers one request: from the mirror when it holds an answer young enough, else from one
// firewall call shared by every requester of the datum waiting.
func (b *Broker) Call(ctx context.Context, request Request) (*Answer, error) {
	if reason := CheckRead(request.Endpoint.Method, request.Endpoint.Path); reason != "" {
		b.refuseWrite(ctx, request, reason)
		return &Answer{Response: opnsense.Response{Endpoint: request.Endpoint}},
			&ErrWriteRefused{Requester: request.Requester.Name, Method: request.Endpoint.Method,
				Path: request.Endpoint.Path, Reason: reason}
	}
	if request.Endpoint.Stream {
		return &Answer{Response: opnsense.Response{Endpoint: request.Endpoint}}, ErrStreamRequested
	}
	if b.mirror == nil {
		// A broker with no mirror -- a plugin's own tests -- is the gate and nothing more.
		return b.unmirrored(ctx, request)
	}
	datum, key, err := b.datumOf(ctx, request)
	if err != nil {
		// A mirror that cannot be reached never fails a request: the call is made unmirrored.
		b.options.Log(fmt.Sprintf("the mirror could not register a datum of %s: %v", request.Endpoint.Path, err))
		return b.unmirrored(ctx, request)
	}
	maxAge := b.maxAgeOf(request)
	floor := b.floorOf(ctx, request.Endpoint.Path)
	b.mutex.Lock()
	b.lastRequests[datum.ID] = request
	b.mutex.Unlock()
	refresh := func(ctx context.Context) schedule.Outcome {
		b.mutex.Lock()
		last := b.lastRequests[datum.ID]
		b.mutex.Unlock()
		answer, err := b.shared(ctx, datum, key, last, false)
		return scheduleOutcome(answer, err)
	}
	if maxAge > 0 {
		maxAge = b.scheduler.Demand(key, request.Endpoint.Path, request.Requester.Name, maxAge, floor, refresh)
	}
	if replaced := b.streams.replacing(request.Requester.Name, request.Endpoint.Path); replaced != "" {
		b.scheduler.Suspend(key, request.Requester.Name, true)
		return &Answer{Response: opnsense.Response{Endpoint: request.Endpoint, Outcome: opnsense.OutcomeOK},
			ServedFrom: ServedFromStream, ReplacedBy: replaced, FetchedAt: b.now(), Datum: datum.ID}, nil
	}
	b.scheduler.Suspend(key, request.Requester.Name, false)
	if !request.Fresh && maxAge > 0 {
		if served := b.fromMirror(ctx, datum, request, maxAge); served != nil {
			b.mutex.Lock()
			b.counters.MirrorHits++
			b.mutex.Unlock()
			b.count(ctx, datum.EndpointID, "mirror_hits")
			return served, nil
		}
	}
	answer, err := b.shared(ctx, datum, key, request, true)
	if err != nil {
		return answer, err
	}
	return b.serveFresh(ctx, datum, request, answer)
}

// scheduleOutcome is how the scheduler counts an answer.
func scheduleOutcome(answer *Answer, err error) schedule.Outcome {
	if answer == nil {
		return schedule.Failed
	}
	switch answer.Response.Outcome {
	case opnsense.OutcomeServerError, opnsense.OutcomeTransportFailure:
		return schedule.Failed
	case opnsense.OutcomeNotFound, opnsense.OutcomeForbidden:
		return schedule.Refused
	case "":
		if err != nil {
			return schedule.Failed
		}
	}
	return schedule.Succeeded
}

// refuseWrite counts and logs a refused write.
func (b *Broker) refuseWrite(ctx context.Context, request Request, reason string) {
	b.mutex.Lock()
	b.counters.WriteRefused++
	b.mutex.Unlock()
	b.options.Log(fmt.Sprintf("%s: %s %s by %s: %s", CodeWriteRefused, request.Endpoint.Method,
		request.Endpoint.Path, request.Requester.Name, reason))
	if b.mirror == nil {
		return
	}
	if endpointID, err := b.mirror.EndpointID(ctx, mirror.Endpoint{Path: request.Endpoint.Path,
		Method: methodForMirror(request.Endpoint.Method), Encoding: encodingOf(request.Endpoint)}); err == nil {
		b.count(ctx, endpointID, "write_refused")
	}
}

// methodForMirror is the method a refused request is counted under: the mirror records GET and
// POST, and a refused PUT or DELETE counts under the path's POST row.
func methodForMirror(method string) string {
	if method == "GET" {
		return "GET"
	}
	return "POST"
}

// count increments one of an endpoint's counters.
func (b *Broker) count(ctx context.Context, endpointID int64, counter string) {
	_, _ = b.mirror.Exec(ctx, "UPDATE opnsense.mirror_endpoint SET "+counter+" = "+counter+" + 1 WHERE id = ?", endpointID)
}

// unmirrored makes a call the mirror plays no part in.
func (b *Broker) unmirrored(ctx context.Context, request Request) (*Answer, error) {
	started := b.clock.Now()
	response, err := b.client.Call(ctx, request.Endpoint, request.Options)
	b.mutex.Lock()
	b.counters.Calls++
	b.counters.PerEndpoint[request.Endpoint.Path]++
	b.mutex.Unlock()
	served := &Answer{Response: response, ServedFrom: ServedFromFirewall, FetchedAt: started.Unix(),
		Holding: answer.Everything}
	if response.OK() {
		served.Value, _ = answer.Parse(response.Body)
	}
	return served, err
}

// pageSizeOf is the page size a request asks for, 0 for none, -1 for all rows.
func (b *Broker) pageSizeOf(request Request) (int64, int64) {
	page, size := int64(0), int64(0)
	for _, parameter := range parametersOf(request.Options) {
		if len(parameter.values) == 0 {
			continue
		}
		value, err := strconv.ParseInt(strings.TrimSpace(parameter.values[0]), 10, 64)
		if err != nil {
			continue
		}
		switch b.role(request.Endpoint.Path, parameter.name) {
		case infer.RolePage:
			page = value
		case infer.RolePageSize:
			size = value
		}
	}
	return page, size
}

// fromMirror serves a request from the mirror, or returns nil when the mirror cannot.
func (b *Broker) fromMirror(ctx context.Context, datum *mirror.Datum, request Request, maxAge time.Duration) *Answer {
	now := b.now()
	if datum.AttemptedAt > datum.FetchedAt && float64(now-datum.AttemptedAt) <= maxAge.Seconds() {
		// An unsuccessful outcome counts for freshness without replacing data.
		return &Answer{Response: opnsense.Response{Endpoint: request.Endpoint, Outcome: opnsense.Outcome(datum.Outcome),
			StatusCode: datum.StatusCode, Detail: datum.Detail}, ServedFrom: ServedFromMirror,
			FetchedAt: datum.AttemptedAt, AgeSeconds: float64(now - datum.AttemptedAt), Datum: datum.ID,
			Holding: answer.Everything}
	}
	if datum.Generation == 0 || float64(now-datum.FetchedAt) > maxAge.Seconds() {
		return nil
	}
	if request.Whole && !datum.ReadWhole {
		return nil
	}
	page, size := b.pageSizeOf(request)
	if size > 0 && datum.PageSize >= 0 && size > datum.PageSize {
		return nil
	}
	selection := mirror.Selection{After: request.After}
	if size > 0 && page <= 1 {
		selection.Limit = int(size)
	}
	value, err := b.mirror.Load(ctx, datum, selection)
	if err != nil {
		if !errors.Is(err, mirror.ErrNotMirrored) {
			b.options.Log(fmt.Sprintf("the mirror could not serve %s: %v", request.Endpoint.Path, err))
		}
		return nil
	}
	if page > 1 && size > 0 {
		value = sliceRows(value, datum.RowSet, int((page-1)*size), int(size))
	}
	served := &Answer{Response: opnsense.Response{Endpoint: request.Endpoint, Outcome: opnsense.OutcomeOK,
		StatusCode: datum.StatusCode, Body: value.Marshal()}, Value: value, Holding: mirror.HoldingOf(datum),
		ServedFrom: ServedFromMirror, FetchedAt: datum.FetchedAt, AgeSeconds: float64(now - datum.FetchedAt),
		Datum: datum.ID}
	if datum.ReadWhole && datum.Body != "" && request.After == nil && page <= 1 && selection.Limit == 0 {
		served.Response.Body = []byte(datum.Body)
		served.Holding = answer.Everything
	}
	if request.After != nil {
		served.Gaps, _ = b.mirror.Gaps(ctx, datum.ID, int64(*request.After), now)
	}
	return served
}

// shared runs one firewall call for a datum, or joins the one in progress. A reader's call goes
// through the scheduler's queue; a refresh the scheduler started is already its call.
func (b *Broker) shared(ctx context.Context, datum *mirror.Datum, key string, request Request, queued bool) (
	*Answer, error) {
	b.mutex.Lock()
	if running, found := b.flights[datum.ID]; found {
		b.mutex.Unlock()
		select {
		case <-running.done:
			return running.answer, running.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	current := &flight{done: make(chan struct{})}
	b.flights[datum.ID] = current
	b.mutex.Unlock()
	defer func() {
		b.mutex.Lock()
		delete(b.flights, datum.ID)
		b.mutex.Unlock()
		close(current.done)
	}()
	if !queued {
		current.answer, current.err = b.fetch(ctx, datum, request)
		return current.answer, current.err
	}
	_, err := b.scheduler.Do(ctx, key, request.Endpoint.Path, b.maxAgeOf(request), func(callCtx context.Context) schedule.Outcome {
		current.answer, current.err = b.fetch(ctx, datum, request)
		return scheduleOutcome(current.answer, current.err)
	})
	if errors.Is(err, schedule.ErrBackingOff) {
		// The datum backs off: its last outcome is served, with when it was had.
		current.answer = &Answer{Response: opnsense.Response{Endpoint: request.Endpoint,
			Outcome: opnsense.Outcome(datum.Outcome), StatusCode: datum.StatusCode, Detail: datum.Detail},
			ServedFrom: ServedFromMirror, FetchedAt: datum.AttemptedAt, Datum: datum.ID, Holding: answer.Everything}
		if datum.Outcome == "" {
			current.answer.Response.Outcome = opnsense.OutcomeTransportFailure
			current.answer.Response.Detail = "the firewall is backed off after failing"
		}
		return current.answer, nil
	}
	if err != nil && current.answer == nil {
		current.err = err
	}
	return current.answer, current.err
}

// serveFresh shapes a fresh answer for one requester: the page it asked for, or the rows past
// its cursor.
func (b *Broker) serveFresh(ctx context.Context, datum *mirror.Datum, request Request, fresh *Answer) (*Answer, error) {
	if fresh == nil || fresh.Value == nil {
		return fresh, nil
	}
	served := *fresh
	if request.After != nil {
		value, err := b.mirror.Load(ctx, datum, mirror.Selection{After: request.After})
		if err == nil {
			served.Value, served.Holding = value, mirror.HoldingOf(datum)
			served.Response.Body = value.Marshal()
			served.Gaps, _ = b.mirror.Gaps(ctx, datum.ID, int64(*request.After), b.now())
		}
		return &served, nil
	}
	page, size := b.pageSizeOf(request)
	if size > 0 && (page > 1 || int64(countRows(fresh.Value, datum.RowSet)) > size) {
		offset := 0
		if page > 1 {
			offset = int((page - 1) * size)
		}
		served.Value = sliceRows(fresh.Value, datum.RowSet, offset, int(size))
		served.Response.Body = served.Value.Marshal()
	}
	return &served, nil
}

// fetch makes the call -- walking the pages of a paged datum, asking a record stream only for the
// window after its newest stored row -- and stores what was read in the mirror.
func (b *Broker) fetch(ctx context.Context, datum *mirror.Datum, request Request) (*Answer, error) {
	options := cloneOptions(request.Options)
	normaliser, _ := b.options.Normaliser(ctx)
	held := b.declarationOf(request.Endpoint.Path)
	corrections := infer.Corrections{}
	if held != nil {
		corrections = held.corrections
	}
	window, windowName := b.narrowWindow(ctx, datum, request, options)

	started := b.clock.Now()
	response, err := b.client.Call(ctx, request.Endpoint, options)
	pages := 1
	b.recordCall(ctx, datum, request.Endpoint.Path, started, len(response.Body))
	if response.EventStream {
		b.markStream(ctx, datum.EndpointID)
		return &Answer{Response: response, ServedFrom: ServedFromFirewall, FetchedAt: started.Unix(), Datum: datum.ID,
			Holding: answer.Everything}, err
	}
	if err != nil || !response.OK() {
		if response.Outcome != "" {
			if attemptErr := b.mirror.Attempt(ctx, datum, started.Unix(), string(response.Outcome),
				response.StatusCode, response.Detail); attemptErr != nil {
				b.options.Log(attemptErr.Error())
			}
			if response.Outcome == opnsense.OutcomeServerError || response.Outcome == opnsense.OutcomeTransportFailure {
				_ = b.mirror.RecordGap(ctx, datum.ID, mirror.Gap{From: datum.FetchedAt, To: started.Unix(),
					Reason: mirror.GapCallFailed})
			}
		}
		return &Answer{Response: response, ServedFrom: ServedFromFirewall, FetchedAt: started.Unix(), Datum: datum.ID,
			Holding: answer.Everything}, err
	}
	value, parseErr := answer.Parse(response.Body)
	if parseErr != nil {
		// An answer that is not JSON is served as it came, and nothing of it is mirrored.
		_ = b.mirror.Attempt(ctx, datum, started.Unix(), string(response.Outcome), response.StatusCode, response.Detail)
		return &Answer{Response: response, ServedFrom: ServedFromFirewall, FetchedAt: started.Unix(), Datum: datum.ID,
			Holding: answer.Everything}, nil
	}
	shape := infer.Learn(value, corrections, b.options.Vocabulary, normaliser)
	keepLearnedCursor(datum, &shape)
	value, pages, response = b.walkPages(ctx, datum, request, options, shape, value, response)
	_ = pages
	if window != nil {
		b.verifyWindow(ctx, datum, request.Endpoint.Path, windowName, *window, shape, value, normaliser)
	}
	_, size := b.pageSizeOf(request)
	if pages > 1 || size < 0 {
		size = -1
	}
	fetched := mirror.Fetch{At: started.Unix(), StatusCode: response.StatusCode, PageSize: size, Body: response.Body,
		Normaliser: normaliser}
	if _, storeErr := b.mirror.Store(ctx, datum, shape, value, fetched); storeErr != nil {
		// A failed mirror write never fails a request.
		b.options.Log(fmt.Sprintf("the mirror could not store %s: %v", request.Endpoint.Path, storeErr))
	}
	return &Answer{Response: response, Value: value, Holding: answer.Everything, ServedFrom: ServedFromFirewall,
		FetchedAt: started.Unix(), Datum: datum.ID, stored: &storedAnswer{datum: datum, value: value, shape: shape, fetch: fetched}}, nil
}

// recordCall counts a firewall call and records its cost.
func (b *Broker) recordCall(ctx context.Context, datum *mirror.Datum, path string, started time.Time, size int) {
	elapsed := b.clock.Now().Sub(started)
	b.mutex.Lock()
	b.counters.Calls++
	b.counters.PerEndpoint[path]++
	b.mutex.Unlock()
	_, err := b.mirror.Exec(ctx, `UPDATE opnsense.mirror_endpoint SET calls = calls + 1,
		latencies_ms = (SELECT coalesce(jsonb_agg(value), '[]') FROM (SELECT value FROM jsonb_array_elements(
			latencies_ms || jsonb_build_array(CAST(? AS double precision))) WITH ORDINALITY AS e(value, n)
			ORDER BY n DESC LIMIT 64) AS kept),
		sizes_bytes = (SELECT coalesce(jsonb_agg(value), '[]') FROM (SELECT value FROM jsonb_array_elements(
			sizes_bytes || jsonb_build_array(CAST(? AS bigint))) WITH ORDINALITY AS e(value, n)
			ORDER BY n DESC LIMIT 64) AS kept)
		WHERE id = ?`, float64(elapsed.Microseconds())/1000, int64(size), datum.EndpointID)
	if err != nil {
		b.options.Log(fmt.Sprintf("recording the cost of %s: %v", path, err))
	}
}

// markStream records an endpoint seen to answer with an event stream.
func (b *Broker) markStream(ctx context.Context, endpointID int64) {
	_, _ = b.mirror.Exec(ctx, `UPDATE opnsense.mirror_endpoint SET observed_stream = true WHERE id = ?`, endpointID)
}

// medianLatency is an endpoint's median recorded latency.
func (b *Broker) medianLatency(ctx context.Context, path string) (time.Duration, bool) {
	if b.mirror == nil {
		return 0, false
	}
	var encoded *string
	if err := b.mirror.QueryRow(ctx, `SELECT CAST(latencies_ms AS text) FROM opnsense.mirror_endpoint WHERE path = ?
		ORDER BY id LIMIT 1`, path).Scan(&encoded); err != nil || encoded == nil {
		return 0, false
	}
	var values []float64
	if json.Unmarshal([]byte(*encoded), &values) != nil || len(values) == 0 {
		return 0, false
	}
	sort.Float64s(values)
	return time.Duration(values[len(values)/2] * float64(time.Millisecond)), true
}

// cloneOptions copies a request's options, so a broker's narrowing never changes the caller's.
func cloneOptions(options opnsense.RequestOptions) opnsense.RequestOptions {
	copied := opnsense.RequestOptions{Arguments: append([]string(nil), options.Arguments...)}
	if options.Query != nil {
		copied.Query = url.Values{}
		for key, values := range options.Query {
			copied.Query[key] = append([]string(nil), values...)
		}
	}
	if options.Form != nil {
		copied.Form = url.Values{}
		for key, values := range options.Form {
			copied.Form[key] = append([]string(nil), values...)
		}
	}
	if object, isObject := options.JSON.(map[string]any); isObject {
		copiedJSON := make(map[string]any, len(object))
		for key, value := range object {
			copiedJSON[key] = value
		}
		copied.JSON = copiedJSON
	} else {
		copied.JSON = options.JSON
	}
	return copied
}

// Done records what a requester read of an answer: the field paths it read become stored from
// the datum's next call.
func (b *Broker) Done(ctx context.Context, served *Answer, requester Requester, maxAge time.Duration,
	reads *answer.Reads) {
	if b.mirror == nil || served == nil || served.Datum == 0 || reads == nil || reads.Empty() {
		return
	}
	added, err := b.mirror.RecordReads(ctx, served.Datum, mirror.Read{Reader: requester.Name,
		ReaderVersion: requester.Version, MaxAgeSeconds: maxAge.Seconds(), Fields: reads.Fields(),
		Whole: reads.WholePaths()})
	if err != nil {
		b.options.Log(fmt.Sprintf("recording the reads of %s: %v", requester.Name, err))
		return
	}
	if len(added) > 0 && served.stored != nil {
		// The fields read first are stored from the answer that was read, so the next reader
		// finds them without a call.
		if _, err := b.mirror.Store(ctx, served.stored.datum, served.stored.shape, served.stored.value,
			served.stored.fetch); err != nil {
			b.options.Log(fmt.Sprintf("storing what %s read first: %v", requester.Name, err))
		}
	}
}

// keepLearnedCursor keeps a record stream's cursor once learned. An order is only seen across
// three rows or more, and a refresh asking for the window after the newest stored row usually
// answers fewer: such an answer shows no order, which is no evidence the order went away. The
// cursor is kept while its fields are still in the rows the answer carries.
func keepLearnedCursor(datum *mirror.Datum, shape *infer.Shape) {
	if len(shape.Cursor) > 0 || datum.Nature != infer.NatureRecordStream || len(datum.Cursor) == 0 ||
		datum.RowSet != shape.RowSet {
		return
	}
	for _, field := range datum.Cursor {
		if kind, found := shape.Types[answer.Join(shape.RowSet, field)]; found && kind != datum.CursorType &&
			kind != infer.TypeNumber && kind != infer.TypeTimestamp {
			return
		}
	}
	shape.Nature, shape.Cursor, shape.CursorType, shape.CursorDescending = infer.NatureRecordStream, datum.Cursor,
		datum.CursorType, datum.CursorDescending
}
