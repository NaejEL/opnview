// Package plugindecl is the declaration of a table a plugin owns: its columns and their types, its
// keys and indexes, its retention, and the SQL it is created, written and read with. It holds no
// connection: internal/store/sqldb keeps the set ensured on one database, and
// internal/store/plugintables ensures, writes, reads and purges the tables.
package plugindecl
