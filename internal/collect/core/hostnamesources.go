package core

import (
	"context"
	"sort"

	"github.com/NaejEL/opnview/internal/collect/provider"
)

// When the unresolved host-name lookups are examined again.
//
// A lookup whose `client` the query report gave as a host name, and which no single
// address answered for when it was read, is examined ONCE more, and marked: the mark is
// what keeps the retry bounded and what lets it survive a restart (cause H6 of the step-5A
// live corrections). Several kinds can name such a host -- the DHCP leases, and the
// resolver's local data -- each marked `names_hosts` in its kind file, so the one
// examination waits until every such kind IN USE has been read after the lookup was
// ingested. Spending it on a pass that had read one source and not the other would leave
// the lookup unresolved for good whenever the other source was the one that named it.
//
// A kind is in use when the probe round, or the operator, activated a provider of it. A read
// that failed does not hold the lookups back once its pass says so (NoteHostNameSourceFailed),
// and its failure is in its own availability row; the lease side never says so, keeping its
// own rule for a backend that keeps failing (once lease_backend_failure_pass_limit, in
// internal/collect/kinds/leases/dhcplease.go). The instants are this run's and are kept in
// memory: after a restart the examination waits until every source has been read once, which
// costs at most one interval of the slowest and loses nothing, because the mark is stored.

// NoteHostNameSourceRead records a pass of a host-naming kind whose read began at readAt, and returns
// the instant before which the unresolved lookups may now be examined, and whether they may be.
func (c *Collector) NoteHostNameSourceRead(ctx context.Context, kind string, readAt int64) (int64, bool, error) {
	var inUse []string
	for _, contract := range provider.Kinds() {
		if !contract.NamesHosts || contract.Kind == kind {
			continue
		}
		used, err := c.kindInUse(ctx, contract.Kind)
		if err != nil {
			return 0, false, err
		}
		if used {
			inUse = append(inUse, contract.Kind)
		}
	}
	sort.Strings(inUse)
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	if c.HostNameReads == nil {
		c.HostNameReads = map[string]int64{}
	}
	c.HostNameReads[kind] = readAt
	delete(c.HostNameFailed, kind)
	before := readAt
	for _, other := range inUse {
		if c.HostNameFailed[other] {
			continue
		}
		at := c.HostNameReads[other]
		if at == 0 {
			return 0, false, nil
		}
		before = min(before, at)
	}
	return before, true, nil
}

// NoteHostNameSourceFailed records a pass of a host-naming kind that read no source: it holds the
// lookups back no longer, until its next successful read.
func (c *Collector) NoteHostNameSourceFailed(kind string) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	if c.HostNameFailed == nil {
		c.HostNameFailed = map[string]bool{}
	}
	c.HostNameFailed[kind] = true
}

// kindInUse reports whether any provider of a kind is active.
func (c *Collector) kindInUse(ctx context.Context, kind string) (bool, error) {
	sources, err := c.ActiveSources(ctx, kind)
	if err != nil {
		return false, err
	}
	return len(sources) > 0, nil
}
