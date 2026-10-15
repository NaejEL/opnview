package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The closed outcomes of a fetch. The failures are the availability outcomes the MaxMind and
// Public Suffix List refreshes always reported, so a plugin passes them straight on.
const (
	// OutcomeFetched is a body written and in place.
	OutcomeFetched = "fetched"
	// OutcomeNotModified is a 304: the copy held is current and was kept.
	OutcomeNotModified = "not_modified"
	// OutcomeAnswered is a HEAD that answered.
	OutcomeAnswered = "answered"
	// OutcomeRefused is a 401 or a 403: the credentials were refused.
	OutcomeRefused = "download_refused"
	// OutcomeLimited is a 429: a download limit is spent.
	OutcomeLimited = "download_limit_reached"
	// OutcomeFailed is any other failure: the network, another status, a body too large.
	OutcomeFailed = "download_failed"
	// OutcomeMalformed is a body the plugin's check refused.
	OutcomeMalformed = "download_malformed"
	// OutcomeHostRefused is a request, or a redirect, to a host the manifest does not declare.
	OutcomeHostRefused = "host_not_declared"
)

// ErrHostNotDeclared is a request to a host the plugin's manifest does not declare.
var ErrHostNotDeclared = errors.New("fetch: the host is not one the plugin's manifest declares")

// Facility fetches for one plugin and holds its data directory.
type Facility struct {
	plugin       string
	dir          string
	destinations map[string]bool
	client       *http.Client
}

// DataDirectory is the directory a plugin's files live in: <data-dir>/plugin-data/<plugin>.
func DataDirectory(dataDir, plugin string) string {
	return filepath.Join(dataDir, "plugin-data", plugin)
}

// New returns the facility of one plugin. destinations are its declared destinations, each a
// scheme and a host (such as https://publicsuffix.org); dir is its data directory, created on first use;
// transport is what the requests are sent through, nil for the product's own -- a test passes one
// that answers locally and counts what is sent and where.
func New(plugin, dir string, destinations []string, timeout time.Duration, transport http.RoundTripper) *Facility {
	declared := map[string]bool{}
	for _, destination := range destinations {
		declared[strings.TrimRight(destination, "/")] = true
	}
	facility := &Facility{plugin: plugin, dir: dir, destinations: declared}
	inner := transport
	if inner == nil {
		inner = &http.Transport{Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 10 * time.Second}
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	facility.client = &http.Client{
		Transport: &guard{facility: facility, inner: inner},
		Timeout:   timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if !facility.declared(request.URL) {
				return ErrHostNotDeclared
			}
			if len(via) >= 5 {
				return errors.New("fetch: too many redirects")
			}
			return nil
		},
	}
	return facility
}

// declared reports whether a URL's scheme and host are a declared destination.
func (f *Facility) declared(target *url.URL) bool {
	return f.destinations[target.Scheme+"://"+target.Host]
}

// headersKey carries a request's own headers and the host they belong to, to the transport.
type headersKey struct{}

type ownHeaders struct {
	host    string
	headers map[string]string
}

// guard refuses an undeclared host, and adds the plugin's headers to a request for the host the
// plugin asked and to no other: a redirect never carries its credentials elsewhere.
type guard struct {
	facility *Facility
	inner    http.RoundTripper
}

func (g *guard) RoundTrip(request *http.Request) (*http.Response, error) {
	if !g.facility.declared(request.URL) {
		return nil, &url.Error{Op: request.Method, URL: request.URL.Redacted(), Err: ErrHostNotDeclared}
	}
	if own, ok := request.Context().Value(headersKey{}).(ownHeaders); ok && own.host == request.URL.Host {
		request = request.Clone(request.Context())
		for name, value := range own.headers {
			request.Header.Set(name, value)
		}
	}
	return g.inner.RoundTrip(request)
}

// Request is one fetch.
type Request struct {
	// URL is what is fetched.
	URL string
	// Method is GET or HEAD; empty is GET.
	Method string
	// Headers are set on the request to URL's host only.
	Headers map[string]string
	// File is the name, in the data directory, the body is written to; a GET needs one.
	File string
	// MaxBytes bounds the body; a GET needs one.
	MaxBytes int64
	// Conditional sends the validators of the copy held, when there is one.
	Conditional bool
	// Check is handed the complete body's temporary name and decides whether it replaces the
	// copy held. Nil accepts every complete body.
	Check func(temporary string) (bool, string)
}

// Result is what one fetch did.
type Result struct {
	Outcome      string `json:"outcome"`
	StatusCode   int    `json:"status_code"`
	Detail       string `json:"detail"`
	LastModified string `json:"last_modified"`
	ETag         string `json:"etag"`
}

// validators are what is kept beside a file: the validators the server sent with it.
type validators struct {
	ETag         string `json:"etag"`
	LastModified string `json:"last_modified"`
}

// Fetch makes one request.
func (f *Facility) Fetch(ctx context.Context, request Request) Result {
	target, err := url.Parse(request.URL)
	if err != nil || (target.Scheme != "https" && target.Scheme != "http") || target.Host == "" {
		return Result{Outcome: OutcomeFailed, Detail: "the URL is not an absolute http or https URL"}
	}
	if !f.declared(target) {
		return Result{Outcome: OutcomeHostRefused, Detail: target.Scheme + "://" + target.Host + " is not declared"}
	}
	method := request.Method
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodHead {
		return Result{Outcome: OutcomeFailed, Detail: "a fetch is a GET or a HEAD"}
	}
	var path string
	if method == http.MethodGet {
		if request.MaxBytes <= 0 {
			return Result{Outcome: OutcomeFailed, Detail: "a GET states the most bytes it may write"}
		}
		if path, err = f.Path(request.File); err != nil {
			return Result{Outcome: OutcomeFailed, Detail: err.Error()}
		}
	}
	built, err := http.NewRequestWithContext(context.WithValue(ctx, headersKey{},
		ownHeaders{host: target.Host, headers: request.Headers}), method, request.URL, nil)
	if err != nil {
		return Result{Outcome: OutcomeFailed, Detail: err.Error()}
	}
	held := validators{}
	if request.Conditional && path != "" {
		if _, err := os.Stat(path); err == nil {
			held = f.readValidators(path)
			if held.ETag != "" {
				built.Header.Set("If-None-Match", held.ETag)
			}
			if held.LastModified != "" {
				built.Header.Set("If-Modified-Since", held.LastModified)
			}
		}
	}
	answer, err := f.client.Do(built)
	if err != nil {
		if errors.Is(err, ErrHostNotDeclared) {
			return Result{Outcome: OutcomeHostRefused, Detail: "a redirect led to a host that is not declared"}
		}
		return Result{Outcome: OutcomeFailed, Detail: "the request did not complete"}
	}
	defer func() { _ = answer.Body.Close() }()
	result := Result{StatusCode: answer.StatusCode, LastModified: answer.Header.Get("Last-Modified"),
		ETag: answer.Header.Get("ETag")}
	switch {
	case answer.StatusCode == http.StatusNotModified && request.Conditional:
		result.Outcome, result.ETag, result.LastModified = OutcomeNotModified, held.ETag, held.LastModified
		return result
	case answer.StatusCode == http.StatusUnauthorized || answer.StatusCode == http.StatusForbidden:
		result.Outcome = OutcomeRefused
		return result
	case answer.StatusCode == http.StatusTooManyRequests:
		result.Outcome = OutcomeLimited
		return result
	case answer.StatusCode < 200 || answer.StatusCode > 299:
		result.Outcome, result.Detail = OutcomeFailed, fmt.Sprintf("the host answered %d", answer.StatusCode)
		return result
	}
	if method == http.MethodHead {
		result.Outcome = OutcomeAnswered
		return result
	}
	return f.store(answer.Body, path, request, result)
}

// store streams a body to a temporary file, checks it, and puts it in place.
func (f *Facility) store(body io.Reader, path string, request Request, result Result) Result {
	if err := os.MkdirAll(f.dir, 0o750); err != nil {
		result.Outcome, result.Detail = OutcomeFailed, "the data directory cannot be created"
		return result
	}
	temporary, err := os.CreateTemp(f.dir, ".fetch-*")
	if err != nil {
		result.Outcome, result.Detail = OutcomeFailed, "a temporary file cannot be created"
		return result
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	written, err := io.Copy(temporary, io.LimitReader(body, request.MaxBytes+1))
	closeErr := temporary.Close()
	switch {
	case err != nil || closeErr != nil:
		result.Outcome, result.Detail = OutcomeFailed, "the body could not be read to its end"
		return result
	case written > request.MaxBytes:
		result.Outcome, result.Detail = OutcomeFailed, fmt.Sprintf("the body is larger than %d bytes", request.MaxBytes)
		return result
	}
	if request.Check != nil {
		if accepted, why := request.Check(filepath.Base(name)); !accepted {
			result.Outcome, result.Detail = OutcomeMalformed, why
			return result
		}
	}
	if err := os.Rename(name, path); err != nil {
		result.Outcome, result.Detail = OutcomeFailed, "the new copy cannot be put in place"
		return result
	}
	f.writeValidators(path, validators{ETag: result.ETag, LastModified: result.LastModified})
	result.Outcome = OutcomeFetched
	return result
}

// validatorsPath is the file beside a fetched file holding its validators.
func validatorsPath(path string) string { return path + ".validators" }

func (f *Facility) readValidators(path string) validators {
	var held validators
	if raw, err := os.ReadFile(validatorsPath(path)); err == nil {
		_ = json.Unmarshal(raw, &held)
	}
	return held
}

// writeValidators keeps the validators beside a file. A failure to write them costs only a
// conditional request, so it is not a failure of the fetch.
func (f *Facility) writeValidators(path string, held validators) {
	if encoded, err := json.Marshal(held); err == nil {
		_ = os.WriteFile(validatorsPath(path), encoded, 0o640)
	}
}

// Path resolves a name in the plugin's data directory. A name is a plain file name: no
// separator, no `..`, no leading dot, so nothing outside the directory is ever reached.
func (f *Facility) Path(name string) (string, error) {
	if f.dir == "" {
		return "", errors.New("fetch: this plugin has no data directory")
	}
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." ||
		(strings.HasPrefix(name, ".") && !strings.HasPrefix(name, ".fetch-") && !strings.HasPrefix(name, ".extract-")) {
		return "", fmt.Errorf("fetch: %q is not a plain file name of the plugin's data directory", name)
	}
	return filepath.Join(f.dir, name), nil
}

// Directory is the plugin's data directory.
func (f *Facility) Directory() string { return f.dir }

// Destinations are the destinations the plugin declared, sorted.
func (f *Facility) Destinations() []string {
	list := make([]string, 0, len(f.destinations))
	for destination := range f.destinations {
		list = append(list, destination)
	}
	sortStrings(list)
	return list
}
