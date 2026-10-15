// Package mirror is the learned mirror of the firewall API (A10 and A11 of the broker amendment
// to specs/SPEC-backend-completion-plugins.md): the PostgreSQL schema `opnsense`, holding what the
// broker observed of the firewall's answers, field for field as the firewall wrote them, and only
// the fields some reader read.
//
// THE TABLES ARE LEARNED. Nothing names an endpoint in advance. The metadata tables are the core
// schema's (internal/store/schema.sql); the data tables are created here, one per endpoint and row
// set, with one column per field path a reader read, and altered as readers read more: a new field
// is a new column, a type conflict converts a column to JSONB without loss, and every change records
// a schema version. Nothing is dropped: a column nobody reads any more stops being filled and is
// kept, with its rows, until retention.
//
// A FAILED MIRROR WRITE NEVER FAILS A REQUEST. The broker serves the firewall's answer whatever
// happens here, logs the failure, and treats the datum as not mirrored.
package mirror
