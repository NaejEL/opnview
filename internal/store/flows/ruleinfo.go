package flows

import (
	"context"
	"fmt"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// The rule-info cache (provider_rule_info): what a security_event provider said of the rules its
// events name, and the rules it was asked about and did not know.
//
// A ROW IS AN ANSWER, AND AN ABSENT ROW IS A QUESTION NOT YET ASKED. lookup_state is found or
// not_found, and an identity with no row has never been fetched: the served alert rows tell the
// three apart (docs/data-model.md, provider_rule_info).

// The lookup states of a cache row.
const (
	// RuleFound is a rule the provider knew.
	RuleFound = "found"
	// RuleNotFound is a rule the provider was asked about and did not know.
	RuleNotFound = "not_found"
)

// RuleInfo is one answer of a provider about one rule identity.
type RuleInfo struct {
	RuleIdentity string
	// LookupState is RuleFound or RuleNotFound.
	LookupState string
	// NormalisedSeverity is nil when the provider reported no severity, and for a rule not found.
	NormalisedSeverity *string
	// ProviderSeverity is the raw value the provider reported, verbatim.
	ProviderSeverity *string
	Category         *string
	RuleSource       *string
	FetchedAt        int64
}

// RuleIdentitiesToFetch returns the rule identities a provider's events carry that the cache
// holds no row for, or a row fetched before staleBefore, in identity order and at most bound of
// them: what the provider is asked about next.
func (s *FlowStore) RuleIdentitiesToFetch(ctx context.Context, providerID, staleBefore, bound int64) ([]string,
	error) {
	text, err := sqldb.Statement("rule_identities_to_fetch")
	if err != nil {
		return nil, err
	}
	rows, err := sqldb.Query(ctx, s.SQL, text, map[string]any{"provider_id": providerID,
		"stale_before": staleBefore, "bound": bound})
	if err != nil {
		return nil, fmt.Errorf("store: reading the rule identities to fetch: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var identities []string
	for rows.Next() {
		var identity string
		if err := rows.Scan(&identity); err != nil {
			return nil, fmt.Errorf("store: reading the rule identities to fetch: %w", err)
		}
		identities = append(identities, identity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading the rule identities to fetch: %w", err)
	}
	return identities, nil
}

// UpsertRuleInfo writes one answer, replacing what the cache held for that rule. A rule not found
// carries nothing but its instant, which the schema enforces.
func (s *FlowStore) UpsertRuleInfo(ctx context.Context, providerID int64, info RuleInfo) error {
	if info.LookupState != RuleFound && info.LookupState != RuleNotFound {
		return fmt.Errorf("store: the rule-info lookup state %q is neither %s nor %s", info.LookupState,
			RuleFound, RuleNotFound)
	}
	if _, err := sqldb.ExecNamed(ctx, s.SQL, "rule_info_upsert", map[string]any{
		"provider_id":         providerID,
		"rule_identity":       info.RuleIdentity,
		"lookup_state":        info.LookupState,
		"normalised_severity": sqldb.OptionalText(info.NormalisedSeverity),
		"provider_severity":   sqldb.OptionalText(info.ProviderSeverity),
		"category":            sqldb.OptionalText(info.Category),
		"rule_source":         sqldb.OptionalText(info.RuleSource),
		"fetched_at":          info.FetchedAt,
	}); err != nil {
		return fmt.Errorf("store: writing the rule info of %s: %w", info.RuleIdentity, err)
	}
	return nil
}
