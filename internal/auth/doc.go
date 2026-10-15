// Package auth holds password hashing and verification, account creation,
// session issue / validate / revoke, and the one-time setup token.
//
// NONE OF THIS IS A SEAM, and that is a decision rather than an omission.
// ROADMAP.md's modularity rule is a rule about KINDS THAT HAVE SEVERAL
// IMPLEMENTATIONS in a real installation — a lease source, a resolver, a theme, a
// widget. There is one mandated password algorithm, one session store, and the
// storage rule forbids provisioning a second one. A registry here would be
// machinery for a problem nobody has. What ROADMAP.md actually asks for is
// PARAMETER AGILITY — the parameters stored beside the hash so they can be raised
// later — and that is a column layout, which internal/store carries.
//
// NO STRING IN THIS PACKAGE IS USER-VISIBLE. Every failure is a typed error, and
// internal/web maps it to a catalogue key.
package auth
