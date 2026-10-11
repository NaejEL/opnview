package widget

import (
	"context"
	"testing"
)

// TestGroupingAndTheResolverCeilingAreNamedStates is the rest of A9: grouping by
// registrable domain before the first successful Public Suffix List download is an explicit
// unavailable state and nothing is grouped; with a list, the names collapse; and a resolver
// ring-buffer gap in the window sets the flag on every resolver-fed response.
func TestGroupingAndTheResolverCeilingAreNamedStates(t *testing.T) {
	t.Parallel()
	f := newFixture(t, defaultSpec)
	grouped := f.respond(t, "top_sites", map[string]any{"group_by_registrable_domain": true})
	rows := grouped.Data.(TopSitesData)
	if rows.Grouping != "registrable_domain" || len(rows.Rows) == 0 || rows.Rows[0].SiteNames == nil {
		t.Errorf("with a list the names were not grouped: %+v", rows.Grouping)
	}
	bare, err := New(f.db, Options{Now: f.service.now})
	check(t, err)
	response, err := bare.Respond(context.Background(), "top_sites", `{"group_by_registrable_domain":true}`)
	check(t, err)
	if !contains(response.States, "registrable_domain_grouping_unavailable") ||
		response.Data.(TopSitesData).Grouping != "site_name" {
		t.Errorf("before any download the grouping reads %v", response.States)
	}

	if bounded := f.respond(t, "top_sites", nil).ResolverWindowBounded; bounded == nil || *bounded {
		t.Errorf("a window with no resolver gap is flagged bounded: %v", bounded)
	}
	provider, err := f.db.ProviderID(context.Background(), "dns_lookup", "unbound")
	check(t, err)
	f.exec(t, `INSERT INTO collection_gap (provider_id, interval_start_at, interval_end_at, reason, detected_at)
		VALUES (?, ?, ?, 'resolver_window_not_honoured', ?)`, provider, f.now-7200, f.now-3600, f.now)
	for _, widgetType := range []string{"top_sites", "attribution_rate_per_client", "blocked_dns_lookups"} {
		if bounded := f.respond(t, widgetType, nil).ResolverWindowBounded; bounded == nil || !*bounded {
			t.Errorf("%s is not flagged as bounded by the resolver's ring buffer", widgetType)
		}
	}
}
