package filterlog

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/derive"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// What this package's external tests call that the package keeps unexported.

// PullPlanKeeping is a pull plan that stores the records it is given.
func PullPlanKeeping(records []vocab.Flow) pullPlan { return pullPlan{keep: records} }

// ReportedFlows are a records answer's records as the pass reads them.
func ReportedFlows(answer provider.Answer) []vocab.Flow {
	var records []vocab.Flow
	for _, record := range provider.Records(answer, "records") {
		records = append(records, reportedFlow(record))
	}
	return records
}

// StoreFirewallLog is storeFirewallLog.
func (c *FilterLogCoordinator) StoreFirewallLog(ctx context.Context, records []vocab.Flow, plan pullPlan,
	snapshot core.Discovery, now, providerID, newestStored int64, hasStored bool, stored *derive.Derivation) error {
	return c.storeFirewallLog(ctx, records, plan, snapshot, now, providerID, newestStored, hasStored, stored)
}
