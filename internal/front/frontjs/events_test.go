package frontjs_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/front/frontjs"
)

// The event-driven canvas (decision D21 as amended; docs/widget-api.md, "Events"): one stream per
// page while signed in, each event asking again only the widgets it concerns, at most one request
// per widget every 2 s, reconnection with back-off, polling only while disconnected, nothing while
// hidden, and the state said in the header and on each card.
//
// FIXTURE PROVENANCE. The answers are the widget API's recorded answers (answers_test.go); the
// dashboards, the two extra registry rows and the events are invented.

// eventsDashboard holds one widget per way an event can concern it, on the canvas shown, and one
// widget on a canvas not shown.
const eventsDashboard = `{"format_version": 1, "canvases": [
  {"id": "main", "title": "Main", "widgets": [
    {"id": "sites", "type": "top_sites", "placement": {"x": 0, "y": 0, "w": 6, "h": 6}},
    {"id": "composition", "type": "traffic_composition", "placement": {"x": 6, "y": 0, "w": 6, "h": 6}},
    {"id": "freshness", "type": "aggregate_freshness", "placement": {"x": 0, "y": 6, "w": 6, "h": 6}},
    {"id": "leases", "type": "lease_example", "placement": {"x": 6, "y": 6, "w": 6, "h": 6}},
    {"id": "peers", "type": "peer_example", "placement": {"x": 0, "y": 12, "w": 6, "h": 6}}
  ]},
  {"id": "other", "title": "Other", "widgets": [
    {"id": "elsewhere", "type": "public_address", "placement": {"x": 0, "y": 0, "w": 6, "h": 6}}
  ]}]}`

// eventTypes are the widget types of eventsDashboard's shown canvas.
var eventTypes = []string{"top_sites", "traffic_composition", "aggregate_freshness", "lease_example", "peer_example"}

// eventsFixture serves eventsDashboard: top_sites windowed, traffic_composition windowed and
// answering a live figure, aggregate_freshness neither, lease_example reading the dhcp_lease kind,
// peer_example a widget of the example_peers plugin.
func eventsFixture(t *testing.T) *canvasFixture {
	t.Helper()
	fixture := newCanvasFixture(t, eventsDashboard)
	fixture.recordedFor(t, "top_sites")
	fixture.recordedFor(t, "aggregate_freshness")
	fixture.recordedFor(t, "public_address")
	fixture.extraRows = append(fixture.extraRows,
		map[string]any{"type": "lease_example", "shape": "table", "backend": true, "windowed": false,
			"providers": []any{"dhcp_lease"}, "parameters": []any{}, "title_key": "plugin.top_sites.widget.top_sites.title"},
		map[string]any{"type": "peer_example", "shape": "table", "backend": true, "windowed": false,
			"plugin": "example_peers", "providers": []any{}, "parameters": []any{},
			"title_key": "plugin.top_sites.widget.top_sites.title"})
	plain := firstDrawable(t, "top_sites").Raw
	for _, extra := range []string{"lease_example", "peer_example"} {
		fixture.answer(extra, func(map[string]any) frontjsAnswer {
			var body any
			_ = json.Unmarshal(plain, &body)
			return ok(body)
		})
	}
	live := firstDrawable(t, "traffic_composition").Raw
	fixture.answer("traffic_composition", func(map[string]any) frontjsAnswer {
		var body map[string]any
		_ = json.Unmarshal(live, &body)
		data := body["data"].(map[string]any)
		data["current_rates"] = map[string]any{"by_direction": []any{map[string]any{"key": "outbound", "rate": map[string]any{
			"state": "current", "measure": "sampled", "in_bits_per_second": 8000, "out_bits_per_second": 4000, "out_measured": true,
			"sampled_at": 1790001795, "span_seconds": 2, "cadence_seconds": 5, "lower_bound": true}}},
			"outbound_unsplit": map[string]any{"state": "no_current_sample", "measure": "sampled"}, "by_member": []any{}}
		return ok(body)
	})
	return fixture
}

// counts are the requests of every widget type of the shown canvas so far.
func counts(page *frontjs.Runtime) map[string]int {
	counted := map[string]int{}
	for _, widgetType := range append(append([]string{}, eventTypes...), "public_address") {
		counted[widgetType] = widgetRequests(page, widgetType)
	}
	return counted
}

// askedSince names the widget types asked since before, with how many times each.
func askedSince(page *frontjs.Runtime, before map[string]int) map[string]int {
	asked := map[string]int{}
	for widgetType, count := range counts(page) {
		if count != before[widgetType] {
			asked[widgetType] = count - before[widgetType]
		}
	}
	return asked
}

func debounceMS(page *frontjs.Runtime) int64 {
	return page.Run(`opnview.scheduler.DEBOUNCE_MS`).ToInteger()
}
func fallbackMS(page *frontjs.Runtime) int64 {
	return page.Run(`opnview.scheduler.FALLBACK_MS`).ToInteger()
}

// hello is the stream's first event.
const hello = `{"type": "hello", "at": 1790001800, "heartbeat_seconds": 15, "lifetime_seconds": 300}`

// liveCanvas opens the events dashboard and makes its stream live.
func liveCanvas(t *testing.T) *frontjs.Runtime {
	t.Helper()
	page := eventsFixture(t).canvasPage(t, "/canvas/1/main")
	page.Emit("hello", hello)
	page.Advance(debounceMS(page))
	page.Advance(0)
	return page
}

func streamState(page *frontjs.Runtime) string {
	return page.Attribute(".live-pill", "data-stream")
}

func setHidden(page *frontjs.Runtime, hidden bool) {
	state := "visible"
	if hidden {
		state = "hidden"
	}
	page.Run(fmt.Sprintf(`document.visibilityState = %q; (function () { var e = __makeEvent("visibilitychange");
  document.body.dispatchEvent(e); })();`, state))
	page.Advance(0)
}

// TestTheEventStreamIsOpenWhileSignedIn: one EventSource on /api/events once signed in, closed on
// sign-out, and none before a session.
func TestTheEventStreamIsOpenWhileSignedIn(t *testing.T) {
	t.Parallel()
	page := eventsFixture(t).canvasPage(t, "/canvas/1/main")
	opened, open := page.Streams()
	if opened != 1 || open != 1 {
		t.Fatalf("signed in, the page opened %d streams and holds %d open, not one", opened, open)
	}
	if url := page.Run(`__eventSources[0].url`).String(); url != "/api/events" {
		t.Errorf("the stream is opened on %q", url)
	}
	page.Run(`(function () { var buttons = document.querySelectorAll("button"); for (var i = 0; i < buttons.length; i++) {
  if (buttons[i].textContent === opnview.strings.t("action.sign_out")) { buttons[i].dispatchEvent(__makeEvent("click")); } } })();`)
	page.Advance(0)
	if _, open := page.Streams(); open != 0 {
		t.Errorf("signed out, the page still holds %d streams open", open)
	}
	page.Advance(120000)
	if opened, _ := page.Streams(); opened != 1 {
		t.Errorf("signed out, the page opened the stream again: %d opened", opened)
	}

	signedOut := newFakeServer(t, true, false).page(t, "/sign-in")
	signedOut.Advance(0)
	if opened, _ := signedOut.Streams(); opened != 0 {
		t.Errorf("without a session the page opened %d streams", opened)
	}
}

// TestEachEventAsksOnlyTheWidgetsItConcerns: every event type of the contract, and the widgets of
// the canvas shown it asks again at once.
func TestEachEventAsksOnlyTheWidgetsItConcerns(t *testing.T) {
	t.Parallel()
	page := eventsFixture(t).canvasPage(t, "/canvas/1/main")
	for _, widgetType := range eventTypes {
		if got := widgetRequests(page, widgetType); got != 1 {
			t.Fatalf("the canvas asked %s %d times when it opened, not once", widgetType, got)
		}
	}
	if widgetRequests(page, "public_address") != 0 {
		t.Fatalf("a widget of a canvas not shown was asked")
	}
	pause := debounceMS(page)
	before := counts(page)
	page.Emit("hello", hello)
	page.Advance(pause)
	page.Advance(0)
	if asked := askedSince(page, before); len(asked) != 0 {
		t.Errorf("the first hello asked %v again", asked)
	}
	at := int64(1790001810)
	cases := []struct {
		name, eventType, data string
		want                  map[string]int
	}{
		{"a collection pass of the dhcp_lease kind", "collection_pass",
			`{"type": "collection_pass", "at": 1790001810, "kind": "dhcp_lease", "provider_key": "kea"}`,
			map[string]int{"lease_example": 1}},
		{"a collection pass of a kind nothing reads", "collection_pass",
			`{"type": "collection_pass", "at": 1790001810, "kind": "security_event", "provider_key": "suricata"}`,
			map[string]int{}},
		{"a pass of the example_peers plugin", "plugin_pass",
			`{"type": "plugin_pass", "at": 1790001810, "plugin": "example_peers", "provider": "peers"}`,
			map[string]int{"peer_example": 1}},
		{"an aggregate refresh of the current slot", "aggregate_refresh",
			fmt.Sprintf(`{"type": "aggregate_refresh", "at": %d, "periods": ["hour"], "families": ["volume"],
			  "ranges": [{"period": "hour", "from": %d, "to": %d}]}`, at, at-at%3600, at-at%3600),
			map[string]int{"top_sites": 1, "traffic_composition": 1}},
		{"an aggregate refresh older than every window", "aggregate_refresh",
			fmt.Sprintf(`{"type": "aggregate_refresh", "at": %d, "periods": ["day"], "families": ["volume"],
			  "ranges": [{"period": "day", "from": %d, "to": %d}]}`, at, at-40*86400, at-35*86400),
			map[string]int{}},
		{"a live sample", "live_sample",
			`{"type": "live_sample", "at": 1790001810, "kind": "measurement_sample", "provider_key": "top", "sampled_at": 1790001809}`,
			map[string]int{"traffic_composition": 1}},
		{"a resync", "resync", `{"type": "resync", "at": 1790001810}`,
			map[string]int{"top_sites": 1, "traffic_composition": 1, "aggregate_freshness": 1, "lease_example": 1, "peer_example": 1}},
	}
	for _, c := range cases {
		before := counts(page)
		page.Emit(c.eventType, c.data)
		page.Advance(0)
		asked := askedSince(page, before)
		if fmt.Sprint(asked) != fmt.Sprint(c.want) {
			t.Errorf("%s asked %v, not %v", c.name, asked, c.want)
		}
		page.Advance(pause)
		page.Advance(0)
		if again := askedSince(page, before); fmt.Sprint(again) != fmt.Sprint(c.want) {
			t.Errorf("%s asked %v once its 2 s had passed, not %v", c.name, again, c.want)
		}
	}
	// A hello after a reconnection: every widget of the canvas shown, once.
	page.StreamError()
	page.Advance(page.Run(`opnview.events.RETRY_MS`).ToInteger())
	page.Advance(0)
	before = counts(page)
	page.Emit("hello", hello)
	page.Advance(0)
	want := map[string]int{"top_sites": 1, "traffic_composition": 1, "aggregate_freshness": 1, "lease_example": 1, "peer_example": 1}
	if asked := askedSince(page, before); fmt.Sprint(asked) != fmt.Sprint(want) {
		t.Errorf("a hello after a reconnection asked %v, not %v", asked, want)
	}
	// Another canvas: the first one's widgets hear no more events.
	page.Go("/canvas/1/other")
	page.Advance(pause)
	page.Advance(0)
	before = counts(page)
	page.Emit("resync", `{"type": "resync", "at": 1790001900}`)
	page.Advance(0)
	if asked := askedSince(page, before); fmt.Sprint(asked) != fmt.Sprint(map[string]int{"public_address": 1}) {
		t.Errorf("on another canvas a resync asked %v", asked)
	}
}

// TestAWidgetIsAskedAtMostOnceEveryTwoSeconds: events in a burst ask a widget at once, then once
// more when 2 s have passed, and no more.
func TestAWidgetIsAskedAtMostOnceEveryTwoSeconds(t *testing.T) {
	t.Parallel()
	page := liveCanvas(t)
	pause := debounceMS(page)
	refresh := `{"type": "aggregate_refresh", "at": 1790001810, "periods": ["hour"], "families": ["volume"],
	  "ranges": [{"period": "hour", "from": 1790000000, "to": 1790001800}]}`
	before := widgetRequests(page, "top_sites")
	page.Emit("aggregate_refresh", refresh)
	page.Advance(0)
	if got := widgetRequests(page, "top_sites") - before; got != 1 {
		t.Fatalf("the first event asked the widget %d times, not once", got)
	}
	for step := 0; step < 4; step++ {
		page.Advance(pause / 5)
		page.Emit("aggregate_refresh", refresh)
		page.Advance(0)
	}
	if got := widgetRequests(page, "top_sites") - before; got != 1 {
		t.Errorf("events within 2 s asked the widget %d times, not once", got)
	}
	page.Advance(pause)
	page.Advance(0)
	if got := widgetRequests(page, "top_sites") - before; got != 2 {
		t.Errorf("after 2 s the events held back asked the widget %d times in all, not twice", got)
	}
	page.Advance(10 * pause)
	page.Advance(0)
	if got := widgetRequests(page, "top_sites") - before; got != 2 {
		t.Errorf("with no event the widget was asked %d times in all, not twice", got)
	}
	// The canvas's own first request counts: an event just after it waits for the 2 s.
	fresh := eventsFixture(t).canvasPage(t, "/canvas/1/main")
	fresh.Emit("hello", hello)
	fresh.Emit("aggregate_refresh", refresh)
	fresh.Advance(0)
	if got := widgetRequests(fresh, "top_sites"); got != 1 {
		t.Errorf("an event just after the canvas opened asked the widget at once: %d requests", got)
	}
	fresh.Advance(pause)
	fresh.Advance(0)
	if got := widgetRequests(fresh, "top_sites"); got != 2 {
		t.Errorf("the event held back was not sent after 2 s: %d requests", got)
	}
}

// TestTheStreamReconnectsWithBackOff: an error leaves the page disconnected and opens the stream
// again after 2, 4, 8 ... up to 30 s; a hello makes it live and resets the back-off; an end opens
// it again after the stated retry and leaves the page live.
func TestTheStreamReconnectsWithBackOff(t *testing.T) {
	t.Parallel()
	page := eventsFixture(t).canvasPage(t, "/canvas/1/main")
	if state := streamState(page); state != "disconnected" {
		t.Errorf("before its hello the header says %q", state)
	}
	page.Emit("hello", hello)
	page.Advance(0)
	if state := streamState(page); state != "live" {
		t.Fatalf("after hello the header says %q, not live", state)
	}
	english := englishCatalogue(t)
	if text := page.Text(".live-pill"); !strings.Contains(text, english["header.stream.live"]) {
		t.Errorf("the live header reads %q", text)
	}
	retry := page.Run(`opnview.events.RETRY_MS`).ToInteger()
	ceiling := page.Run(`opnview.events.BACKOFF_MAX_MS`).ToInteger()
	opened := 1
	for _, wait := range []int64{retry, 2 * retry, 4 * retry, 8 * retry, ceiling, ceiling} {
		page.StreamError()
		page.Advance(0)
		if state := streamState(page); state != "disconnected" {
			t.Fatalf("after an error the header says %q", state)
		}
		if text := page.Text(".live-pill"); !strings.Contains(text, english["header.stream.disconnected"]) {
			t.Errorf("the disconnected header reads %q", text)
		}
		page.Advance(wait - 1)
		if got, open := page.Streams(); got != opened || open != 0 {
			t.Fatalf("before %d ms the stream was opened again: %d opened, %d open", wait, got, open)
		}
		page.Advance(1)
		opened++
		if got, open := page.Streams(); got != opened || open != 1 {
			t.Fatalf("after %d ms the stream was not opened again: %d opened, %d open", wait, got, open)
		}
	}
	page.Emit("hello", hello)
	page.Advance(0)
	if state := streamState(page); state != "live" {
		t.Errorf("a hello after reconnecting leaves the header %q", state)
	}
	page.StreamError()
	page.Advance(retry)
	opened++
	if got, _ := page.Streams(); got != opened {
		t.Errorf("a hello did not reset the back-off: %d opened after %d ms", got, retry)
	}
	// The stream's lifetime elapsed: closed, opened again after the retry, live meanwhile.
	page.Emit("hello", hello)
	page.Emit("end", "")
	page.Advance(0)
	if _, open := page.Streams(); open != 0 {
		t.Errorf("an end left %d streams open", open)
	}
	if state := streamState(page); state != "live" {
		t.Errorf("an end made the header %q", state)
	}
	page.Advance(retry)
	opened++
	if got, open := page.Streams(); got != opened || open != 1 {
		t.Errorf("after an end the stream was not opened again: %d opened, %d open", got, open)
	}
}

// TestAHiddenPageAsksNothingAndCatchesUpOnce: while hidden, neither an event nor the fallback asks
// anything; shown again, every widget of the canvas is asked once.
func TestAHiddenPageAsksNothingAndCatchesUpOnce(t *testing.T) {
	t.Parallel()
	for _, live := range []bool{true, false} {
		page := eventsFixture(t).canvasPage(t, "/canvas/1/main")
		if live {
			page.Emit("hello", hello)
		}
		page.Advance(debounceMS(page))
		page.Advance(0)
		setHidden(page, true)
		before := counts(page)
		page.Emit("aggregate_refresh", `{"type": "aggregate_refresh", "at": 1790001810, "periods": ["hour"],
		  "families": ["volume"], "ranges": [{"period": "hour", "from": 1790000000, "to": 1790001800}]}`)
		page.Emit("live_sample", `{"type": "live_sample", "at": 1790001810, "kind": "measurement_sample",
		  "provider_key": "top", "sampled_at": 1790001809}`)
		page.Emit("resync", `{"type": "resync", "at": 1790001810}`)
		page.Advance(5 * fallbackMS(page))
		if asked := askedSince(page, before); len(asked) != 0 {
			t.Errorf("live=%v: a hidden page asked %v", live, asked)
		}
		setHidden(page, false)
		want := map[string]int{"top_sites": 1, "traffic_composition": 1, "aggregate_freshness": 1, "lease_example": 1, "peer_example": 1}
		if asked := askedSince(page, before); fmt.Sprint(asked) != fmt.Sprint(want) {
			t.Errorf("live=%v: shown again, the page asked %v, not %v", live, asked, want)
		}
	}
}

// TestWindowedWidgetsArePolledOnlyWhileDisconnected: every 15 s while the stream is not heard, the
// windowed widgets only; nothing by time while it is live.
func TestWindowedWidgetsArePolledOnlyWhileDisconnected(t *testing.T) {
	t.Parallel()
	page := eventsFixture(t).canvasPage(t, "/canvas/1/main")
	every := fallbackMS(page)
	windowed := map[string]int{"top_sites": 1, "traffic_composition": 1}
	before := counts(page)
	page.Advance(every - 1)
	if asked := askedSince(page, before); len(asked) != 0 {
		t.Errorf("before 15 s the fallback asked %v", asked)
	}
	page.Advance(1)
	if asked := askedSince(page, before); fmt.Sprint(asked) != fmt.Sprint(windowed) {
		t.Errorf("disconnected, 15 s asked %v, not %v", asked, windowed)
	}
	before = counts(page)
	page.Advance(every)
	if asked := askedSince(page, before); fmt.Sprint(asked) != fmt.Sprint(windowed) {
		t.Errorf("disconnected, the next 15 s asked %v, not %v", asked, windowed)
	}
	// Live: nothing is asked by time.
	page.Emit("hello", hello)
	page.Advance(0)
	before = counts(page)
	page.Advance(20 * every)
	if asked := askedSince(page, before); len(asked) != 0 {
		t.Errorf("live, time alone asked %v", asked)
	}
	// Disconnected again: the fallback resumes.
	page.StreamError()
	page.Advance(0)
	before = counts(page)
	page.Advance(every)
	if asked := askedSince(page, before); fmt.Sprint(asked) != fmt.Sprint(windowed) {
		t.Errorf("disconnected again, 15 s asked %v, not %v", asked, windowed)
	}
}

// TestEachCardStatesTheTimeOfItsLastAnswer: "updated hh:mm:ss" through the catalogue, renewed with
// every answer.
func TestEachCardStatesTheTimeOfItsLastAnswer(t *testing.T) {
	t.Parallel()
	page := liveCanvas(t)
	template := englishCatalogue(t)["card.updated"]
	prefix := strings.TrimSpace(strings.Split(template, "{time}")[0])
	if prefix == "" || !strings.Contains(template, "{time}") {
		t.Fatalf("the catalogue's card.updated is %q", template)
	}
	for _, id := range []string{"sites", "composition", "freshness", "leases", "peers"} {
		text := page.Text(fmt.Sprintf(`section[data-widget-card=%q] [data-updated=%q]`, id, id))
		clock := strings.TrimSpace(strings.TrimPrefix(text, prefix))
		if !strings.HasPrefix(text, prefix) || len(clock) < 5 || strings.Count(clock, ":") < 1 {
			t.Errorf("card %s says %q", id, text)
		}
	}
}
