package settingstest

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/NaejEL/opnview/internal/web/webtest"
)

// The JSON side of the Settings API (docs/widget-api.md, "Settings API"), as the step-7
// application uses it.

// APIToken is the form token GET /api/session hands this reader: the session's own, or, with no
// session, the one in this browser's cookie, which the answer sets.
func APIToken(h *webtest.Harness) string {
	h.T.Helper()
	state := GetJSON(h, "/api/session")
	token, _ := state["csrf_token"].(string)
	if token == "" {
		h.T.Fatalf("GET /api/session handed no form token: %v", state)
	}
	return token
}

// GetJSON issues a GET and decodes its JSON answer, failing unless it is 200.
func GetJSON(h *webtest.Harness, path string) map[string]any {
	h.T.Helper()
	answer := h.Get(path)
	defer func() { _ = answer.Body.Close() }()
	body := webtest.ReadBody(h.T, answer)
	if answer.StatusCode != http.StatusOK {
		h.T.Fatalf("GET %s answered %d: %s", path, answer.StatusCode, body)
	}
	return DecodeJSON(h.T, body)
}

// SendJSON issues a JSON write carrying this reader's form token, as the application makes one,
// and returns its status and decoded answer.
func SendJSON(h *webtest.Harness, path string, body any) (int, map[string]any) {
	h.T.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		h.T.Fatalf("encoding a JSON body for %s: %v", path, err)
	}
	answer := h.PostJSON(path, string(encoded), APIToken(h), "")
	defer func() { _ = answer.Body.Close() }()
	return answer.StatusCode, DecodeJSON(h.T, webtest.ReadBody(h.T, answer))
}

// DecodeJSON decodes one JSON object.
func DecodeJSON(t *testing.T, text string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("the answer is not one JSON object (%v): %s", err, text)
	}
	return decoded
}

// Path reads one member of a decoded answer by its dot-separated path, an integer part
// indexing an array; nil when it is absent.
func Path(value any, path ...any) any {
	for _, part := range path {
		switch key := part.(type) {
		case string:
			object, ok := value.(map[string]any)
			if !ok {
				return nil
			}
			value = object[key]
		case int:
			list, ok := value.([]any)
			if !ok || key < 0 || key >= len(list) {
				return nil
			}
			value = list[key]
		}
	}
	return value
}
