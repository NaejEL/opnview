package sources

import (
	"context"
	"fmt"
	"strings"

	"github.com/NaejEL/opnview/internal/web"
)

// What the external tests of this surface need of its internals.

// RecentGapWindow is how far back the surface lists collection gaps.
const RecentGapWindow = recentGapWindow

// PathParts cuts a probe after each slash.
var PathParts = pathParts

// CountOutages is the indicator's own count, read through the surface's own rule.
func CountOutages(ctx context.Context, host web.Host) (int, error) {
	statuses, err := newSurface(host).readSourceStatuses(ctx)
	if err != nil {
		return 0, err
	}
	return countOutages(statuses), nil
}

// PageFigures are every figure the Sources page draws, one line each, read through the page's own
// fill: what the JSON answer must carry (C-AC9). A state is its catalogue key.
func PageFigures(ctx context.Context, host web.Host) ([]string, error) {
	var built view
	if err := newSurface(host).fillSources(ctx, &built); err != nil {
		return nil, err
	}
	var lines []string
	for _, row := range built.Sources {
		line := fmt.Sprintf("source %s %s %s active %s selection %s outage %s checked %s%s probe %s detail %s%s",
			row.KindKey, row.ProviderKey, row.Availability, row.ActiveKey, row.SelectionKey, row.OutageKey,
			row.Checked, row.CheckedState, strings.Join(row.ProbeParts, ""), row.Detail, row.DetailState)
		for _, page := range row.Pages {
			line += fmt.Sprintf(" page %s %s", page.URL, web.ConditionOf(page.LabelKey))
		}
		lines = append(lines, line)
	}
	for _, outage := range built.Outages {
		lines = append(lines, fmt.Sprintf("outage %s %s %s", outage.KindKey, outage.ProviderKey, outage.StateKey))
	}
	for _, gap := range built.RecentGaps {
		lines = append(lines, fmt.Sprintf("gap %s %s %s %s %s %v detail %s%s", gap.KindKey, gap.ProviderKey,
			gap.ReasonKey, gap.Start, gap.End, gap.Missed, gap.Detail, gap.DetailState))
	}
	return lines, nil
}
