package sources

import (
	"context"

	"github.com/NaejEL/opnview/internal/web"
)

// What the external tests of this surface need of its internals.

// RecentGapWindow is how far back the surface lists collection gaps.
const RecentGapWindow = recentGapWindow

// CountOutages is the outage count read through the surface's own rule: what outage_count
// answers.
func CountOutages(ctx context.Context, host web.Host) (int, error) {
	statuses, err := newSurface(host).readSourceStatuses(ctx)
	if err != nil {
		return 0, err
	}
	return len(findOutages(statuses)), nil
}
