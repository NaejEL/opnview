package live_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/live"
	"github.com/NaejEL/opnview/internal/web"
	surface "github.com/NaejEL/opnview/internal/web/surfaces/live"
	"github.com/NaejEL/opnview/internal/web/webtest"
	"github.com/NaejEL/opnview/internal/web/webtest/collectiontest"
)

// The live surface (C3 of the backend-completion cycle): the rolling strip and the live settings, in
// JSON, behind the session and, for the write, the CSRF proof (C-AC4).

func decode(t *testing.T, answer *http.Response, into any) {
	t.Helper()
	body := webtest.ReadBody(t, answer)
	if err := json.Unmarshal([]byte(body), into); err != nil {
		t.Fatalf("the answer is not JSON: %.300s", body)
	}
}

func condition(t *testing.T, answer *http.Response) string {
	t.Helper()
	var decoded struct {
		Condition string `json:"condition"`
	}
	decode(t, answer, &decoded)
	return decoded.Condition
}

func TestLiveSettingsWriteRefusesAMissingOrForeignProof(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	body := `{"live_interest_seconds": 60}`
	token := harness.SessionFormToken()
	anonymous := webtest.FreshClient(t, harness)
	if answer := anonymous.PostJSON(surface.PathSettings, body, token, ""); answer.StatusCode != http.StatusUnauthorized ||
		condition(t, answer) != "no_session" {
		t.Errorf("a write without a session answered %d", answer.StatusCode)
	}
	if answer := anonymous.Get(surface.PathStrip); answer.StatusCode != http.StatusUnauthorized {
		t.Errorf("the strip without a session answered %d", answer.StatusCode)
	}
	if answer := anonymous.Get(surface.PathSettings); answer.StatusCode != http.StatusUnauthorized {
		t.Errorf("the settings without a session answered %d", answer.StatusCode)
	}
	for name, attempt := range map[string]func() *http.Response{
		"no proof":    func() *http.Response { return harness.PostJSON(surface.PathSettings, body, "", "") },
		"a wrong one": func() *http.Response { return harness.PostJSON(surface.PathSettings, body, token+"0", "") },
		"another origin": func() *http.Response {
			return harness.PostJSON(surface.PathSettings, body, token, "https://elsewhere.example")
		},
	} {
		answer := attempt()
		if answer.StatusCode != http.StatusForbidden || condition(t, answer) != web.ConditionCSRFRefused {
			t.Errorf("%s answered %d", name, answer.StatusCode)
		}
	}
	if value := collectiontest.Setting(harness, "live_interest_seconds"); value != "30" {
		t.Errorf("a refused write left live_interest_seconds at %q", value)
	}
	if answer := harness.PostJSON(surface.PathSettings, body, token, harness.HTTP.URL); answer.StatusCode != http.StatusOK {
		t.Errorf("a same-origin write with its proof answered %d", answer.StatusCode)
	}
	if value := collectiontest.Setting(harness, "live_interest_seconds"); value != "60" {
		t.Errorf("the accepted write stored %q", value)
	}
}

func TestLiveCadenceAndInterestSettingsAreReadAndWrittenWithinTheirBounds(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	token := harness.SessionFormToken()
	var settings surface.SettingsAnswer
	decode(t, harness.Get(surface.PathSettings), &settings)
	if len(settings.Settings) != 2 {
		t.Fatalf("%d live settings", len(settings.Settings))
	}
	found := false
	for _, cadence := range settings.Cadences {
		if cadence.Plugin == "insight" {
			found = true
			if cadence.CadenceSeconds != 10 || cadence.FloorSeconds != 2 || cadence.SettingKey != "insight_live_cadence_seconds" {
				t.Errorf("the insight live pass is listed as %+v", cadence)
			}
		}
	}
	if !found {
		t.Error("the insight plugin's live pass is not listed")
	}
	for _, refused := range []struct{ body, condition, key string }{
		{`{"live_interest_seconds": 4}`, "setting_out_of_range", "live_interest_seconds"},
		{`{"live_sample_retention_seconds": "an hour"}`, "invalid_setting_value", "live_sample_retention_seconds"},
		{`{"live_cadence_seconds": 5}`, "unknown_setting", "live_cadence_seconds"},
		// One good value beside a bad one: nothing is written.
		{`{"live_interest_seconds": 60, "live_sample_retention_seconds": 1}`, "setting_out_of_range",
			"live_sample_retention_seconds"},
	} {
		answer := harness.PostJSON(surface.PathSettings, refused.body, token, harness.HTTP.URL)
		var decoded surface.Refusal
		decode(t, answer, &decoded)
		if answer.StatusCode != http.StatusBadRequest || decoded.Condition != refused.condition || decoded.Key != refused.key {
			t.Errorf("%s answered %d %+v", refused.body, answer.StatusCode, decoded)
		}
	}
	if value := collectiontest.Setting(harness, "live_interest_seconds"); value != "30" {
		t.Errorf("a refused write stored live_interest_seconds = %q", value)
	}
	answer := harness.PostJSON(surface.PathSettings, `{"live_sample_retention_seconds": 7200}`, token, harness.HTTP.URL)
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("a valid write answered %d", answer.StatusCode)
	}
	decode(t, answer, &settings)
	for _, setting := range settings.Settings {
		if setting.Key == "live_sample_retention_seconds" && (setting.Value != 7200 || !setting.Set) {
			t.Errorf("after the write the retention reads %+v", setting)
		}
	}
}

func TestLiveFieldsAndStripTheStripRenewsTheLeaseAtTheLiveCadence(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	interest := live.New(nil)
	harness.Server.Widgets().SetLiveInterest(interest)
	answer := harness.Get(surface.PathStrip)
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("the strip answered %d: %.300s", answer.StatusCode, webtest.ReadBody(t, answer))
	}
	var strip struct {
		CadenceSeconds    int64           `json:"cadence_seconds"`
		LiveInterestUntil *int64          `json:"live_interest_until"`
		Throughput        json.RawMessage `json:"throughput"`
		Decisions         json.RawMessage `json:"decisions"`
	}
	decode(t, answer, &strip)
	if strip.CadenceSeconds != 10 || strip.LiveInterestUntil == nil || interest.Renewals() != 1 {
		t.Errorf("the strip is at %d s, its lease until %v, renewed %d times", strip.CadenceSeconds,
			strip.LiveInterestUntil, interest.Renewals())
	}
	if strings.Contains(string(strip.Throughput), `"measure":"logged"`) ||
		strings.Contains(string(strip.Decisions), `"measure":"sampled"`) {
		t.Error("a strip series mixes the sampled and the logged measures")
	}
	refused := harness.Get(surface.PathStrip + "?" + "parameters=" + url.QueryEscape(`{"window_seconds": 5}`))
	if refused.StatusCode != http.StatusBadRequest || condition(t, refused) != surface.ConditionWindowOutOfRange {
		t.Errorf("a window of 5 s answered %d", refused.StatusCode)
	}
}
