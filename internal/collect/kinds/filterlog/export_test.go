package filterlog

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/collect/firewalllog"
)

// What this package's external tests call that the package keeps unexported.

// PullPlanKeeping is a pull plan that stores the records it is given.
func PullPlanKeeping(records []firewalllog.Record) pullPlan { return pullPlan{keep: records} }

// StoreFirewallLog is storeFirewallLog.
func (c *FilterLogCoordinator) StoreFirewallLog(ctx context.Context, records []firewalllog.Record, plan pullPlan,
	snapshot core.Discovery, now, providerID, newestStored int64, hasStored bool, stored *derive.Derivation) error {
	return c.storeFirewallLog(ctx, records, plan, snapshot, now, providerID, newestStored, hasStored, stored)
}
