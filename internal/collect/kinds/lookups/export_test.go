package lookups

import (
	"context"

	"github.com/NaejEL/opnview/internal/collect/dnslookup"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// BuildDNSResolution is buildDNSResolution, for this package's external tests.
func (c *LookupCoordinator) BuildDNSResolution(ctx context.Context, record dnslookup.Record, resolver string,
	now int64) (vocab.DNSResolution, error) {
	return c.buildDNSResolution(ctx, record, resolver, now)
}
