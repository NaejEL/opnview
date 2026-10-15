// Package answer holds a firewall answer as the firewall wrote it, and records which of its
// fields a reader read (A10 of specs/SPEC-backend-completion-plugins.md, the broker amendment).
//
// A VALUE IS KEPT LEXICALLY. A number keeps the text the firewall printed -- "1.50", an integer
// beyond 2^53 -- and an object keeps the order of its keys, so what the mirror stores and serves
// again is the firewall's answer field for field, in JSON type and in lexical value. Nothing here
// converts a value to a Go number; a reader that wants one asks for it.
package answer
