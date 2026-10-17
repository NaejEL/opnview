//go:build review

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
)

// goldenPath is the widget API's recorded answers, in the source tree.
var goldenPath = filepath.Join("..", "..", "internal", "widget", "widgets", "testdata", "golden_responses.json.gz")

// fakeServer stands in for the routes: it refuses without the session cookie, and otherwise answers a
// marker that is no recorded answer.
func fakeServer() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, err := request.Cookie("session"); err != nil {
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte(`{"condition":"no_session"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"from":"the database"}`))
	})
}

func reviewFixture(t *testing.T) *reviewServer {
	t.Helper()
	answers, err := readRecordedAnswers(goldenPath)
	if err != nil {
		t.Fatalf("reading the recorded answers: %v", err)
	}
	if len(answers) == 0 {
		t.Fatal("the recorded answers are empty")
	}
	return &reviewServer{next: fakeServer(), answers: answers}
}

func get(t *testing.T, handler http.Handler, path string, signedIn bool) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if signedIn {
		request.AddCookie(&http.Cookie{Name: "session", Value: "x"})
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestAWidgetRequestIsAnsweredFromTheRecordedAnswers(t *testing.T) {
	server := reviewFixture(t)
	for widgetType, recorded := range server.answers {
		answer := get(t, server, "/api/widget/"+widgetType, true)
		if answer.Code != http.StatusOK {
			t.Fatalf("%s: status %d", widgetType, answer.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(answer.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v", widgetType, err)
		}
		if _, fromDatabase := body["from"]; fromDatabase {
			t.Fatalf("%s: answered by the database", widgetType)
		}
		found := false
		for _, candidate := range recorded {
			found = found || string(candidate.body) == answer.Body.String()
		}
		if !found {
			t.Errorf("%s: the answer is none of the recorded ones", widgetType)
		}
	}
}

func TestTheRecordedAnswerAskedWithTheSameParametersIsChosen(t *testing.T) {
	server := reviewFixture(t)
	for widgetType, recorded := range server.answers {
		for _, candidate := range recorded {
			parameters, err := json.Marshal(candidate.parameters)
			if err != nil {
				t.Fatal(err)
			}
			answer := get(t, server, "/api/widget/"+widgetType+"?parameters="+url.QueryEscape(string(parameters)), true)
			want := server.choose(widgetType, string(parameters))
			if answer.Body.String() != string(want) {
				t.Errorf("%s %s: not the matching recorded answer", widgetType, parameters)
			}
		}
	}
}

func TestTheSessionStillApplies(t *testing.T) {
	server := reviewFixture(t)
	for widgetType := range server.answers {
		if answer := get(t, server, "/api/widget/"+widgetType, false); answer.Code != http.StatusUnauthorized {
			t.Errorf("%s without a session: status %d", widgetType, answer.Code)
		}
		break
	}
}

func TestEveryOtherRouteIsTheServers(t *testing.T) {
	server := reviewFixture(t)
	for _, path := range []string{"/api/widgets", "/api/widget/no_such_type", "/api/dashboards"} {
		if answer := get(t, server, path, true); answer.Body.String() != `{"from":"the database"}` {
			t.Errorf("%s: %s", path, answer.Body.String())
		}
	}
}
