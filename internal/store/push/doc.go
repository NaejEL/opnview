// Package push is the part of the store holding the push sources' bookkeeping: the gaps a push
// path records, its listeners' liveness, and the source periods -- the spans during which one
// delivery path, pull or push, delivered a provider's records. internal/store composes it into
// Store.
package push
