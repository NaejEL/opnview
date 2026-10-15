// Package fetch is the plugin host's facility for fetching over HTTP(S) and for the files a plugin
// keeps in its own data directory (A7 of the backend-completion cycle).
//
// THE CORE CONTACTS THE FIREWALL AND NOTHING ELSE. Every other request opnview makes is made here,
// on a plugin's behalf, to a destination the plugin's manifest declares: the built-in maxmind
// plugin downloads from MaxMind's hosts and the built-in publicsuffix plugin from publicsuffix.org,
// and a third-party plugin's destinations are its author's business. A request to a host the
// manifest does not declare is refused before any connection, redirects included, so the plugin
// listing never misstates what a plugin contacts. That declaration is the author's own, not an
// operator allowlist.
//
// WHAT THE FACILITY DOES, AND WHY EACH IS ITS JOB RATHER THAN THE SCRIPT'S:
//
//   - a body is streamed to a file in the plugin's data directory, never into script memory, under
//     a size bound the plugin states;
//   - a fetch is conditional when asked: the validators of the copy held (ETag, Last-Modified) are
//     sent, and a 304 keeps it;
//   - a body is written to a temporary file and replaces the previous good copy only once it is
//     complete and passes the plugin's own check, so a failed or malformed download leaves the
//     previous copy in use;
//   - headers the plugin sets -- its credentials among them -- go to the host it asked, and never to
//     a host a redirect leads to;
//   - every answer is one closed outcome, which the plugin maps to its availability.
package fetch
