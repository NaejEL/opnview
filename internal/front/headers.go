package front

import (
	"net/http"
	"strings"
)

// The headers every front response carries (A3).
//
// THE POLICY IS THE ENFORCEABLE FORM OF THE OUTBOUND RULE. opnview makes exactly three outbound
// calls -- the firewall API, the MaxMind download and the Public Suffix List download -- and a page
// makes none of them: every script, stylesheet, image, font and request is this server's own
// ('self'), images may be inline data, and nothing else loads, frames the page, posts a form
// elsewhere or rewrites the base. The shell holds no inline script and no style attribute, so the
// policy needs neither 'unsafe-inline' nor a nonce.
//
// THE CACHE REVALIDATES. Every asset is tagged by its content and served no-cache, so a browser
// keeps a copy and asks whether it is current: an asset changes only with the binary or a part
// dropped into the data directory, and a stale script against a new binary is a broken page nobody
// can clear.

// ContentSecurityPolicy is the policy of the shell and of every asset, verbatim.
const ContentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"connect-src 'self'; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// WriteHeaders sets the headers of a front response: the policy, nosniff, the cache rule and the
// tag.
func WriteHeaders(writer http.ResponseWriter, contentType, etag string) {
	header := writer.Header()
	header.Set("Content-Security-Policy", ContentSecurityPolicy)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Cache-Control", "no-cache")
	header.Set("Referrer-Policy", "no-referrer")
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	if etag != "" {
		header.Set("ETag", etag)
	}
}

// NotModified reports whether a request already holds the tagged content.
func NotModified(request *http.Request, etag string) bool {
	for _, candidate := range strings.Split(request.Header.Get("If-None-Match"), ",") {
		if strings.TrimSpace(candidate) == etag {
			return true
		}
	}
	return false
}

// Serve answers one tagged body: 304 when the request holds it, the body otherwise. A HEAD gets
// the headers alone.
func Serve(writer http.ResponseWriter, request *http.Request, contentType, etag string, body []byte) {
	WriteHeaders(writer, contentType, etag)
	if NotModified(request, etag) {
		writer.WriteHeader(http.StatusNotModified)
		return
	}
	writer.WriteHeader(http.StatusOK)
	if request.Method == http.MethodHead {
		return
	}
	// The reader may have gone; there is nothing to report to and nothing to fix.
	_, _ = writer.Write(body)
}
