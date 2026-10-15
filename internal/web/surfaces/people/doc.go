// Package people mounts the JSON API of the people the user creates and the machines the user
// assigns to them (C1 of the backend-completion cycle, docs/widget-api.md, "Write endpoints",
// *People and machines*). It draws no page and carries no catalogue: it answers JSON only, for the
// step-7 application.
//
// Ownership is assigned, never inferred: every write names its clients by id and changes exactly
// those. Nothing here contacts the firewall. Every route is authenticated; every write is a JSON
// POST the middleware asks for the session's form token, and no GET changes anything.
package people
