package labels_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/NaejEL/opnview/internal/store/owners"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/labels"
	"github.com/NaejEL/opnview/internal/web/webtest"
)

// decode reads a JSON answer into a value, failing on any other status than want.
func decode[T any](t *testing.T, answer *http.Response, want int) T {
	t.Helper()
	body := webtest.ReadBody(t, answer)
	if answer.StatusCode != want {
		t.Fatalf("answered %d, want %d: %.400s", answer.StatusCode, want, body)
	}
	var value T
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		t.Fatalf("the answer is not JSON: %v: %.400s", err, body)
	}
	return value
}

// seed writes one interface and one blocklist, and returns their ids.
func seed(t *testing.T, harness *webtest.Harness) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	at := harness.Clock.Now().Unix() - 60
	iface, err := harness.Store.UpsertInterface(ctx, vocab.Interface{Identifier: "example_if_0", Device: "exdev0",
		Description: "example description", LinkType: "example-link", LinkKind: "other"}, at)
	if err != nil {
		t.Fatal(err)
	}
	list, err := harness.Store.UpsertBlocklist(ctx, "an observed list name", at)
	if err != nil {
		t.Fatal(err)
	}
	return iface, list
}

// rows is the label and the purpose as text.
func rows(t *testing.T, harness *webtest.Harness) string {
	t.Helper()
	var text string
	if err := harness.Store.DB().QueryRowContext(context.Background(), `SELECT
		(SELECT coalesce(string_agg(id || '|' || coalesce(user_label, '-'), ','), '') FROM interface) || ' / ' ||
		(SELECT coalesce(string_agg(id || '|' || coalesce(purpose, '-') || '|' ||
		        coalesce(CAST(purpose_assigned_at AS TEXT), '-'), ','), '') FROM blocklist)`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	return text
}

// TestALabelAndAPurposeAreSetAndClearedOverJSON is C1's labels and purposes over the API, and
// C-AC6's refusal: an out-of-vocabulary purpose is refused with a closed code and changes nothing.
func TestALabelAndAPurposeAreSetAndClearedOverJSON(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	token := harness.SessionFormToken()
	iface, list := seed(t, harness)
	labelPath := fmt.Sprintf("/api/interfaces/%d/label", iface)
	purposePath := fmt.Sprintf("/api/blocklists/%d/purpose", list)

	labelled := decode[labels.InterfaceAnswer](t, harness.PostJSON(labelPath, `{"user_label": " Example label "}`, token, ""),
		http.StatusOK)
	if labelled.Interface.UserLabel == nil || *labelled.Interface.UserLabel != "Example label" {
		t.Errorf("the label answered %+v", labelled)
	}
	listed := decode[labels.BlocklistsAnswer](t, harness.Get(labels.PathBlocklists), http.StatusOK)
	if len(listed.Blocklists) != 1 || listed.Blocklists[0].Purpose != nil || len(listed.Purposes) != len(owners.BlocklistPurposes) {
		t.Errorf("the listing answered %+v", listed)
	}
	assigned := decode[labels.BlocklistAnswer](t, harness.PostJSON(purposePath, `{"purpose": "threat"}`, token, ""),
		http.StatusOK)
	if assigned.Blocklist.Purpose == nil || *assigned.Blocklist.Purpose != "threat" ||
		*assigned.Blocklist.PurposeAssignedAt != harness.Clock.Now().Unix() {
		t.Errorf("the purpose answered %+v", assigned)
	}

	before := rows(t, harness)
	for _, refused := range []struct {
		path, body, condition string
		status                int
	}{
		{purposePath, `{"purpose": "malware"}`, owners.RefusalPurposeNotInVocabulary, http.StatusBadRequest},
		{purposePath, `{"purpose": 3}`, web.ConditionMalformedBody, http.StatusBadRequest},
		{purposePath, `{}`, web.ConditionMalformedBody, http.StatusBadRequest},
		{"/api/blocklists/999/purpose", `{"purpose": "threat"}`, owners.RefusalUnknownBlocklist, http.StatusNotFound},
		{labelPath, `{"user_label": "  "}`, owners.RefusalLabelEmpty, http.StatusBadRequest},
		{labelPath, `{"user_label": "a", "other": 1}`, web.ConditionMalformedBody, http.StatusBadRequest},
		{"/api/interfaces/999/label", `{"user_label": "a"}`, owners.RefusalUnknownInterface, http.StatusNotFound},
		{"/api/interfaces/x/label", `{"user_label": "a"}`, owners.RefusalUnknownInterface, http.StatusNotFound},
	} {
		refusal := decode[labels.Refusal](t, harness.PostJSON(refused.path, refused.body, token, ""), refused.status)
		if refusal.Condition != refused.condition {
			t.Errorf("%s %s answered %s, want %s", refused.path, refused.body, refusal.Condition, refused.condition)
		}
	}
	if after := rows(t, harness); after != before {
		t.Errorf("a refused write changed rows: %s -> %s", before, after)
	}

	decode[labels.InterfaceAnswer](t, harness.PostJSON(labelPath, `{"user_label": null}`, token, ""), http.StatusOK)
	decode[labels.BlocklistAnswer](t, harness.PostJSON(purposePath, `{"purpose": null}`, token, ""), http.StatusOK)
	if cleared := rows(t, harness); cleared != fmt.Sprintf("%d|- / %d|-|-", iface, list) {
		t.Errorf("after clearing the rows read %s", cleared)
	}
}

// TestEveryLabelWriteRefusesAMissingOrForeignProofAndChangesNothing is C-AC4 for the label and
// purpose routes.
func TestEveryLabelWriteRefusesAMissingOrForeignProofAndChangesNothing(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	token := harness.SessionFormToken()
	iface, list := seed(t, harness)
	writes := map[string]string{
		fmt.Sprintf("/api/interfaces/%d/label", iface):  `{"user_label": "Example label"}`,
		fmt.Sprintf("/api/blocklists/%d/purpose", list): `{"purpose": "threat"}`,
	}
	before := rows(t, harness)
	anonymous := webtest.FreshClient(t, harness)
	for path, body := range writes {
		if refusal := decode[labels.Refusal](t, anonymous.PostJSON(path, body, token, ""), http.StatusUnauthorized); refusal.Condition != "no_session" {
			t.Errorf("%s without a session answered %s", path, refusal.Condition)
		}
		for name, attempt := range map[string]func() *http.Response{
			"no proof":       func() *http.Response { return harness.PostJSON(path, body, "", "") },
			"a wrong one":    func() *http.Response { return harness.PostJSON(path, body, token+"0", "") },
			"another origin": func() *http.Response { return harness.PostJSON(path, body, token, "https://elsewhere.example") },
		} {
			if refusal := decode[labels.Refusal](t, attempt(), http.StatusForbidden); refusal.Condition != web.ConditionCSRFRefused {
				t.Errorf("%s with %s answered %s", path, name, refusal.Condition)
			}
		}
	}
	decode[labels.BlocklistsAnswer](t, harness.Get(labels.PathBlocklists), http.StatusOK)
	if answer := anonymous.Get(labels.PathBlocklists); answer.StatusCode != http.StatusUnauthorized {
		t.Errorf("the listing without a session answered %d", answer.StatusCode)
	}
	if after := rows(t, harness); after != before {
		t.Errorf("a refused write or a GET changed rows: %s -> %s", before, after)
	}
}
