package dashboards_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/web/surfaces/dashboards"
	"github.com/NaejEL/opnview/internal/web/webtest"
)

// The dashboard API over HTTP (F7-AC9 of specs/SPEC-step-7-frontend.md). The validator's own
// conformance is internal/dashboard's test; these assert that every route answers it, stores a
// document as the text it was sent, and sits behind the session and the CSRF proof.

// conformance is the directory of the shared fixtures.
func conformance(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("the test cannot locate its own file")
	}
	return filepath.Join(filepath.Dir(here), "..", "..", "..", "..", "testdata", "dashboards", "conformance")
}

// fixture reads one document of the shared fixtures.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(conformance(t), "documents", name))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSpace(raw)
}

// signedIn is a server with an account, signed in, and its form token.
func signedIn(t *testing.T) (*webtest.Harness, string) {
	t.Helper()
	harness := webtest.New(t)
	harness.CompleteSetup()
	return harness, harness.SessionFormToken()
}

// answered reads an answer whole and fails on any other status than want.
func answered(t *testing.T, answer *http.Response, want int) []byte {
	t.Helper()
	status, body := webtest.StatusAndBody(t, answer)
	if status != want {
		t.Fatalf("answered %d, want %d: %.600s", status, want, body)
	}
	return []byte(body)
}

// decoded decodes a JSON answer.
func decoded(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("the answer is not a JSON object: %v: %.400s", err, raw)
	}
	return value
}

// documentText is the text of an answer's document member: the answer's last member, spliced.
func documentText(t *testing.T, raw []byte) []byte {
	t.Helper()
	marker := []byte(`"document":`)
	cut := bytes.Index(raw, marker)
	if cut < 0 || raw[len(raw)-1] != '}' {
		t.Fatalf("the answer carries no document member: %.400s", raw)
	}
	return raw[cut+len(marker) : len(raw)-1]
}

// envelope is a create's body around a document's text.
func envelope(document []byte) string {
	return `{"document": ` + string(document) + `, "origin": "imported", "source_name": "review.json"}`
}

// table is the whole dashboard table as text, to show that a request wrote nothing.
func table(t *testing.T, harness *webtest.Harness) string {
	t.Helper()
	var text string
	if err := harness.Store.DB().QueryRowContext(context.Background(), `SELECT coalesce(string_agg(
		id || '|' || position || '|' || revision || '|' || origin || '|' || coalesce(source_name, '-') || '|' ||
		CAST(document AS TEXT) || '|' || created_at || '|' || updated_at, ',' ORDER BY id), '') FROM dashboard`).
		Scan(&text); err != nil {
		t.Fatal(err)
	}
	return text
}

// TestEveryDocumentRefusalIsAnsweredWithItsIndicesAndFieldAndStoresNothing: each refusing fixture,
// sent as a new dashboard, answers 400 with the fixture's own refusal, and nothing is stored.
func TestEveryDocumentRefusalIsAnsweredWithItsIndicesAndFieldAndStoresNothing(t *testing.T) {
	t.Parallel()
	harness, token := signedIn(t)
	raw, err := os.ReadFile(filepath.Join(conformance(t), "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name       string          `json:"name"`
		File       string          `json:"file"`
		Text       *string         `json:"text"`
		PadToBytes int             `json:"pad_to_bytes"`
		Expect     json.RawMessage `json:"expect"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	refusing := 0
	for _, entry := range cases {
		if string(entry.Expect) == "null" {
			continue
		}
		refusing++
		var document []byte
		if entry.Text != nil {
			document = []byte(*entry.Text)
		} else {
			document = fixture(t, entry.File)
		}
		if entry.PadToBytes > 0 {
			// Spaces after a value are not part of the body's document member; the size bound is
			// held by TestADocumentAboveOneMebibyteIsRefused instead.
			continue
		}
		answer := answered(t, harness.PostJSON(dashboards.PathDashboards, envelope(document), token, ""),
			http.StatusBadRequest)
		var got, want any
		if err := json.Unmarshal(answer, &got); err != nil {
			t.Fatalf("%s: %v", entry.Name, err)
		}
		if err := json.Unmarshal(entry.Expect, &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: answered %v, want %v", entry.Name, got, want)
		}
	}
	if refusing < 20 {
		t.Fatalf("only %d refusing fixtures were read", refusing)
	}
	if stored := table(t, harness); stored != "" {
		t.Errorf("a refused document was stored: %.300s", stored)
	}
}

// TestADocumentAboveOneMebibyteIsRefused: a document whose text is one byte above 1 MiB is refused
// document_too_large and nothing is stored; one of exactly 1 MiB is stored.
func TestADocumentAboveOneMebibyteIsRefused(t *testing.T) {
	t.Parallel()
	harness, token := signedIn(t)
	sized := func(size int) []byte {
		head := `{"format_version": 1, "canvases": [{"id": "c", "title": "C"}], "description": "`
		return []byte(head + strings.Repeat("x", size-len(head)-2) + `"}`)
	}
	refusal := decoded(t, answered(t, harness.PostJSON(dashboards.PathDashboards, envelope(sized(1<<20+1)), token, ""),
		http.StatusBadRequest))
	if refusal["condition"] != "document_too_large" {
		t.Errorf("a document above 1 MiB answered %v", refusal)
	}
	if stored := table(t, harness); stored != "" {
		t.Errorf("a refused document was stored")
	}
	answered(t, harness.PostJSON(dashboards.PathDashboards, envelope(sized(1<<20)), token, ""), http.StatusOK)
	answered(t, harness.PostJSON(dashboards.PathDashboards, envelope(sized(2<<20)), token, ""), http.StatusBadRequest)
}

// TestADocumentIsStoredAndReadBackByteForByte:unknown keys, member order and the spacing of the
// text sent come back unchanged, in a create's answer, a read and a save's.
func TestADocumentIsStoredAndReadBackByteForByte(t *testing.T) {
	t.Parallel()
	harness, token := signedIn(t)
	document := fixture(t, "unknown-keys-member-order.json")
	created := answered(t, harness.PostJSON(dashboards.PathDashboards, envelope(document), token, ""), http.StatusOK)
	if got := documentText(t, created); !bytes.Equal(got, document) {
		t.Errorf("the create answered the document as\n%s\nnot as it was sent\n%s", got, document)
	}
	head := decoded(t, created)
	id := int64(head["id"].(float64))
	if head["origin"] != "imported" || head["source_name"] != "review.json" || head["revision"] != float64(1) {
		t.Errorf("the create answered %v", head)
	}
	path := "/api/dashboards/" + strconv.FormatInt(id, 10)
	read := answered(t, harness.Get(path), http.StatusOK)
	if got := documentText(t, read); !bytes.Equal(got, document) {
		t.Errorf("the read answered the document as\n%s\nnot as it was sent\n%s", got, document)
	}
	readHead := decoded(t, read)
	for _, member := range []string{"id", "revision", "origin", "source_name", "document"} {
		if _, present := readHead[member]; !present {
			t.Errorf("the read carries no %s", member)
		}
	}

	// The listing names the dashboard by its document's title and lists its canvases.
	listing := decoded(t, answered(t, harness.Get(dashboards.PathDashboards), http.StatusOK))
	listed := listing["dashboards"].([]any)
	if len(listed) != 1 {
		t.Fatalf("the listing holds %v", listed)
	}
	first := listed[0].(map[string]any)
	canvases := first["canvases"].([]any)
	if first["title"] != "Members out of order, unknown keys kept" || len(canvases) != 1 ||
		canvases[0].(map[string]any)["id"] != "order-kept" || canvases[0].(map[string]any)["title"] != "Order kept" {
		t.Errorf("the listing answered %v", first)
	}
	if format := listing["format"].(map[string]any); format["current"] != float64(1) || format["minimum"] != float64(1) {
		t.Errorf("the listing's format is %v", format)
	}

	// A save of another text in the same spirit is stored as it was sent, at the next revision.
	edited := []byte(strings.Replace(string(document), `"title": "Order kept"`, `"title":"Order  still kept"`, 1))
	saved := answered(t, harness.PostJSON(path, `{"revision": 1, "document": `+string(edited)+`}`, token, ""),
		http.StatusOK)
	if got := documentText(t, saved); !bytes.Equal(got, edited) {
		t.Errorf("the save answered the document as\n%s\nnot as it was sent\n%s", got, edited)
	}
	if decoded(t, saved)["revision"] != float64(2) {
		t.Errorf("a save did not answer the next revision: %s", saved)
	}
}

// TestAStaleRevisionIsAConflictAndWritesNothing.
func TestAStaleRevisionIsAConflictAndWritesNothing(t *testing.T) {
	t.Parallel()
	harness, token := signedIn(t)
	document := fixture(t, "canvas-period-and-scope.json")
	id := int64(decoded(t, answered(t, harness.PostJSON(dashboards.PathDashboards, `{"document": `+string(document)+`}`,
		token, ""), http.StatusOK))["id"].(float64))
	path := "/api/dashboards/" + strconv.FormatInt(id, 10)
	answered(t, harness.PostJSON(path, `{"revision": 1, "document": `+string(document)+`}`, token, ""), http.StatusOK)
	before := table(t, harness)
	conflict := decoded(t, answered(t, harness.PostJSON(path, `{"revision": 1, "document": `+string(document)+`}`,
		token, ""), http.StatusConflict))
	if conflict["condition"] != "revision_conflict" || conflict["revision"] != float64(2) {
		t.Errorf("a stale revision answered %v", conflict)
	}
	if after := table(t, harness); after != before {
		t.Errorf("a refused save wrote:\n%s\n%s", before, after)
	}
	for body, field := range map[string]string{`{"document": ` + string(document) + `}`: "revision",
		`{"revision": "2", "document": ` + string(document) + `}`: "revision", `{"revision": 2}`: "document"} {
		refusal := decoded(t, answered(t, harness.PostJSON(path, body, token, ""), http.StatusBadRequest))
		if refusal["condition"] != "malformed_body" || refusal["field"] != field {
			t.Errorf("%.40s answered %v", body, refusal)
		}
	}
	unknown := decoded(t, answered(t, harness.PostJSON("/api/dashboards/999999", `{"revision": 1, "document": `+
		string(document)+`}`, token, ""), http.StatusNotFound))
	if unknown["condition"] != "unknown_dashboard" {
		t.Errorf("an unknown dashboard answered %v", unknown)
	}
	answered(t, harness.Get("/api/dashboards/999999"), http.StatusNotFound)
	answered(t, harness.PostJSON("/api/dashboards/999999/delete", `{}`, token, ""), http.StatusNotFound)
}

// TestAWriteNeedsTheSessionAndItsProof: no token, another session's token and another origin are
// refused 403 csrf_refused; no session is 401. None of them writes.
func TestAWriteNeedsTheSessionAndItsProof(t *testing.T) {
	t.Parallel()
	harness, token := signedIn(t)
	body := `{"document": ` + string(fixture(t, "worked-example.json")) + `}`
	for name, answer := range map[string]*http.Response{
		"no token":      harness.PostJSON(dashboards.PathDashboards, body, "", ""),
		"a wrong token": harness.PostJSON(dashboards.PathDashboards, body, token+"x", ""),
		"another origin": harness.PostJSON(dashboards.PathDashboards, body, token,
			"http://cross-site.example.org"),
	} {
		refusal := decoded(t, answered(t, answer, http.StatusForbidden))
		if refusal["condition"] != "csrf_refused" {
			t.Errorf("%s answered %v", name, refusal)
		}
	}
	anonymous := webtest.FreshClient(t, harness)
	answered(t, anonymous.PostJSON(dashboards.PathDashboards, body, token, ""), http.StatusUnauthorized)
	answered(t, anonymous.Get(dashboards.PathDashboards), http.StatusUnauthorized)
	answered(t, anonymous.Get("/api/dashboards/1"), http.StatusUnauthorized)
	if stored := table(t, harness); stored != "" {
		t.Errorf("a refused write was stored: %.300s", stored)
	}
}

// TestADeletionIsUndoneByRestoringItsAnswer: delete answers the whole dashboard, and restoring that
// answer unchanged recreates it identically, under its own id and at its own position.
func TestADeletionIsUndoneByRestoringItsAnswer(t *testing.T) {
	t.Parallel()
	harness, token := signedIn(t)
	ids := []string{}
	for _, name := range []string{"worked-example.json", "unknown-keys-member-order.json", "canvas-period-and-scope.json"} {
		created := decoded(t, answered(t, harness.PostJSON(dashboards.PathDashboards, envelope(fixture(t, name)), token, ""),
			http.StatusOK))
		ids = append(ids, strconv.FormatInt(int64(created["id"].(float64)), 10))
	}
	answered(t, harness.PostJSON("/api/dashboards/"+ids[1], `{"revision": 1, "document": `+
		string(fixture(t, "unknown-keys-member-order.json"))+`}`, token, ""), http.StatusOK)
	before := table(t, harness)
	deleted := answered(t, harness.PostJSON("/api/dashboards/"+ids[1]+"/delete", `{}`, token, ""), http.StatusOK)
	if table(t, harness) == before {
		t.Fatal("the deletion deleted nothing")
	}
	answered(t, harness.Get("/api/dashboards/"+ids[1]), http.StatusNotFound)
	restored := answered(t, harness.PostJSON(dashboards.PathDashboardRestore, string(deleted), token, ""), http.StatusOK)
	if !bytes.Equal(restored, deleted) {
		t.Errorf("the restore answered\n%s\nnot the deleted dashboard\n%s", restored, deleted)
	}
	if after := table(t, harness); after != before {
		t.Errorf("the restored table differs:\n%s\n%s", before, after)
	}
	for body, field := range map[string]string{`{"document": {}}`: "id",
		strings.Replace(string(deleted), `"origin":"imported"`, `"origin":"invented"`, 1): "origin"} {
		refusal := decoded(t, answered(t, harness.PostJSON(dashboards.PathDashboardRestore, body, token, ""),
			http.StatusBadRequest))
		if refusal["condition"] != "malformed_body" || refusal["field"] != field {
			t.Errorf("a restore of %.60s answered %v", body, refusal)
		}
	}

	// The order: every id once, else refused; an id naming nothing is unknown.
	ordered := decoded(t, answered(t, harness.PostJSON(dashboards.PathDashboardOrder,
		`{"ids": [`+ids[2]+`, `+ids[0]+`, `+ids[1]+`]}`, token, ""), http.StatusOK))
	if got, _ := json.Marshal(ordered["ids"]); string(got) != "["+ids[2]+","+ids[0]+","+ids[1]+"]" {
		t.Errorf("the order answered %s", got)
	}
	answered(t, harness.PostJSON(dashboards.PathDashboardOrder, `{"ids": [`+ids[0]+`]}`, token, ""), http.StatusBadRequest)
	answered(t, harness.PostJSON(dashboards.PathDashboardOrder, `{"ids": [`+ids[0]+`, `+ids[0]+`, `+ids[1]+`]}`, token, ""),
		http.StatusBadRequest)
	answered(t, harness.PostJSON(dashboards.PathDashboardOrder, `{"ids": [`+ids[0]+`, `+ids[1]+`, 999999]}`, token, ""),
		http.StatusNotFound)
}

// TestAReadChangesNothingAndTheReadRoleCannotReadDashboards.
func TestAReadChangesNothingAndTheReadRoleCannotReadDashboards(t *testing.T) {
	t.Parallel()
	harness, token := signedIn(t)
	created := decoded(t, answered(t, harness.PostJSON(dashboards.PathDashboards,
		envelope(fixture(t, "worked-example.json")), token, ""), http.StatusOK))
	before := table(t, harness)
	answered(t, harness.Get(dashboards.PathDashboards), http.StatusOK)
	answered(t, harness.Get("/api/dashboards/"+strconv.FormatInt(int64(created["id"].(float64)), 10)), http.StatusOK)
	answered(t, harness.Get("/api/dashboards/not-a-number"), http.StatusNotFound)
	if after := table(t, harness); after != before {
		t.Errorf("a read wrote:\n%s\n%s", before, after)
	}
	if _, err := harness.Store.Reader.DB.QueryContext(context.Background(), "SELECT * FROM dashboard"); err == nil ||
		!strings.Contains(err.Error(), "42501") {
		t.Errorf("the read role reads the dashboards: %v", err)
	}
}
