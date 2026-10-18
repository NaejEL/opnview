package vocab

// ProviderKind is a kind's row of provider_kind: whether several of its providers may be active at once.
// The attribute is the kind's declaration's -- its kind file (internal/collect/provider/kinds) or its
// plugin's manifest -- and the schema's index reads it from every provider row (schema.sql,
// uq_provider_active_per_exclusive_kind): a kind admits several concurrently active providers exactly when
// the identity of its destination rows includes the provider.
type ProviderKind struct {
	Kind       string
	Concurrent bool
}

// GapReasonDeclaration is a row of the gap-reason registry, gap_reason: a reason a collection gap may be
// recorded for, and what declares it -- a kind's file, the push sources or one push protocol.
type GapReasonDeclaration struct {
	Reason     GapReason
	DeclaredBy string
}

// ProviderDeclaration is a registry row as a declaration states it: its kind, its key within the kind,
// and what it is called.
type ProviderDeclaration struct {
	Kind        string
	Key         string
	DisplayName string
}
