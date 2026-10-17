package lookups

import (
	"context"

	"github.com/NaejEL/opnview/internal/store/vocab"
)

// BuildDNSResolution is buildDNSResolution, for this package's external tests: record holds what a resolver reported.
func (c *LookupCoordinator) BuildDNSResolution(ctx context.Context, record vocab.DNSResolution, resolver string,
	now int64) (vocab.DNSResolution, error) {
	return c.buildDNSResolution(ctx, record, resolver, now)
}
