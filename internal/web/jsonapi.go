package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/NaejEL/opnview/internal/widget"
)

// The JSON API's shared pieces (docs/widget-api.md, "Settings API" and "Write endpoints").
//
// EVERY PAGE HAS A JSON EQUIVALENT, for the step-7 application (C5 of the backend-completion
// cycle). A surface answers its JSON routes beside its page routes, with the same validation, the
// same access level and the same CSRF rule, and with closed codes rather than catalogue sentences:
// a refusal a page draws as a catalogue key is answered as that key's last part
// (ConditionOf), the identifier an interface translates.

// The conditions the JSON API answers that are not a page's catalogue key.
const (
	// ConditionMalformedBody is a write whose body is not one JSON object.
	ConditionMalformedBody = "malformed_body"
	// ConditionInternalError is a failure of the database or of a stored secret, whose detail
	// goes to the console and never into the answer.
	ConditionInternalError = "internal_error"
	// ConditionSetupClosed refuses a JSON setup once an account exists.
	ConditionSetupClosed = "setup_closed"
	// ConditionKeyFileRefused refuses a JSON reset that did not present the key file.
	ConditionKeyFileRefused = "key_file_refused"
)

// maxJSONBody bounds a JSON write's body.
const maxJSONBody = 64 << 10

// ConditionOf is the closed code of a catalogue key a page refuses or reports with: its last
// dot-separated part, so "error.firewall_url_invalid" is "firewall_url_invalid".
func ConditionOf(key MessageKey) string {
	text := string(key)
	if cut := strings.LastIndexByte(text, '.'); cut >= 0 {
		return text[cut+1:]
	}
	return text
}

// JSONCondition is a refused JSON request: its closed code, and the field it is about when one
// field is the reason.
type JSONCondition struct {
	Condition string `json:"condition"`
	Field     string `json:"field,omitempty"`
}

// WriteJSON answers a JSON route.
func WriteJSON(writer http.ResponseWriter, status int, body any) {
	widget.WriteJSON(writer, status, body)
}

// RefuseJSON answers a refusal with its closed code and, when not empty, its field.
func RefuseJSON(writer http.ResponseWriter, status int, condition, field string) {
	WriteJSON(writer, status, JSONCondition{Condition: condition, Field: field})
}

// FailJSON answers 500 with the internal-error condition, and the console gets the detail.
func FailJSON(writer http.ResponseWriter, request *http.Request, err error) {
	fmt.Fprintf(os.Stderr, "opnview: web: %s %s: %v\n", request.Method, request.URL.Path, err)
	RefuseJSON(writer, http.StatusInternalServerError, ConditionInternalError, "")
}

// DecodeJSONObject reads a body that is exactly one JSON object, its numbers kept as written,
// within the JSON API's bound.
func DecodeJSONObject(body io.Reader) (map[string]any, bool) {
	raw, err := io.ReadAll(io.LimitReader(body, maxJSONBody+1))
	if err != nil || len(raw) > maxJSONBody {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return object, true
}

// JSONText is one member of a decoded object as the text a form field would carry: a string as
// it is, a number as it was written, a boolean as "true" or "false". sent is false when the member
// is absent or null; a member of any other shape is sent as text no rule accepts, so it is refused
// by the same validation a page's mistyped field is.
func JSONText(object map[string]any, key string) (text string, sent bool) {
	value, present := object[key]
	if !present || value == nil {
		return "", false
	}
	switch typed := value.(type) {
	case string:
		return typed, true
	case json.Number:
		return typed.String(), true
	case bool:
		if typed {
			return "true", true
		}
		return "false", true
	}
	return "\x00", true
}

// FormToken is the form token a request's reader writes with: the session's own, or, with no
// session, the one in this browser's own cookie, set now when there is none. It is what a page's
// form carries, and what a JSON write sends in the X-CSRF-Token header.
func (s *Server) FormToken(writer http.ResponseWriter, request *http.Request) (string, error) {
	return s.formToken(writer, request, sessionFrom(request))
}

// keyFileFromJSON reads the key file a JSON reset presents in its body's "key_file" member, and
// returns the request with its body intact for the handler.
func keyFileFromJSON(request *http.Request) (string, *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(request.Body, maxJSONBody+1))
	request.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil || len(raw) > maxJSONBody {
		return "", request
	}
	var presented struct {
		KeyFile string `json:"key_file"`
	}
	if json.Unmarshal(raw, &presented) != nil {
		return "", request
	}
	return presented.KeyFile, request
}
