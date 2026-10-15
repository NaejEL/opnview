package core

import "context"

// When the unresolved host-name lookups are examined again.
//
// A lookup whose `client` the query report gave as a host name, and which no single
// address answered for when it was read, is examined ONCE more, and marked: the mark is
// what keeps the retry bounded and what lets it survive a restart (cause H6 of the step-5A
// live corrections). Since the resolver-cache cycle two sources can name such a host --
// the DHCP leases, and the resolver's local data -- so the one examination waits until
// every source IN USE has been read after the lookup was ingested. Spending it on a pass
// that had read one source and not the other would leave the lookup unresolved for good
// whenever the other source was the one that named it.
//
// A source is in use when the probe round, or the operator, activated a provider of its kind. The
// lease side keeps its own rule for a backend that keeps failing (lease_backend_failure_pass_limit,
// in internal/collect/kinds/leases/dhcplease.go); a local-data read that failed does not hold the
// lookups back, and its failure is in its own availability row. The instants are this run's and are
// kept in memory: after a restart the examination waits until both sources have been read once,
// which costs at most one interval of the slower and loses nothing, because the mark is stored.

// noteLeaseRead records a complete lease pass whose read began at readAt, and returns the
// instant before which the unresolved lookups may now be examined, and whether they may
// be.
func (c *Collector) NoteLeaseRead(ctx context.Context, readAt int64) (int64, bool, error) {
	localInUse, err := c.kindInUse(ctx, KindResolverLocalData)
	if err != nil {
		return 0, false, err
	}
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	c.LeaseReadAt = readAt
	switch {
	case !localInUse || c.LocalDataFailed:
		return readAt, true, nil
	case c.LocalDataReadAt == 0:
		return 0, false, nil
	}
	return min(readAt, c.LocalDataReadAt), true, nil
}

// noteLocalDataRead records a successful local-data pass whose read began at readAt, and
// returns the instant before which the unresolved lookups may now be examined, and
// whether they may be.
func (c *Collector) NoteLocalDataRead(ctx context.Context, readAt int64) (int64, bool, error) {
	leasesInUse, err := c.kindInUse(ctx, KindDHCPLease)
	if err != nil {
		return 0, false, err
	}
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	c.LocalDataReadAt, c.LocalDataFailed = readAt, false
	switch {
	case !leasesInUse:
		return readAt, true, nil
	case c.LeaseReadAt == 0:
		return 0, false, nil
	}
	return min(readAt, c.LeaseReadAt), true, nil
}

// noteLocalDataFailed records a local-data pass that read no source.
func (c *Collector) NoteLocalDataFailed() {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	c.LocalDataFailed = true
}

// kindInUse reports whether any provider of a kind is active.
func (c *Collector) kindInUse(ctx context.Context, kind string) (bool, error) {
	sources, err := c.ActiveSources(ctx, kind)
	if err != nil {
		return false, err
	}
	return len(sources) > 0, nil
}
