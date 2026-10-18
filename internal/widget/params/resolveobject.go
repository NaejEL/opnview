package params

import (
	"context"
	"encoding/json"

	"github.com/NaejEL/opnview/internal/store"
)

// Resolving a reference object outside a widget's parameters, for GET /api/references/resolve
// (specs/SPEC-step-7-frontend.md, A8): the import of a dashboard asks it of a canvas's scope.
//
// IT IS ResolveReference, NOT A SECOND RESOLVER. The object is decoded as a widget parameter's is
// (decodeReference) and resolved by the very function the widget API resolves its parameters with,
// with no expected kind -- a canvas scope list accepts the kind it is filed under, and the vocabulary
// checks still run -- so its answer for a reference is the reason the widget envelope's
// unresolved_references gives for that same reference, or resolved.

// ResolvedState is the state of a reference that matched.
const ResolvedState = "resolved"

// NotAReferenceObject is a value that is not a reference object at all: not an object carrying a
// string kind, a string by and a value. A widget parameter reports it invalid with the same code.
const NotAReferenceObject = reasonNotAReference

// ResolveObject resolves one reference object and answers its state: ResolvedState, one of the
// unresolved reasons of ResolveReference, or NotAReferenceObject with ok false.
func ResolveObject(ctx context.Context, database *store.Store, raw json.RawMessage, noDomains bool) (
	reference Reference, state string, ok bool, err error) {
	reference, ok = decodeReference(raw)
	if !ok {
		return Reference{}, NotAReferenceObject, false, nil
	}
	_, reason, err := ResolveReference(ctx, database, referenceAt{Reference: reference}, noDomains)
	if err != nil {
		return reference, "", true, err
	}
	if reason == "" {
		return reference, ResolvedState, true, nil
	}
	return reference, reason, true, nil
}
