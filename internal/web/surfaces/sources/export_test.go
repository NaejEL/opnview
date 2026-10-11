package sources

import (
	"context"

	"github.com/NaejEL/opnview/internal/web"
)

// What the external tests of this surface need of its internals.

// RecentGapWindow is how far back the surface lists collection gaps.
const RecentGapWindow = recentGapWindow

// PathParts cuts a probe after each slash.
var PathParts = pathParts

// OpnsensePagesFor names the OPNsense pages that turn a provider on.
var OpnsensePagesFor = opnsensePagesFor

// CountOutages is the indicator's own count, read through the surface's own rule.
func CountOutages(ctx context.Context, host web.Host) (int, error) {
	statuses, err := newSurface(host).readSourceStatuses(ctx)
	if err != nil {
		return 0, err
	}
	return countOutages(statuses), nil
}
