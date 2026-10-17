package leases

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/collect/dhcplease"
)

// What this package's external tests call that the package keeps unexported.

// CollectLeases is collectLeases.
func (c *LeaseCoordinator) CollectLeases(ctx context.Context, seen *[]string) (bool, error) {
	return c.collectLeases(ctx, seen)
}

// IngestLease stores one observation as a pass stores its observations.
func (c *LeaseCoordinator) IngestLease(ctx context.Context, observation dhcplease.Observation,
	snapshot core.Discovery, providerID int64, backend string, now int64) error {
	return c.storeLeases(ctx, []dhcplease.Observation{observation}, snapshot, providerID, backend, now)
}

// LeaseFailurePasses is how many lease passes in a row one backend has failed.
func (c *LeaseCoordinator) LeaseFailurePasses(providerID int64) int64 {
	c.Mutex.RLock()
	defer c.Mutex.RUnlock()
	return c.LeaseFailures[providerID]
}
