// Package store opens the PostgreSQL database, applies the embedded schema, and holds the typed
// write and read paths the collectors and the widget API use.
//
// The store is PostgreSQL with the TimescaleDB extension (ROADMAP.md, "Rules that apply to every
// step"; decision 13 of specs/SPEC-backend-completion-plugins.md). One database per installation;
// opnview is its only client. The schema is one file, applied in place on every start, in one
// transaction; there is no migration runner and no schema_version table. Migrations begin with
// step 8.
//
// The driver is pgx, pure Go, so CGO_ENABLED stays 0.
//
// THE STORE IS COMPOSED OF PARTS, each a package below this one named by what it holds, each
// embedding the one handle they all run on (sqldb/): accounts/, owners/, flows/, attribution/,
// aggregates/, samples/, netflow/, push/, geo/, reads/, plugintables/ and purge/. Store embeds
// every part, so a caller holds one Store and calls its methods without naming a part. The closed
// vocabularies and row types are vocab/'s, a plugin table's declaration is plugindecl/'s; this
// package opens the database and applies the schema.
package store
