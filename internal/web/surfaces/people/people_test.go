package people_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NaejEL/opnview/internal/store/owners"
	"github.com/NaejEL/opnview/internal/store/vocab"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/surfaces/people"
	"github.com/NaejEL/opnview/internal/web/webtest"
)

// The JSON API of C1 over HTTP. The store's own tests carry the rules in depth; these assert that
// each route answers them, with closed codes, behind the session and the CSRF proof.

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

// exampleClient writes one client by MAC and returns its id.
func exampleClient(t *testing.T, harness *webtest.Harness, mac, hostname string) int64 {
	t.Helper()
	client := vocab.Client{Identity: vocab.ClientIdentity{Kind: vocab.IdentityMAC, Key: mac}, MAC: &mac,
		Hostname: &hostname}
	id, err := harness.Store.UpsertClient(context.Background(), client, harness.Clock.Now().Unix()-60)
	if err != nil {
		t.Fatalf("writing a client: %v", err)
	}
	return id
}

// ownership is every client's owner and instant, and every person, as text.
func ownership(t *testing.T, harness *webtest.Harness) string {
	t.Helper()
	var text string
	if err := harness.Store.DB().QueryRowContext(context.Background(), `SELECT
		(SELECT coalesce(string_agg(id || '|' || coalesce(CAST(owner_id AS TEXT), '-') || '|' ||
		        coalesce(CAST(owner_assigned_at AS TEXT), '-'), ',' ORDER BY id), '') FROM client) || ' / ' ||
		(SELECT coalesce(string_agg(id || '|' || display_name, ',' ORDER BY id), '') FROM owner)`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	return text
}

// TestOwnersAreCreatedRenamedDeletedRestoredAndAssignedOverJSON is C-AC1 over the API: each write,
// its refusals with closed codes and no row changed, the undo restoring the original instants, and
// the client listing with an unstable identity listed as such (C-AC2).
func TestOwnersAreCreatedRenamedDeletedRestoredAndAssignedOverJSON(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	token := harness.SessionFormToken()
	post := func(path, body string) *http.Response { return harness.PostJSON(path, body, token, "") }

	laptop := exampleClient(t, harness, "00:00:5e:00:53:01", "laptop.example")
	twin := exampleClient(t, harness, "00:00:5e:00:53:02", "laptop.example")
	phone := exampleClient(t, harness, "02:00:5e:00:53:03", "phone.example")

	created := decode[people.OwnerAnswer](t, post(people.PathOwners, `{"display_name": " Example Person "}`), http.StatusOK)
	if created.Owner.DisplayName != "Example Person" || created.Owner.ClientCount != 0 {
		t.Errorf("created %+v", created.Owner)
	}
	before := ownership(t, harness)
	for body, code := range map[string]string{
		`{"display_name": "  "}`:             owners.RefusalDisplayNameEmpty,
		`{"display_name": "Example Person"}`: owners.RefusalDisplayNameTaken,
		`{"display_name": 3}`:                web.ConditionMalformedBody,
		`{"name": "Example"}`:                web.ConditionMalformedBody,
		`not json`:                           web.ConditionMalformedBody,
	} {
		refusal := decode[people.Refusal](t, post(people.PathOwners, body), http.StatusBadRequest)
		if refusal.Condition != code {
			t.Errorf("%s answered %s, want %s", body, refusal.Condition, code)
		}
	}
	if after := ownership(t, harness); after != before {
		t.Errorf("a refused creation changed rows: %s -> %s", before, after)
	}

	renamePath := fmt.Sprintf("/api/owners/%d/rename", created.Owner.ID)
	renamed := decode[people.OwnerAnswer](t, post(renamePath, `{"display_name": "Example Renamed"}`), http.StatusOK)
	if renamed.Owner.DisplayName != "Example Renamed" {
		t.Errorf("renamed %+v", renamed.Owner)
	}
	if refusal := decode[people.Refusal](t, post("/api/owners/999/rename", `{"display_name": "X"}`),
		http.StatusNotFound); refusal.Condition != owners.RefusalUnknownOwner {
		t.Errorf("renaming nobody answered %s", refusal.Condition)
	}

	assignPath := fmt.Sprintf("/api/owners/%d/assign", created.Owner.ID)
	assigned := decode[people.AssignmentsAnswer](t, post(assignPath, fmt.Sprintf(`{"clients": [%d, %d]}`, laptop, phone)),
		http.StatusOK)
	if len(assigned.Assignments) != 2 || !assigned.Assignments[0].Changed || *assigned.Assignments[0].OwnerID != created.Owner.ID {
		t.Errorf("the assignment answered %+v", assigned)
	}
	before = ownership(t, harness)
	if refusal := decode[people.Refusal](t, post(assignPath, fmt.Sprintf(`{"clients": [%d, 999]}`, twin)),
		http.StatusBadRequest); refusal.Condition != owners.RefusalUnknownClient || refusal.Client != 999 {
		t.Errorf("an unknown client answered %+v", refusal)
	}
	if after := ownership(t, harness); after != before {
		t.Errorf("a refused assignment changed rows: %s -> %s", before, after)
	}

	listed := decode[people.ClientsAnswer](t, harness.Get(people.PathClients), http.StatusOK)
	if len(listed.Clients) != 3 {
		t.Fatalf("the listing holds %d clients", len(listed.Clients))
	}
	for _, client := range listed.Clients {
		owned := client.ID == laptop || client.ID == phone
		if owned != (client.Owner != nil) || client.UnstableIdentity != (client.ID == phone) {
			t.Errorf("client %d is listed as %+v", client.ID, client)
		}
		if owned && (*client.Owner.DisplayName != "Example Renamed" || client.OwnerAssignedAt == nil) {
			t.Errorf("client %d lists the owner %+v", client.ID, client.Owner)
		}
	}
	owners := decode[people.OwnersAnswer](t, harness.Get(people.PathOwners), http.StatusOK)
	if len(owners.Owners) != 1 || owners.Owners[0].ClientCount != 2 {
		t.Errorf("the people are listed as %+v", owners)
	}

	unassigned := decode[people.AssignmentsAnswer](t, post(people.PathClientsUnassign, fmt.Sprintf(`{"clients": [%d]}`, phone)),
		http.StatusOK)
	if !unassigned.Assignments[0].Changed || unassigned.Assignments[0].OwnerID != nil {
		t.Errorf("the unassignment answered %+v", unassigned)
	}
	var laptopAt int64
	if err := harness.Store.DB().QueryRow("SELECT owner_assigned_at FROM client WHERE id = $1", laptop).Scan(&laptopAt); err != nil {
		t.Fatal(err)
	}

	deletePath := fmt.Sprintf("/api/owners/%d/delete", created.Owner.ID)
	deleted := decode[people.DeletedAnswer](t, post(deletePath, `{}`), http.StatusOK)
	if len(deleted.Cleared) != 1 || deleted.Cleared[0] != (people.Assignment{Client: laptop, OwnerAssignedAt: laptopAt}) {
		t.Errorf("the deletion answered %+v", deleted)
	}
	// The answer of a deletion is the body of its undo, whatever happened in between.
	harness.Clock.Advance(120 * time.Second)
	undoBody, _ := json.Marshal(map[string]any{"display_name": deleted.DisplayName, "cleared": deleted.Cleared})
	restored := decode[people.RestoredAnswer](t, post(people.PathOwnerRestore, string(undoBody)), http.StatusOK)
	if len(restored.Restored) != 1 || restored.Restored[0].OwnerAssignedAt != laptopAt || len(restored.NotRestored) != 0 ||
		restored.Owner.DisplayName != "Example Renamed" {
		t.Errorf("the undo answered %+v", restored)
	}
	var restoredAt int64
	if err := harness.Store.DB().QueryRow("SELECT owner_assigned_at FROM client WHERE id = $1", laptop).Scan(&restoredAt); err != nil {
		t.Fatal(err)
	}
	if restoredAt != laptopAt {
		t.Errorf("the undo restored the instant %d, not the original %d", restoredAt, laptopAt)
	}
	if refusal := decode[people.Refusal](t, post(people.PathOwnerRestore, `{"display_name": "Y", "cleared": [{"client": 1}]}`),
		http.StatusBadRequest); refusal.Condition != web.ConditionMalformedBody {
		t.Errorf("a malformed undo answered %s", refusal.Condition)
	}
}

// TestEveryPeopleWriteRefusesAMissingOrForeignProofAndChangesNothing is C-AC4 for the people
// routes: no session is a 401, a missing or wrong proof or another origin a 403, each in JSON, and
// no row changes; no GET changes anything.
func TestEveryPeopleWriteRefusesAMissingOrForeignProofAndChangesNothing(t *testing.T) {
	t.Parallel()
	harness := webtest.New(t)
	harness.CompleteSetup()
	token := harness.SessionFormToken()
	client := exampleClient(t, harness, "00:00:5e:00:53:41", "host.example")
	person := decode[people.OwnerAnswer](t, harness.PostJSON(people.PathOwners, `{"display_name": "Example Person"}`, token, ""),
		http.StatusOK).Owner
	other := decode[people.OwnerAnswer](t, harness.PostJSON(people.PathOwners, `{"display_name": "Example Other"}`, token, ""),
		http.StatusOK).Owner
	if _, err := harness.Store.AssignClients(context.Background(), other.ID, []int64{client}, 100); err != nil {
		t.Fatal(err)
	}
	writes := map[string]string{
		people.PathOwners: `{"display_name": "Example New"}`,
		fmt.Sprintf("/api/owners/%d/rename", person.ID): `{"display_name": "Example Renamed"}`,
		fmt.Sprintf("/api/owners/%d/delete", other.ID):  `{}`,
		people.PathOwnerRestore:                         `{"display_name": "Example Back", "cleared": []}`,
		fmt.Sprintf("/api/owners/%d/assign", person.ID): fmt.Sprintf(`{"clients": [%d]}`, client),
		people.PathClientsUnassign:                      fmt.Sprintf(`{"clients": [%d]}`, client),
	}
	before := ownership(t, harness)
	anonymous := webtest.FreshClient(t, harness)
	for path, body := range writes {
		answer := anonymous.PostJSON(path, body, token, "")
		if refusal := decode[people.Refusal](t, answer, http.StatusUnauthorized); refusal.Condition != "no_session" {
			t.Errorf("%s without a session answered %s", path, refusal.Condition)
		}
		for name, attempt := range map[string]func() *http.Response{
			"no proof":       func() *http.Response { return harness.PostJSON(path, body, "", "") },
			"a wrong one":    func() *http.Response { return harness.PostJSON(path, body, token+"0", "") },
			"another origin": func() *http.Response { return harness.PostJSON(path, body, token, "https://elsewhere.example") },
		} {
			if refusal := decode[people.Refusal](t, attempt(), http.StatusForbidden); refusal.Condition != web.ConditionCSRFRefused {
				t.Errorf("%s with %s answered %s", path, name, refusal.Condition)
			}
		}
	}
	for _, path := range []string{people.PathOwners, people.PathClients} {
		decode[map[string]any](t, harness.Get(path), http.StatusOK)
		if answer := anonymous.Get(path); answer.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s without a session answered %d", path, answer.StatusCode)
		}
	}
	if after := ownership(t, harness); after != before {
		t.Errorf("a refused write or a GET changed rows: %s -> %s", before, after)
	}
	if !strings.Contains(before, "Example Other") {
		t.Fatalf("the fixture is not what the test assumes: %s", before)
	}
}
