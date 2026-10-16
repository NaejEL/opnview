package derive

import "context"

// What this package's external tests call that the package keeps unexported.

// RefreshInstant is refreshInstant.
func (c *Deriver) RefreshInstant() (int64, int64) { return c.refreshInstant() }

// JoinFlowRecords is joinFlowRecords.
func (c *Deriver) JoinFlowRecords(ctx context.Context) (int64, error) { return c.joinFlowRecords(ctx) }
