package web

import "net/http"

// The one static asset.
//
// IT IS SERVED FROM THE BINARY'S OWN EMBEDDED COPY, and there is exactly one of
// them. No font, no script, no icon set, no favicon, no analytics beacon and no
// version check: ROADMAP.md permits opnview three outbound calls — the firewall API,
// the MaxMind download and the Public Suffix List download — and a page that fetched
// a stylesheet, a font or an icon from anywhere would be a fourth. That is a privacy
// and availability rule rather than a performance preference: the owner of the network
// is entitled to know that looking at his firewall data contacts nobody.
//
// THERE IS NO http.FileServer HERE, deliberately. A file server serves whatever is
// in a directory, so the set of things reachable would be whatever somebody later
// dropped in; one handler per asset makes the set of reachable files the set written
// down in the route table.

// handleStylesheet serves the embedded stylesheet.
func (s *Server) handleStylesheet(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "text/css; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	// No revalidation story exists yet — the asset changes only when the binary
	// does, and there is no build stamp in the path to key a cache on — so it is
	// served fresh. A wrong stylesheet cached against a new binary is a broken page
	// nobody can clear.
	writer.Header().Set("Cache-Control", "no-cache")
	if _, err := writer.Write(s.renderer.stylesheet); err != nil {
		// The reader has gone. There is nothing to report to and nothing to fix.
		return
	}
}
