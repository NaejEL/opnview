// Package push receives what the firewall sends to opnview once the user has configured it to: syslog
// lines and NetFlow records (decision 15 and PS1-PS16 of the push amendment to
// specs/SPEC-backend-completion-plugins.md; docs/plugins.md, "Push sources").
//
// IT IS INBOUND. A listener accepts datagrams and connections from the firewall's own addresses and
// makes no call of its own: the broker stays the only path from opnview to the firewall's API, and
// nothing here writes to the firewall.
//
// IT IS AGNOSTIC. The package holds the listeners, the routing by subscription, the bounded queues,
// the batching, the gaps, the sender check and the liveness; each protocol's generic decoder is a
// directory below protocols/, discovered; what any program's message says is its plugin's. No
// non-test file here names a program, a message field, or compiles a regular expression over a
// message (agnostic_test.go).
package push
