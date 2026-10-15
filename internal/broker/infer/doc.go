// Package infer learns what a firewall answer is (A10 of the broker amendment to
// specs/SPEC-backend-completion-plugins.md): the type of each field, the objects whose keys are
// values, the rows the answer holds, its nature -- a snapshot or a record stream -- its identity
// key and its cursor, and the role of each request parameter. A plugin may correct any of it in
// its manifest, and a correction always overrides an inference.
//
// The core never needs to know an endpoint in advance: nothing here names one. What it reads is
// the answer's shape, the names OPNsense gives its fields and parameters, and the shapes of the
// values -- never an installation's data.
package infer
