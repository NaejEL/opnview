// Package firewallcatalogue mounts the learned API catalogue (A15 of the broker amendment to
// specs/SPEC-backend-completion-plugins.md, docs/widget-api.md, "API catalogue"): what the firewall
// broker learned of every endpoint it called -- parameters and their roles, the nature, key, cursor
// and schema versions of each datum, call costs, floors, streams, readers and counters. It never
// carries a field value, a credential, the base URL or an authorisation header. It draws no page.
package firewallcatalogue
