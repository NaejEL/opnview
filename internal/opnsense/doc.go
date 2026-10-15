// Package opnsense is the only package in opnview that constructs an HTTP
// request.
//
// Three rules are enforced here rather than remembered:
//
//  1. Every path opnview may call is a registry entry, each carrying the
//     section of docs/opnsense-api-survey.md and the upstream URL that
//     establishes it. A path that is not in the registry cannot be issued, so
//     an invented endpoint cannot compile and pass.
//  2. No mutating command is ever issued. The refusal happens before a request
//     is built, so the transport never sees one.
//  3. The firewall is the only host reached. There is exactly one HTTP client
//     and one base URL, and nothing else in the program builds a request.
package opnsense
