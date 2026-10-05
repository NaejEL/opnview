package publicsuffix

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// ErrHostRefused is a request, or a redirect, to any host but publicsuffix.org. It
// is refused before anything is sent.
var ErrHostRefused = errors.New("publicsuffix: a host other than publicsuffix.org was refused")

// StatusError is an answer that is neither a success nor "not modified".
type StatusError struct {
	// Code is the HTTP status the server answered.
	Code int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("publicsuffix: the list host answered %d", e.Code)
}

// maxListBytes bounds what is read of a download. The list is a few hundred
// kilobytes; this leaves two orders of magnitude of room and stops a broken answer
// from filling memory.
const maxListBytes = 32 << 20

// Client makes the one request this package makes. It is the only thing here that
// reaches the network.
type Client struct {
	http *http.Client
	url  string
}

// NewClient returns the client for publicsuffix.org. timeout bounds one whole
// request, download included.
func NewClient(timeout time.Duration) *Client {
	return newClient(listURL, []string{listHost},
		&http.Transport{Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 10 * time.Second},
		timeout)
}

// newClient is NewClient with the location, the permitted hosts and the transport
// supplied, which is what a test does with a local server standing in for the list
// host.
func newClient(location string, allowed []string, inner http.RoundTripper, timeout time.Duration) *Client {
	permitted := make(map[string]bool, len(allowed))
	for _, host := range allowed {
		permitted[host] = true
	}
	return &Client{
		url: location,
		http: &http.Client{
			Transport: &guardedTransport{permitted: permitted, inner: inner},
			Timeout:   timeout,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if !permitted[request.URL.Host] {
					return ErrHostRefused
				}
				if len(via) >= 5 {
					return errors.New("publicsuffix: too many redirects")
				}
				return nil
			},
		},
	}
}

// guardedTransport refuses every host but the permitted ones.
type guardedTransport struct {
	permitted map[string]bool
	inner     http.RoundTripper
}

// RoundTrip sends one request, or refuses it.
func (g *guardedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !g.permitted[request.URL.Host] {
		return nil, &url.Error{Op: request.Method, URL: request.URL.Redacted(), Err: ErrHostRefused}
	}
	return g.inner.RoundTrip(request)
}

// Validators are what the server said identifies the copy it served, sent back on
// the next request so an unchanged list is not downloaded again.
type Validators struct {
	// ETag is the entity tag, verbatim.
	ETag string
	// LastModified is the Last-Modified header, verbatim.
	LastModified string
}

// Fetched is the answer to one request.
type Fetched struct {
	// NotModified says the server answered 304: the copy held is current.
	NotModified bool
	// Body is the list, when it was sent.
	Body []byte
	// Validators identify the copy the server sent.
	Validators Validators
}

// Fetch asks for the list, conditionally on the copy already held when there is
// one.
func (c *Client) Fetch(ctx context.Context, held Validators) (Fetched, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return Fetched{}, fmt.Errorf("publicsuffix: building the request: %w", err)
	}
	if held.ETag != "" {
		request.Header.Set("If-None-Match", held.ETag)
	}
	if held.LastModified != "" {
		request.Header.Set("If-Modified-Since", held.LastModified)
	}
	answer, err := c.http.Do(request)
	if err != nil {
		if errors.Is(err, ErrHostRefused) {
			return Fetched{}, ErrHostRefused
		}
		return Fetched{}, fmt.Errorf("publicsuffix: requesting the list: %w", err)
	}
	defer func() { _ = answer.Body.Close() }()

	switch {
	case answer.StatusCode == http.StatusNotModified:
		return Fetched{NotModified: true, Validators: held}, nil
	case answer.StatusCode < 200 || answer.StatusCode > 299:
		return Fetched{}, &StatusError{Code: answer.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(answer.Body, maxListBytes+1))
	if err != nil {
		return Fetched{}, fmt.Errorf("publicsuffix: reading the list: %w", err)
	}
	if len(body) > maxListBytes {
		return Fetched{}, fmt.Errorf("publicsuffix: the list is larger than %d bytes", maxListBytes)
	}
	return Fetched{
		Body: body,
		Validators: Validators{
			ETag:         answer.Header.Get("ETag"),
			LastModified: answer.Header.Get("Last-Modified"),
		},
	}, nil
}
