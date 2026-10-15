// Package broker is the only path from opnview to the firewall API (A9 to A18 of the broker
// amendment to specs/SPEC-backend-completion-plugins.md).
//
// EVERY REQUEST COMES HERE: from the plugins, from discovery, from the probe round, from the core
// coordinators. The broker applies the read-only rule (readonly.go); answers from the mirror when
// the mirror holds an answer young enough for the requester's max age, and otherwise queues one
// call with the scheduler; hands that one answer to every requester waiting for it; records what
// each requester read of it; and stores the fields read in the mirror. It is the only caller of
// the client's Call, Stream and Listen. The one exception is the settings surface's check of
// candidate credentials (internal/web/surfaces/settings/verify.go), which tests credentials that
// are not saved against a firewall that may not be the configured one, and whose answer must never
// enter the mirror.
package broker
