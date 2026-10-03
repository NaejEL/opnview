package maxmind

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/NaejEL/opnview/internal/config"
)

// The ways a request to MaxMind fails, each its own state on the settings surface,
// because each sends the operator somewhere different.
var (
	// ErrRefused is a 401 or 403: MaxMind did not accept the account ID and the
	// licence key.
	ErrRefused = errors.New("maxmind: the account ID and licence key were refused")
	// ErrLimited is a 429: the account's daily download limit is spent.
	ErrLimited = errors.New("maxmind: the account's download limit is reached")
	// ErrHostRefused is a request, or a redirect, to a host that is neither of the
	// two. It is refused before anything is sent.
	ErrHostRefused = errors.New("maxmind: a host other than MaxMind's two was refused")
)

// StatusError is any other answer that is not a success.
type StatusError struct {
	// Code is the HTTP status MaxMind answered.
	Code int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("maxmind: the download host answered %d", e.Code)
}

// Client makes the requests. It is the only thing in this package that reaches the
// network.
type Client struct {
	http *http.Client
	base string
}

// NewClient returns the client for MaxMind's own hosts. timeout bounds one whole
// request, download included.
func NewClient(timeout time.Duration) *Client {
	return newClient(downloadBase, downloadHost, []string{downloadHost, redirectHost},
		&http.Transport{Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 10 * time.Second},
		timeout)
}

// newClient is NewClient with the hosts and the transport supplied, which is what a
// test does with two local servers standing in for MaxMind's two hosts.
func newClient(base, authHost string, allowed []string, inner http.RoundTripper,
	timeout time.Duration) *Client {
	permitted := make(map[string]bool, len(allowed))
	for _, host := range allowed {
		permitted[host] = true
	}
	guard := &guardedTransport{permitted: permitted, authHost: authHost, inner: inner}
	return &Client{
		base: base,
		http: &http.Client{
			Transport: guard,
			Timeout:   timeout,
			// A redirect to any host but the two is refused before it is followed.
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if !permitted[request.URL.Host] {
					return ErrHostRefused
				}
				if len(via) >= 5 {
					return errors.New("maxmind: too many redirects")
				}
				return nil
			},
		},
	}
}

// guardedTransport refuses any host but the permitted ones, and adds the
// credentials to a request for the authenticating host and to no other.
//
// THE CREDENTIALS ARE ADDED HERE, PER REQUEST, AND NOT SET ON THE REQUEST. A header set
// on the first request would be copied onto a redirect by the client whenever it
// judged the two hosts alike; adding it here, for one host exactly, means the R2 host
// a download is redirected to never receives the licence key, whatever the client's
// rule is.
type guardedTransport struct {
	permitted map[string]bool
	authHost  string
	inner     http.RoundTripper
}

// credentialsKey carries the credentials from a call to the transport.
type credentialsKey struct{}

// RoundTrip sends one request, or refuses it.
func (g *guardedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !g.permitted[request.URL.Host] {
		return nil, &url.Error{Op: request.Method, URL: request.URL.Redacted(), Err: ErrHostRefused}
	}
	if request.URL.Host == g.authHost {
		if credentials, ok := request.Context().Value(credentialsKey{}).(config.MaxMindCredentials); ok {
			request = request.Clone(request.Context())
			request.SetBasicAuth(credentials.AccountID, credentials.LicenceKey)
		}
	}
	return g.inner.RoundTrip(request)
}

// LastModified asks when one edition was last built, with a HEAD, which MaxMind
// does not count against the download limit.
func (c *Client) LastModified(ctx context.Context, credentials config.MaxMindCredentials,
	edition Edition) (time.Time, error) {
	answer, err := c.do(ctx, credentials, http.MethodHead, edition)
	if err != nil {
		return time.Time{}, err
	}
	_ = answer.Body.Close()
	modified, err := http.ParseTime(answer.Header.Get("Last-Modified"))
	if err != nil {
		return time.Time{}, fmt.Errorf("maxmind: the %s answer carries no readable Last-Modified: %w",
			edition, err)
	}
	return modified, nil
}

// Download writes one edition's archive to destination.
func (c *Client) Download(ctx context.Context, credentials config.MaxMindCredentials,
	edition Edition, destination io.Writer) error {
	answer, err := c.do(ctx, credentials, http.MethodGet, edition)
	if err != nil {
		return err
	}
	defer func() { _ = answer.Body.Close() }()
	if _, err := io.Copy(destination, answer.Body); err != nil {
		return fmt.Errorf("maxmind: reading the %s archive: %w", edition, err)
	}
	return nil
}

// do sends one request for one edition and classifies the answer.
func (c *Client) do(ctx context.Context, credentials config.MaxMindCredentials, method string,
	edition Edition) (*http.Response, error) {
	request, err := http.NewRequestWithContext(context.WithValue(ctx, credentialsKey{}, credentials),
		method, permalink(c.base, edition), nil)
	if err != nil {
		return nil, fmt.Errorf("maxmind: building the %s request: %w", edition, err)
	}
	answer, err := c.http.Do(request)
	if err != nil {
		if errors.Is(err, ErrHostRefused) {
			return nil, ErrHostRefused
		}
		return nil, fmt.Errorf("maxmind: requesting %s: %w", edition, err)
	}
	switch {
	case answer.StatusCode == http.StatusUnauthorized || answer.StatusCode == http.StatusForbidden:
		_ = answer.Body.Close()
		return nil, ErrRefused
	case answer.StatusCode == http.StatusTooManyRequests:
		_ = answer.Body.Close()
		return nil, ErrLimited
	case answer.StatusCode < 200 || answer.StatusCode > 299:
		_ = answer.Body.Close()
		return nil, &StatusError{Code: answer.StatusCode}
	}
	return answer, nil
}
