//go:build review

// THE REVIEW MODE -- A DEVELOPMENT BUILD ONLY.
//
// A binary built with the `review` tag takes one more flag, --review-answers, naming the widget API's
// recorded answers (internal/widget/widgets/testdata/golden_responses.json.gz, recorded from the real
// handlers over a synthesised fixture). With it, GET /api/widget/{type} answers a recorded answer
// instead of reading the database, so a review instance on a scratch data directory draws every widget
// without a firewall. Every other route is the server's own.
//
// IT IS NOT IN THE PRODUCT. A build without the tag compiles review_off.go instead: no flag, no code
// path, no reference to the file. The serve compose file selects it with OPNVIEW_BUILD_FLAGS=-tags=review
// (docker-compose.serve.yml, header).
//
// THE SESSION STILL APPLIES. A widget request is first served by the real route; when that answers 401
// or 403 -- no session, a session expired -- the refusal is passed on unchanged, and only an authorised
// request gets the recorded answer. Which one: the recorded answer asked with the same parameters, else
// the type's first answer carrying data, else its first. A type with no recorded answer (a front-only
// widget, an unknown type) keeps the real route's answer.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/NaejEL/opnview/internal/buildinfo"
)

var reviewAnswersPath = flag.String("review-answers", "",
	"REVIEW BUILD ONLY: the recorded widget answers (golden_responses.json.gz) GET /api/widget/{type} answers "+
		"from instead of the database. Empty is off")

// recordedAnswer is one recorded answer: the parameters it was asked with, decoded, and its body.
type recordedAnswer struct {
	parameters any
	body       []byte
	drawable   bool
}

// reviewHandler wraps the server with the recorded answers when --review-answers names them.
func reviewHandler(server http.Handler) (http.Handler, error) {
	if *reviewAnswersPath == "" {
		return server, nil
	}
	answers, err := readRecordedAnswers(*reviewAnswersPath)
	if err != nil {
		return nil, fmt.Errorf("review mode: %w", err)
	}
	fmt.Printf("%s: REVIEW MODE: widget answers are the recorded answers of %s, not the database\n",
		buildinfo.AppName, *reviewAnswersPath)
	return &reviewServer{next: server, answers: answers}, nil
}

// readRecordedAnswers reads the gzipped golden file: an object keyed "<type> <parameters JSON>", each value
// the answer's JSON or that JSON as a string.
func readRecordedAnswers(path string) (map[string][]recordedAnswer, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	answers := map[string][]recordedAnswer{}
	for _, key := range keys {
		body := []byte(entries[key])
		var text string
		if json.Unmarshal(body, &text) == nil {
			body = []byte(text)
		}
		var answer struct {
			Data      json.RawMessage `json:"data"`
			Condition string          `json:"condition"`
		}
		if err := json.Unmarshal(body, &answer); err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		widgetType, parameters, _ := strings.Cut(key, " ")
		recorded := recordedAnswer{body: body, parameters: decodeParameters(parameters),
			drawable: len(answer.Data) > 0 && !bytes.Equal(answer.Data, []byte("null")) &&
				answer.Condition != "reachable_no_rows"}
		answers[widgetType] = append(answers[widgetType], recorded)
	}
	return answers, nil
}

// decodeParameters decodes a parameters text; empty is the empty object.
func decodeParameters(text string) any {
	if strings.TrimSpace(text) == "" {
		return map[string]any{}
	}
	var decoded any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return text
	}
	return decoded
}

type reviewServer struct {
	next    http.Handler
	answers map[string][]recordedAnswer
}

func (s *reviewServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	widgetType, isWidget := strings.CutPrefix(request.URL.Path, "/api/widget/")
	if request.Method != http.MethodGet || !isWidget || widgetType == "" || strings.Contains(widgetType, "/") {
		s.next.ServeHTTP(writer, request)
		return
	}
	// The real route answers first, so its session check holds.
	recorder := httptest.NewRecorder()
	s.next.ServeHTTP(recorder, request)
	chosen := s.choose(widgetType, request.URL.Query().Get("parameters"))
	if chosen == nil || recorder.Code == http.StatusUnauthorized || recorder.Code == http.StatusForbidden {
		for name, values := range recorder.Header() {
			writer.Header()[name] = values
		}
		writer.WriteHeader(recorder.Code)
		_, _ = writer.Write(recorder.Body.Bytes())
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(chosen)
}

// choose is the recorded answer asked with the same parameters, else the type's first with data, else its
// first; nil when the type has none.
func (s *reviewServer) choose(widgetType, parameters string) []byte {
	recorded := s.answers[widgetType]
	if len(recorded) == 0 {
		return nil
	}
	wanted := decodeParameters(parameters)
	for _, answer := range recorded {
		if reflect.DeepEqual(answer.parameters, wanted) {
			return answer.body
		}
	}
	for _, answer := range recorded {
		if answer.drawable {
			return answer.body
		}
	}
	return recorded[0].body
}
