package netflow_test

import (
	"encoding/json"
	"net/http"
	"testing"

	surface "github.com/NaejEL/opnview/internal/web/surfaces/netflow"
	"github.com/NaejEL/opnview/internal/web/webtest"
)

// The NetFlow settings in JSON (PS19 rule 3 of the NetFlow join amendment: the tolerance is set through
// the settings JSON API), behind the session and, for the write, the CSRF proof.

func decode(t *testing.T, answer *http.Response, into any) {
	t.Helper()
	body := webtest.ReadBody(t, answer)
	if err := json.Unmarshal([]byte(body), into); err != nil {
		t.Fatalf("the answer is not JSON: %.300s", body)
	}
}

// TestTheJoinToleranceIsSetThroughTheSettingsAPI writes the tolerance and the retention, reads them
// back, and refuses a value out of range, a value that is not a whole number and an unknown key, each
// writing nothing.
func TestTheJoinToleranceIsSetThroughTheSettingsAPI(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	token := harness.SessionFormToken()
	if answer := webtest.FreshClient(t, harness).Get(surface.PathSettings); answer.StatusCode != http.StatusUnauthorized {
		t.Errorf("the settings without a session answered %d", answer.StatusCode)
	}

	var read surface.SettingsAnswer
	answer := harness.Get(surface.PathSettings)
	decode(t, answer, &read)
	if len(read.Settings) != 2 || read.Settings[0].Key != "netflow_join_tolerance_seconds" ||
		read.Settings[0].Value != 3 || read.Settings[1].Value != 0 {
		t.Fatalf("the settings read %+v", read)
	}

	answer = harness.PostJSON(surface.PathSettings, `{"netflow_join_tolerance_seconds": 5,
		"retention_flow_record_seconds": 86400}`, token, "")
	decode(t, answer, &read)
	if answer.StatusCode != http.StatusOK || read.Settings[0].Value != 5 || read.Settings[1].Value != 86400 {
		t.Errorf("the write answered %d with %+v", answer.StatusCode, read)
	}
	if harness.Setting("netflow_join_tolerance_seconds") != "5" {
		t.Errorf("the tolerance stored is %q", harness.Setting("netflow_join_tolerance_seconds"))
	}

	for body, want := range map[string]string{
		`{"netflow_join_tolerance_seconds": 0}`:     "setting_out_of_range",
		`{"netflow_join_tolerance_seconds": 61}`:    "setting_out_of_range",
		`{"netflow_join_tolerance_seconds": "two"}`: "invalid_setting_value",
		`{"retention_flow_record_seconds": -1}`:     "setting_out_of_range",
		`{"an_unknown_key": 1}`:                     "unknown_setting",
	} {
		answer := harness.PostJSON(surface.PathSettings, body, token, "")
		var refusal surface.Refusal
		decode(t, answer, &refusal)
		if answer.StatusCode != http.StatusBadRequest || refusal.Condition != want {
			t.Errorf("%s answered %d %+v, want %s", body, answer.StatusCode, refusal, want)
		}
	}
	if harness.Setting("netflow_join_tolerance_seconds") != "5" {
		t.Errorf("a refused write changed the tolerance to %q", harness.Setting("netflow_join_tolerance_seconds"))
	}
}
