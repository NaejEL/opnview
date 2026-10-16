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

// IngestLease is ingestLease.
func (c *LeaseCoordinator) IngestLease(ctx context.Context, observation dhcplease.Observation,
	snapshot core.Discovery, providerID int64, backend string, now int64) error {
	return c.ingestLease(ctx, observation, snapshot, providerID, backend, now)
}

// LeaseFailurePasses is leaseFailurePasses.
func (c *LeaseCoordinator) LeaseFailurePasses(providerID int64) int64 {
	return c.leaseFailurePasses(providerID)
}
