package broker_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/broker/answer"
	"github.com/NaejEL/opnview/internal/broker/infer"
	"github.com/NaejEL/opnview/internal/broker/mirror"
	"github.com/NaejEL/opnview/internal/broker/schedule"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// The broker's tests (BR-AC1 to BR-AC27 of the broker amendment to
// specs/SPEC-backend-completion-plugins.md): a fake firewall that counts every request it receives,
// a clock the test moves, and the mirror on the PostgreSQL test instance.

// testClock is a clock a test moves.
type testClock struct {
	mutex sync.Mutex
	now   time.Time
}

func (c *testClock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.now
}

// After never fires: a test moves the clock and asks again, nothing waits on it.
func (c *testClock) After(time.Duration) <-chan time.Time { return make(chan time.Time) }

func (c *testClock) advance(by time.Duration) {
	c.mutex.Lock()
	c.now = c.now.Add(by)
	c.mutex.Unlock()
}

// recorded is one request the fake firewall received.
type recorded struct {
	method string
	path   string
	query  url.Values
	body   string
}

// fakeFirewall answers through a handler and counts every request.
type fakeFirewall struct {
	mutex    sync.Mutex
	server   *httptest.Server
	requests []recorded
	handler  func(request *http.Request, body string) (int, string, string)
	streams  map[string]chan string
	// open counts the stream connections open, by path; refuse answers the next connections to a
	// stream path with a server error.
	open   map[string]int
	refuse map[string]int
}

func newFakeFirewall(t *testing.T) *fakeFirewall {
	t.Helper()
	fake := &fakeFirewall{streams: map[string]chan string{}, open: map[string]int{}, refuse: map[string]int{}}
	fake.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		fake.mutex.Lock()
		fake.requests = append(fake.requests, recorded{method: request.Method, path: request.URL.Path,
			query: request.URL.Query(), body: string(raw)})
		handler := fake.handler
		stream := fake.streams[request.URL.Path]
		refused := stream != nil && fake.refuse[request.URL.Path] > 0
		if refused {
			fake.refuse[request.URL.Path]--
		} else if stream != nil {
			fake.open[request.URL.Path]++
		}
		fake.mutex.Unlock()
		if refused {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{}`))
			return
		}
		if stream != nil {
			defer func() {
				fake.mutex.Lock()
				fake.open[request.URL.Path]--
				fake.mutex.Unlock()
			}()
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.WriteHeader(http.StatusOK)
			flusher, _ := writer.(http.Flusher)
			if flusher != nil {
				flusher.Flush()
			}
			for {
				select {
				case event, open := <-stream:
					if !open {
						return
					}
					_, _ = writer.Write([]byte("data: " + event + "\n\n"))
					if flusher != nil {
						flusher.Flush()
					}
				case <-request.Context().Done():
					return
				}
			}
		}
		status, contentType, body := http.StatusNotFound, "application/json", `{"errorMessage":"Endpoint not found"}`
		if handler != nil {
			status, contentType, body = handler(request, string(raw))
		}
		writer.Header().Set("Content-Type", contentType)
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeFirewall) answer(handler func(request *http.Request, body string) (int, string, string)) {
	f.mutex.Lock()
	f.handler = handler
	f.mutex.Unlock()
}

func (f *fakeFirewall) count(path string) int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	count := 0
	for _, request := range f.requests {
		if path == "" || request.path == path {
			count++
		}
	}
	return count
}

func (f *fakeFirewall) last(path string) recorded {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	for index := len(f.requests) - 1; index >= 0; index-- {
		if f.requests[index].path == path {
			return f.requests[index]
		}
	}
	return recorded{}
}

func secret(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw)
}

// world is one broker over the fake, the mirror and the clock.
type world struct {
	t       *testing.T
	fake    *fakeFirewall
	clock   *testClock
	broker  *broker.Broker
	store   *store.Store
	key     string
	secret  string
	baseURL string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	database := storetest.Open(t)
	fake := newFakeFirewall(t)
	clock := &testClock{now: time.Unix(1790000000, 0)}
	w := &world{t: t, fake: fake, clock: clock, store: database, key: secret(t), secret: secret(t),
		baseURL: fake.server.URL}
	client := opnsense.NewClient(opnsense.Credentials{BaseURL: fake.server.URL, APIKey: w.key, APISecret: w.secret},
		http.DefaultTransport, 5*time.Second)
	w.broker = broker.New(client, mirror.New(database, func() int64 { return clock.Now().Unix() }),
		broker.Options{Clock: clock, Firewall: func() string { return w.baseURL }})
	w.broker.Scheduler().SetSettings(schedule.Settings{MaxInFlight: 16, MaxPerSecond: 1e6})
	t.Cleanup(w.broker.Close)
	return w
}

// endpoint admits one test endpoint.
func endpoint(t *testing.T, method, path string, encoding opnsense.BodyEncoding) opnsense.Endpoint {
	t.Helper()
	declared := opnsense.Endpoint{Path: path, Method: method, Encoding: encoding}
	if err := opnsense.AdmitDeclaredEndpoint(declared); err != nil {
		t.Fatal(err)
	}
	return declared
}

// call asks the broker as one reader and reads the given fields of every row, as the plugin host
// does: a read the mirror's answer does not hold is answered again from the firewall (A10).
func (w *world) call(reader string, ep opnsense.Endpoint, options opnsense.RequestOptions, maxAge time.Duration,
	fields ...string) (*broker.Answer, map[string]any) {
	w.t.Helper()
	requester := broker.Requester{Name: reader, Version: "1"}
	fresh := false
	for {
		served, err := w.broker.Call(context.Background(), broker.Request{Requester: requester, Endpoint: ep,
			Options: options, MaxAge: maxAge, Fresh: fresh})
		if err != nil {
			w.t.Fatalf("calling %s: %v", ep.Path, err)
		}
		read, reads, notHeld := readRows(served, fields)
		if notHeld && !fresh {
			fresh = true
			continue
		}
		if notHeld {
			w.t.Fatalf("a fresh answer of %s did not hold what was read", ep.Path)
		}
		w.broker.Done(context.Background(), served, requester, maxAge, reads)
		return served, read
	}
}

// readRows reads the given fields of every row of an answer.
func readRows(served *broker.Answer, fields []string) (map[string]any, *answer.Reads, bool) {
	read := map[string]any{}
	reads := answer.NewReads()
	if served.Value == nil {
		return read, reads, false
	}
	root := answer.Observe(served.Value, reads, served.Holding)
	rows, present, err := root.Get("rows")
	if err != nil {
		return nil, nil, true
	}
	if !present {
		return read, reads, false
	}
	count, err := rows.Len()
	if err != nil {
		return nil, nil, true
	}
	for index := 0; index < count; index++ {
		for _, field := range fields {
			value, present, err := rows.Index(index).Get(field)
			if err != nil {
				return nil, nil, true
			}
			if present {
				read[fmt.Sprintf("%d.%s", index, field)] = value.Peek().Text
			}
		}
	}
	return read, reads, false
}

const rowsBody = `{"total": 2, "rows": [{"id": "1", "a": "x", "b": "y", "c": "z"}, {"id": "2", "a": "p", "b": "q", "c": "r"}]}`

func jsonAnswer(body string) func(*http.Request, string) (int, string, string) {
	return func(*http.Request, string) (int, string, string) { return http.StatusOK, "application/json", body }
}

// TestOnlyTheBrokerAndTheCredentialCheckCallTheClient is BR-AC1: every non-test caller of the
// client's Call, Stream and Listen in cmd/ and internal/ is the broker or the settings surface's
// check of candidate credentials, and that check reaches no mirror.
func TestOnlyTheBrokerAndTheCredentialCheckCallTheClient(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	caller := regexp.MustCompile(`\bclient\.(Call|Stream|Listen)\(`)
	var callers []string
	for _, tree := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if caller.Match(source) {
				relative, _ := filepath.Rel(root, path)
				callers = append(callers, filepath.ToSlash(relative))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range callers {
		if !strings.HasPrefix(path, "internal/broker/") && path != "internal/web/surfaces/settings/verify.go" {
			t.Errorf("%s calls the firewall client without the broker", path)
		}
	}
	verify, err := os.ReadFile(filepath.Join(root, "internal", "web", "surfaces", "settings", "verify.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(verify), "internal/broker") {
		t.Error("the credential check reaches the broker, whose mirror its answer must never enter")
	}
}

// TestTwoReadersShareOneCallAndTheMirrorServesWhileFresh is BR-AC2 and BR-AC3.
func TestTwoReadersShareOneCallAndTheMirrorServesWhileFresh(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ep := endpoint(t, http.MethodGet, "/api/example/shared/search", opnsense.NoBody)
	release := make(chan struct{})
	w.fake.answer(func(*http.Request, string) (int, string, string) {
		<-release
		return http.StatusOK, "application/json", rowsBody
	})
	var group sync.WaitGroup
	reads := make([]map[string]any, 2)
	for index, maxAge := range []time.Duration{30 * time.Second, 60 * time.Second} {
		group.Add(1)
		go func(index int, maxAge time.Duration) {
			defer group.Done()
			_, reads[index] = w.call(fmt.Sprintf("example_reader_%d", index), ep, opnsense.RequestOptions{}, maxAge,
				"a", "b")
		}(index, maxAge)
	}
	time.Sleep(200 * time.Millisecond)
	close(release)
	group.Wait()
	if calls := w.fake.count(ep.Path); calls != 1 {
		t.Fatalf("two readers caused %d calls", calls)
	}
	if fmt.Sprint(reads[0]) != fmt.Sprint(reads[1]) || reads[0]["1.b"] != "q" {
		t.Errorf("the readers read %v and %v", reads[0], reads[1])
	}
	w.clock.advance(10 * time.Second)
	served, read := w.call("example_reader_0", ep, opnsense.RequestOptions{}, 30*time.Second, "a", "b")
	if served.ServedFrom != broker.ServedFromMirror || w.fake.count(ep.Path) != 1 || read["1.a"] != "p" ||
		served.AgeSeconds != 10 {
		t.Errorf("a fresh enough request was served %s (age %v, %d calls, %v)", served.ServedFrom, served.AgeSeconds,
			w.fake.count(ep.Path), read)
	}
	w.clock.advance(31 * time.Second)
	served, _ = w.call("example_reader_0", ep, opnsense.RequestOptions{}, 30*time.Second, "a")
	if served.ServedFrom != broker.ServedFromFirewall || w.fake.count(ep.Path) != 2 {
		t.Errorf("a request past its max age was served %s with %d calls", served.ServedFrom, w.fake.count(ep.Path))
	}
}

// TestAFieldReadForTheFirstTimeIsAddedAndAReaderOfTheBodyReadsAll is BR-AC4's broker half: a field
// read first from a mirror answer is not held, so the reader is answered from the firewall, and
// the field is stored from then on; a reader of the body reads the datum whole.
func TestAFieldReadForTheFirstTimeIsAddedAndAReaderOfTheBodyReadsAll(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ep := endpoint(t, http.MethodGet, "/api/example/added/search", opnsense.NoBody)
	w.fake.answer(jsonAnswer(rowsBody))
	w.call("example_reader", ep, opnsense.RequestOptions{}, time.Minute, "a", "b")
	w.call("example_reader", ep, opnsense.RequestOptions{}, time.Minute, "a", "b")
	served, err := w.broker.Call(context.Background(), broker.Request{Requester: broker.Requester{Name: "example_reader",
		Version: "2"}, Endpoint: ep, MaxAge: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	rows, _, _ := answer.Observe(served.Value, answer.NewReads(), served.Holding).Get("rows")
	if _, _, err := rows.Index(0).Get("c"); err == nil {
		t.Fatal("the mirror answered a field it never stored")
	}
	fresh, err := w.broker.Call(context.Background(), broker.Request{Requester: broker.Requester{Name: "example_reader",
		Version: "2"}, Endpoint: ep, MaxAge: time.Minute, Fresh: true})
	if err != nil {
		t.Fatal(err)
	}
	reads := answer.NewReads()
	rows, _, _ = answer.Observe(fresh.Value, reads, fresh.Holding).Get("rows")
	value, _, err := rows.Index(0).Get("c")
	if err != nil || value.Peek().Text != "z" {
		t.Fatalf("the fresh answer gave %v (%v)", value, err)
	}
	w.broker.Done(context.Background(), fresh, broker.Requester{Name: "example_reader", Version: "2"}, time.Minute, reads)
	w.clock.advance(2 * time.Minute)
	w.call("example_reader", ep, opnsense.RequestOptions{}, time.Minute)
	var columns int
	if err := w.store.DB().QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_schema = 'opnsense'
		AND table_name = ? AND column_name = 'c'`, mirror.TableName(mirror.Endpoint{Path: ep.Path, Method: ep.Method},
		"rows[]")).Scan(&columns); err != nil || columns != 1 {
		t.Errorf("the newly read field has %d columns (%v)", columns, err)
	}

	whole := endpoint(t, http.MethodGet, "/api/example/whole/get", opnsense.NoBody)
	w.fake.answer(jsonAnswer(rowsBody))
	response, err := w.broker.GateCall(context.Background(), "core:example", whole, opnsense.RequestOptions{},
		time.Minute)
	if err != nil || !response.OK() {
		t.Fatal(err)
	}
	w.clock.advance(2 * time.Minute)
	if _, err := w.broker.GateCall(context.Background(), "core:example", whole, opnsense.RequestOptions{},
		time.Minute); err != nil {
		t.Fatal(err)
	}
	var readWhole bool
	if err := w.store.DB().QueryRow(`SELECT d.read_whole FROM opnsense.mirror_datum AS d
		JOIN opnsense.mirror_endpoint AS e ON e.id = d.endpoint_id WHERE e.path = ?`, whole.Path).Scan(&readWhole); err != nil ||
		!readWhole {
		t.Errorf("a reader of the body did not make the datum read_whole (%v)", err)
	}
}

// TestARecordStreamIsReadAfterItsCursorWithItsGaps is BR-AC8: after the first fetch each refresh
// asks only for the window after the newest stored row; a reader asking with `after` gets only
// newer rows; an answer whose oldest row is newer than the newest stored records a gap, returned with
// the rows.
func TestARecordStreamIsReadAfterItsCursorWithItsGaps(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ep := endpoint(t, http.MethodPost, "/api/example/log/search", opnsense.JSONBody)
	var mutex sync.Mutex
	rows := []int64{1790000000 - 300, 1790000000 - 200, 1790000000 - 100}
	w.fake.answer(func(_ *http.Request, body string) (int, string, string) {
		var request struct {
			From int64 `json:"from"`
			To   int64 `json:"to"`
		}
		_ = json.Unmarshal([]byte(body), &request)
		mutex.Lock()
		defer mutex.Unlock()
		var items []string
		for index := len(rows) - 1; index >= 0; index-- {
			if rows[index] >= request.From && rows[index] <= request.To {
				items = append(items, fmt.Sprintf(`{"id": "%d", "time": %d, "v": %d}`, rows[index], rows[index], index))
			}
		}
		return http.StatusOK, "application/json", `{"rows": [` + strings.Join(items, ",") + `]}`
	})
	window := func() opnsense.RequestOptions {
		now := w.clock.Now().Unix()
		return opnsense.RequestOptions{JSON: map[string]any{"from": now - 3600, "to": now}}
	}
	w.call("example_reader", ep, window(), 10*time.Second, "time", "v")
	w.clock.advance(20 * time.Second)
	mutex.Lock()
	rows = append(rows, 1790000000+10)
	mutex.Unlock()
	w.call("example_reader", ep, window(), 10*time.Second, "time", "v")
	if body := w.fake.last(ep.Path).body; !strings.Contains(body, `"from":1789999900`) {
		t.Errorf("the refresh asked %s, not the window after the newest stored row", body)
	}
	after := float64(1790000000 - 150)
	served, err := w.broker.Call(context.Background(), broker.Request{Requester: broker.Requester{Name: "example_reader",
		Version: "1"}, Endpoint: ep, Options: window(), MaxAge: time.Hour, After: &after})
	if err != nil {
		t.Fatal(err)
	}
	items := served.Value.Field("rows").Items
	if len(items) != 2 || items[0].Field("time").Text != "1790000010" {
		t.Errorf("after the cursor the reader got %s", served.Value.Marshal())
	}
	// A silence: the firewall now holds only a row far newer than the newest stored.
	w.clock.advance(time.Hour)
	mutex.Lock()
	rows = []int64{1790000000 + 3000}
	mutex.Unlock()
	w.call("example_reader", ep, opnsense.RequestOptions{JSON: map[string]any{"from": int64(1790000000 + 2900),
		"to": w.clock.Now().Unix()}}, 10*time.Second, "time", "v")
	served, err = w.broker.Call(context.Background(), broker.Request{Requester: broker.Requester{Name: "example_reader",
		Version: "1"}, Endpoint: ep, MaxAge: time.Hour, After: &after})
	if err != nil {
		t.Fatal(err)
	}
	if len(served.Gaps) == 0 || served.Gaps[0].Reason != mirror.GapAnswerNewerThanStored {
		t.Errorf("the silence was not returned as a gap: %+v", served.Gaps)
	}
}

// TestAWindowNotHonouredIsNoLongerRelied on is BR-AC9.
func TestAWindowNotHonouredIsNoLongerReliedOn(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ep := endpoint(t, http.MethodPost, "/api/example/ignored/search", opnsense.JSONBody)
	w.fake.answer(jsonAnswer(`{"rows": [{"time": 1789990000, "x": "a"}, {"time": 1789980000, "x": "b"},
		{"time": 1789970000, "x": "c"}]}`))
	now := w.clock.Now().Unix()
	w.call("example_reader", ep, opnsense.RequestOptions{JSON: map[string]any{"from": now - 600, "to": now}},
		10*time.Second, "time", "x")
	var honoured *bool
	if err := w.store.DB().QueryRow(`SELECT p.honoured FROM opnsense.mirror_parameter AS p
		JOIN opnsense.mirror_endpoint AS e ON e.id = p.endpoint_id WHERE e.path = ? AND p.name = 'from'`, ep.Path).
		Scan(&honoured); err != nil || honoured == nil || *honoured {
		t.Fatalf("the window parameter is recorded honoured %v (%v)", honoured, err)
	}
	w.clock.advance(20 * time.Second)
	w.call("example_reader", ep, opnsense.RequestOptions{JSON: map[string]any{"from": now - 600, "to": now}},
		10*time.Second, "time", "x")
	if body := w.fake.last(ep.Path).body; !strings.Contains(body, fmt.Sprintf(`"from":%d`, now-600)) {
		t.Errorf("a window not honoured was narrowed again: %s", body)
	}
}

// TestAPagedDatumIsWalkedToItsEndAndSharedWhateverThePage is BR-AC10.
func TestAPagedDatumIsWalkedToItsEndAndSharedWhateverThePage(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ep := endpoint(t, http.MethodPost, "/api/example/paged/search", opnsense.FormBody)
	const total = 35
	w.fake.answer(func(_ *http.Request, body string) (int, string, string) {
		form, _ := url.ParseQuery(body)
		current, _ := strconv.Atoi(form.Get("current"))
		size, _ := strconv.Atoi(form.Get("rowCount"))
		var items []string
		for index := (current - 1) * size; index < current*size && index < total; index++ {
			items = append(items, fmt.Sprintf(`{"id": "%d"}`, index))
		}
		return http.StatusOK, "application/json", fmt.Sprintf(`{"total": %d, "rowCount": %d, "current": %d, "rows": [%s]}`,
			total, size, current, strings.Join(items, ","))
	})
	served, read := w.call("example_reader", ep, opnsense.RequestOptions{Form: opnsense.Pagination(1, 10)}, time.Minute,
		"id")
	if calls := w.fake.count(ep.Path); calls != 4 {
		t.Errorf("a datum of 3.5 pages took %d calls", calls)
	}
	if len(served.Value.Field("rows").Items) != 10 || read["0.id"] != "0" {
		t.Errorf("the first page holds %d rows", len(served.Value.Field("rows").Items))
	}
	second, read := w.call("example_reader", ep, opnsense.RequestOptions{Form: opnsense.Pagination(4, 10)}, time.Minute,
		"id")
	if w.fake.count(ep.Path) != 4 || second.ServedFrom != broker.ServedFromMirror || len(read) != 5 || read["0.id"] != "30" {
		t.Errorf("the fourth page was served %s with %v after %d calls", second.ServedFrom, read, w.fake.count(ep.Path))
	}
}

// TestCadenceFollowsDemandAndAFloorBoundsIt is BR-AC13.
func TestCadenceFollowsDemandAndAFloorBoundsIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ep := endpoint(t, http.MethodGet, "/api/example/cadence/get", opnsense.NoBody)
	w.broker.Declare("example_reader", false, ep, broker.EndpointDeclaration{Floor: 5 * time.Second,
		UpstreamURL: "https://example.invalid/source"})
	w.fake.answer(jsonAnswer(rowsBody))
	w.call("example_slow", ep, opnsense.RequestOptions{}, 60*time.Second)
	w.call("example_fast", ep, opnsense.RequestOptions{}, 2*time.Second)
	keys := w.datumKeys(ep)
	if interval, live := w.broker.Scheduler().Interval(keys); !live || interval != 5*time.Second {
		t.Errorf("a request below the floor gives the interval %v, not the floor", interval)
	}
	w.clock.advance(3 * time.Second)
	served, _ := w.call("example_fast", ep, opnsense.RequestOptions{}, 2*time.Second)
	if served.ServedFrom != broker.ServedFromMirror || served.AgeSeconds != 3 {
		t.Errorf("a request below the floor was served %s at age %v", served.ServedFrom, served.AgeSeconds)
	}
	// The fast reader stops; within its lapse the interval relaxes to the slow reader's, and to none.
	w.clock.advance(2 * time.Minute)
	w.call("example_slow", ep, opnsense.RequestOptions{}, 60*time.Second)
	if interval, live := w.broker.Scheduler().Interval(keys); !live || interval != 60*time.Second {
		t.Errorf("after the fast reader stopped the interval is %v", interval)
	}
	w.clock.advance(10 * time.Minute)
	if _, live := w.broker.Scheduler().Interval(keys); live {
		t.Error("a datum nobody reads keeps a cadence")
	}
}

// datumKeys is the scheduler's key of an endpoint's datum with no identifying parameter.
func (w *world) datumKeys(ep opnsense.Endpoint) string {
	return ep.Method + " " + ep.Path + " " + mirror.IdentityKey(map[string]any{}) + " " +
		mirror.FirewallIdentity(w.baseURL)
}

// TestFailuresBackOffAndNotFoundDoesNot is BR-AC14's broker half: server errors back a datum off
// until the first answer, while not_found is a state answered to every reader at its own cadence.
func TestFailuresBackOffAndNotFoundDoesNot(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	failing := endpoint(t, http.MethodGet, "/api/example/failing/get", opnsense.NoBody)
	absent := endpoint(t, http.MethodGet, "/api/example/absent/get", opnsense.NoBody)
	w.fake.answer(func(request *http.Request, _ string) (int, string, string) {
		if request.URL.Path == failing.Path {
			return http.StatusInternalServerError, "application/json", `{}`
		}
		return http.StatusNotFound, "application/json", `{}`
	})
	w.call("example_reader", failing, opnsense.RequestOptions{}, time.Second)
	w.clock.advance(time.Second)
	served, _ := w.call("example_reader", failing, opnsense.RequestOptions{}, time.Second)
	if w.fake.count(failing.Path) != 1 || served.Response.Outcome != opnsense.OutcomeServerError {
		t.Errorf("a failing datum was called %d times inside its back-off (%s)", w.fake.count(failing.Path),
			served.Response.Outcome)
	}
	for _, reader := range []string{"example_one", "example_two"} {
		served, _ := w.call(reader, absent, opnsense.RequestOptions{}, time.Minute)
		if served.Response.Outcome != opnsense.OutcomeNotFound {
			t.Errorf("%s was answered %s", reader, served.Response.Outcome)
		}
	}
	if w.fake.count(absent.Path) != 1 {
		t.Errorf("two readers of an absent endpoint caused %d calls", w.fake.count(absent.Path))
	}
	w.clock.advance(2 * time.Minute)
	w.call("example_one", absent, opnsense.RequestOptions{}, time.Minute)
	if w.fake.count(absent.Path) != 2 {
		t.Error("not_found was backed off rather than read at the reader's cadence")
	}
}

// TestAWriteIsRefusedAndNothingReachesTheFirewall is BR-AC15.
func TestAWriteIsRefusedAndNothingReachesTheFirewall(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.fake.answer(jsonAnswer(`{}`))
	for _, refused := range []opnsense.Endpoint{
		{Path: "/api/example/thing/add_item", Method: http.MethodPost},
		{Path: "/api/diagnostics/interface/carp_status", Method: http.MethodPost},
		{Path: "/api/example/thing/get", Method: http.MethodPut},
		{Path: "/api/example/thing/get", Method: http.MethodDelete},
		{Path: "/api/example/thing/reconfigure", Method: http.MethodGet},
		{Path: "/api/example/thing/apply", Method: http.MethodPost},
	} {
		_, err := w.broker.Call(context.Background(), broker.Request{Requester: broker.Requester{Name: "example_writer"},
			Endpoint: refused})
		var writeRefused *broker.ErrWriteRefused
		if !errors.As(err, &writeRefused) {
			t.Errorf("%s %s was not refused as a write: %v", refused.Method, refused.Path, err)
		}
	}
	if count := w.fake.count(""); count != 0 {
		t.Errorf("%d requests reached the firewall", count)
	}
	if counters := w.broker.Counters(); counters.WriteRefused != 6 {
		t.Errorf("%d refusals were counted", counters.WriteRefused)
	}
	for _, read := range []string{"search_rule", "searchItems", "get_interface_names", "query_alerts", "list_local_data",
		"status", "dumpcache_status"} {
		if reason := broker.CheckRead(http.MethodPost, "/api/example/thing/"+read); reason != "" {
			t.Errorf("the read action %s was refused: %s", read, reason)
		}
	}
	if broker.CheckRead(http.MethodPost, "/api/example/thing/dumpcache") == "" {
		t.Error("a POST to a command that is no read action was forwarded")
	}
}

// TestAnEventStreamIsRecognisedFromItsHeaders is BR-AC22.
func TestAnEventStreamIsRecognisedFromItsHeaders(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ep := endpoint(t, http.MethodGet, "/api/example/sneaky/get", opnsense.NoBody)
	events := make(chan string)
	w.fake.mutex.Lock()
	w.fake.streams[ep.Path] = events
	w.fake.mutex.Unlock()
	t.Cleanup(func() { close(events) })
	started := time.Now()
	served, err := w.broker.Call(context.Background(), broker.Request{Requester: broker.Requester{Name: "example_reader"},
		Endpoint: ep, MaxAge: time.Minute})
	if err != nil || !served.Response.EventStream {
		t.Fatalf("the stream was not recognised: %+v %v", served.Response, err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Errorf("the call held for %v, past the stream bound", elapsed)
	}
	catalogue, err := w.broker.Catalogue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range catalogue.Endpoints {
		if entry.Path == ep.Path && !entry.ObservedStream {
			t.Error("the catalogue does not record the stream")
		}
	}
}

// TestAChangedFirewallServesNothingAsFresh is BR-AC25.
func TestAChangedFirewallServesNothingAsFresh(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ep := endpoint(t, http.MethodGet, "/api/example/moved/get", opnsense.NoBody)
	w.fake.answer(jsonAnswer(rowsBody))
	w.call("example_reader", ep, opnsense.RequestOptions{}, time.Hour, "a")
	w.call("example_reader", ep, opnsense.RequestOptions{}, time.Hour, "a")
	if w.fake.count(ep.Path) != 1 {
		t.Fatal("the mirror did not serve a fresh answer")
	}
	w.baseURL = strings.Replace(w.fake.server.URL, "127.0.0.1", "localhost", 1)
	served, _ := w.call("example_reader", ep, opnsense.RequestOptions{}, time.Hour, "a")
	if served.ServedFrom != broker.ServedFromFirewall || w.fake.count(ep.Path) != 2 {
		t.Errorf("after the firewall changed, the answer was served from %s after %d calls", served.ServedFrom, w.fake.count(ep.Path))
	}
}

// TestTheCatalogueHoldsNoValueNoCredentialAndListsAnUncitedEndpoint is BR-AC23's string search and
// BR-AC26's third-party half.
func TestTheCatalogueHoldsNoValueNoCredentialAndListsAnUncitedEndpoint(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ep := endpoint(t, http.MethodGet, "/api/example/listed/search", opnsense.NoBody)
	w.broker.Declare("example_third_party", false, ep, broker.EndpointDeclaration{
		Corrections: infer.Corrections{Key: []string{"id"}}})
	w.fake.answer(jsonAnswer(`{"rows": [{"id": "value-held-secretly-1", "a": "example-value-a"}]}`))
	w.call("example_third_party", ep, opnsense.RequestOptions{Query: url.Values{"client": {"value-in-parameter"}}},
		time.Minute, "id", "a")
	catalogue, err := w.broker.Catalogue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(catalogue)
	for _, forbidden := range []string{"value-held-secretly-1", "example-value-a", "value-in-parameter", w.key, w.secret,
		w.baseURL, "Authorization", "Basic "} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("the catalogue carries %q", forbidden)
		}
	}
	found := false
	for _, entry := range catalogue.Endpoints {
		if entry.Path != ep.Path {
			continue
		}
		found = true
		if len(entry.Uncited) != 1 || entry.Uncited[0] != "example_third_party" || len(entry.Data) != 1 ||
			len(entry.Data[0].Readers) != 1 || len(entry.Data[0].Readers[0].Fields) == 0 ||
			len(entry.Data[0].Tables) == 0 || entry.Cost.Calls != 1 {
			t.Errorf("the endpoint is catalogued as %s", mustJSON(entry))
		}
	}
	if !found {
		t.Error("the endpoint is not catalogued")
	}
	datasets := w.broker.Datasets(context.Background())
	listed := false
	for _, declaration := range datasets {
		if strings.Contains(declaration.Name, "listed_search") && declaration.Origin == "opnsense" {
			listed = true
		}
	}
	if !listed {
		t.Error("the mirror table is no dataset of the data catalogue")
	}
}

func mustJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// TestNoBuiltinScriptReadsTheBody is BR-AC27's second half: no built-in plugin script reads an
// answer's body or calls json(), so the mirror stores only what they read.
func TestNoBuiltinScriptReadsTheBody(t *testing.T) {
	t.Parallel()
	scripts, err := filepath.Glob(filepath.Join("..", "..", "plugins", "*", "*.js"))
	if err != nil || len(scripts) == 0 {
		t.Fatalf("no built-in script (%v)", err)
	}
	reading := regexp.MustCompile(`\.json\(\)|\.body\b`)
	for _, script := range scripts {
		source, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		if reading.Match(source) {
			t.Errorf("%s reads an answer's body", filepath.ToSlash(script))
		}
	}
}

// TestTheMirrorTablesAreDatasetsWithInferredRoles is BR-AC23's data-catalogue half: a mirrored
// table is a dataset of origin opnsense, a number a measure, a text a dimension, and a column
// carrying host names says so, which the catalogue withholds under no_domains.
func TestTheMirrorTablesAreDatasetsWithInferredRoles(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ep := endpoint(t, http.MethodGet, "/api/example/lookups/search", opnsense.NoBody)
	w.fake.answer(jsonAnswer(`{"rows": [{"id": "1", "domain": "www.example.com", "hits": 3},
		{"id": "2", "domain": "mail.example.org", "hits": 5}, {"id": "3", "domain": "ftp.example.net", "hits": 1}]}`))
	w.call("example_reader", ep, opnsense.RequestOptions{}, time.Minute, "id", "domain", "hits")
	var found bool
	for _, declaration := range w.broker.Datasets(context.Background()) {
		if !strings.Contains(declaration.Name, "api_example_lookups_search") {
			continue
		}
		found = true
		if declaration.Origin != "opnsense" {
			t.Errorf("the mirror dataset's origin is %q", declaration.Origin)
		}
		roles := map[string]string{}
		domains := map[string]bool{}
		for _, column := range declaration.Columns {
			roles[column.Name] = string(column.Role)
			domains[column.Name] = column.CarriesDomain
		}
		if roles["hits"] != "measure" || roles["domain"] == "measure" || roles["domain"] == "time" || !domains["domain"] {
			t.Errorf("the mirror dataset's columns are %+v", declaration.Columns)
		}
	}
	if !found {
		t.Error("the mirror table is not a dataset")
	}
}

// TestAFailureOfAPreviousProcessIsNotServedAsFresh is D6 of the live validation of 17 October 2026:
// a failed attempt is persisted in the mirror, and a broker made after it -- a restart to recover from
// an outage -- asks the firewall again rather than serving the previous process's failure for up to
// the reader's max age.
func TestAFailureOfAPreviousProcessIsNotServedAsFresh(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	failing := endpoint(t, http.MethodGet, "/api/example/restarted/get", opnsense.NoBody)
	failed := true
	var mutex sync.Mutex
	w.fake.answer(func(*http.Request, string) (int, string, string) {
		mutex.Lock()
		defer mutex.Unlock()
		if failed {
			return http.StatusInternalServerError, "application/json", `{}`
		}
		return http.StatusOK, "application/json", `{"rows": [{"id": "1"}]}`
	})
	if served, _ := w.call("example_reader", failing, opnsense.RequestOptions{}, 5*time.Minute); served.Response.Outcome !=
		opnsense.OutcomeServerError {
		t.Fatalf("the first call was answered %s", served.Response.Outcome)
	}
	mutex.Lock()
	failed = false
	mutex.Unlock()
	w.clock.advance(10 * time.Second)
	client := opnsense.NewClient(opnsense.Credentials{BaseURL: w.baseURL, APIKey: w.key, APISecret: w.secret},
		http.DefaultTransport, 5*time.Second)
	restarted := broker.New(client, mirror.New(w.store, func() int64 { return w.clock.Now().Unix() }),
		broker.Options{Clock: w.clock, Firewall: func() string { return w.baseURL }})
	t.Cleanup(restarted.Close)
	served, err := restarted.Call(context.Background(), broker.Request{Requester: broker.Requester{Name: "example_reader",
		Version: "1"}, Endpoint: failing, MaxAge: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if served.Response.Outcome != opnsense.OutcomeOK || w.fake.count(failing.Path) != 2 {
		t.Errorf("after the restart the reader was answered %s from %s, after %d calls", served.Response.Outcome,
			served.ServedFrom, w.fake.count(failing.Path))
	}
}
