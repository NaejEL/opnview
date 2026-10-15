// Package labels mounts the JSON API of the labels the user gives interfaces and the purposes the
// user gives blocklists (C1 of the backend-completion cycle, docs/widget-api.md, "Write endpoints",
// *Labels and purposes*). It draws no page and carries no catalogue: it answers JSON only, for the
// step-7 application.
//
// Both are user input that nothing infers: a label is never derived from a description, a purpose
// never from a list's name (docs/data-model.md, "interface", "blocklist"). Discovery never writes a
// label and the purge never removes a purpose. Every route is authenticated; every write is a JSON
// POST the middleware asks for the session's form token, and no GET changes anything.
package labels
