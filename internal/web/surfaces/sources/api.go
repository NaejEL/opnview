package sources

import (
	"context"
	"net/http"

	"github.com/NaejEL/opnview/internal/web/messages"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// The Sources surface's JSON equivalent (docs/widget-api.md, "Settings API"): every provider's
// availability with the operator's selection and whether it counts towards an outage, when its
// current state began (coverage row 39), the outages the top bar's indicator counts, and the
// collection gaps of the last day. It reads the local database and nothing else.

// PathAPISources is every provider, outage and recent gap, and the outage count.
const PathAPISources = "/api/sources"

// apiRoutes are the surface's JSON rows of the route table.
func (s *surface) apiRoutes() []web.Route {
	return []web.Route{
		web.JSONRoute(http.MethodGet, PathAPISources, s.handleAPISources),
	}
}

// SourcesAnswer is every provider, outage and recent gap, and the outage count.
type SourcesAnswer struct {
	Providers []ProviderState `json:"providers"`
	// Outages are what the indicator counts, and OutageCount the indicator's figure.
	Outages     []OutageState `json:"outages"`
	OutageCount int           `json:"outage_count"`
	// RecentGaps are the collection gaps detected in the last RecentGapWindowSeconds.
	RecentGaps             []GapState `json:"recent_gaps"`
	RecentGapWindowSeconds int64      `json:"recent_gap_window_seconds"`
}

// ProviderState is one registered provider.
type ProviderState struct {
	Kind        string `json:"kind"`
	ProviderKey string `json:"provider_key"`
	DisplayName string `json:"display_name"`
	// Availability is the condition as the preset names it: healthy, not_collecting, unavailable,
	// not_yet_probed or unknown; Outcome is the closed outcome code of the last check, null without
	// an availability row.
	Availability string  `json:"availability"`
	Outcome      *string `json:"outcome"`
	// Active is whether opnview reads it; Selection is the operator's: auto, on or off.
	Active    bool   `json:"active"`
	Selection string `json:"selection"`
	// InOutage is whether it counts towards an outage the indicator counts.
	InOutage bool `json:"in_outage"`
	// CheckedAt is when the row was determined, null when it never was.
	CheckedAt *int64 `json:"checked_at"`
	// Probe and Diagnostic are the collector's diagnostic data, verbatim; Diagnostic is null when none.
	Probe      string  `json:"probe"`
	Diagnostic *string `json:"detail"`
	// Since bounds when the current state began: never an exact instant.
	Since request.StateSince `json:"since"`
	// OPNsensePages are, for a provider installed and not collecting, the OPNsense pages that turn
	// it on, on the configured firewall. opnview never makes that change.
	OPNsensePages []OPNsensePage `json:"opnsense_pages"`
}

// OPNsensePage is one page of the OPNsense web interface, named by its menu entry.
type OPNsensePage struct {
	URL  string `json:"url"`
	Page string `json:"page"`
}

// OutageState is one current outage: a kind's, or one provider's (ProviderKey not null).
type OutageState struct {
	Kind        string  `json:"kind"`
	ProviderKey *string `json:"provider_key"`
}

// GapState is one collection gap.
type GapState struct {
	Kind          string  `json:"kind"`
	ProviderKey   string  `json:"provider_key"`
	Reason        string  `json:"reason"`
	StartAt       int64   `json:"start_at"`
	EndAt         int64   `json:"end_at"`
	MissedSeconds int64   `json:"missed_seconds"`
	Diagnostic    *string `json:"detail"`
}

// sourcesAnswer reads every provider, outage and recent gap.
func (s *surface) sourcesAnswer(ctx context.Context) (SourcesAnswer, error) {
	answer := SourcesAnswer{Providers: []ProviderState{}, Outages: []OutageState{}, RecentGaps: []GapState{},
		RecentGapWindowSeconds: int64(recentGapWindow.Seconds())}
	statuses, err := s.readSourceStatuses(ctx)
	if err != nil {
		return answer, err
	}
	since, err := request.ReadAvailabilitySince(ctx, s.store)
	if err != nil {
		return answer, err
	}
	baseURL, _, err := s.store.Setting(ctx, config.KeyOPNsenseBaseURL)
	if err != nil {
		return answer, err
	}
	counted := map[int]bool{}
	outages := findOutages(statuses)
	for _, found := range outages {
		drawn := OutageState{Kind: found.kind}
		if found.provider != "" {
			provider := found.provider
			drawn.ProviderKey = &provider
		}
		answer.Outages = append(answer.Outages, drawn)
		for _, index := range found.rows {
			counted[index] = true
		}
	}
	answer.OutageCount = len(outages)
	for index, status := range statuses {
		row := status.row
		availability, _ := messages.AvailabilityOf(row)
		drawn := ProviderState{Kind: row.Kind, ProviderKey: row.ProviderKey, DisplayName: row.DisplayName,
			Availability: web.ConditionOf(availability), Active: row.IsActive,
			Selection: string(status.selection), InOutage: counted[index], Probe: row.Probe,
			Since: since[request.SinceKey(row.Kind, row.ProviderKey)], OPNsensePages: []OPNsensePage{}}
		if row.HasAvailability {
			outcome := string(row.Outcome)
			drawn.Outcome = &outcome
		}
		if row.HasAvailability && !row.NotYetProbed() {
			checked := row.CheckedAt
			drawn.CheckedAt = &checked
		}
		drawn.Diagnostic = verbatimDetail(row)
		if availability == msgAvailabilityNotCollecting {
			for _, link := range s.opnsenseLinks(baseURL, row.Kind, row.ProviderKey) {
				drawn.OPNsensePages = append(drawn.OPNsensePages,
					OPNsensePage{URL: link.URL, Page: web.ConditionOf(link.LabelKey)})
			}
		}
		answer.Providers = append(answer.Providers, drawn)
	}
	gaps, err := s.store.RecentCollectionGaps(ctx, s.now().Add(-recentGapWindow).UTC().Unix())
	if err != nil {
		return answer, err
	}
	for _, gap := range gaps {
		drawn := GapState{Kind: gap.Kind, ProviderKey: gap.ProviderKey, Reason: string(gap.Reason),
			StartAt: gap.IntervalStartAt, EndAt: gap.IntervalEndAt, MissedSeconds: gap.MissedSeconds()}
		drawn.Diagnostic = verbatimGapDetail(gap)
		answer.RecentGaps = append(answer.RecentGaps, drawn)
	}
	return answer, nil
}

func (s *surface) handleAPISources(writer http.ResponseWriter, request *http.Request) {
	answer, err := s.sourcesAnswer(request.Context())
	if err != nil {
		web.FailJSON(writer, request, err)
		return
	}
	web.WriteJSON(writer, http.StatusOK, answer)
}
