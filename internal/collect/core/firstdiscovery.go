package core

// The end of the run's first discovery pass. At start every loop runs at once, and a pass that
// reads what discovery publishes -- the interfaces a measurement pass samples -- found the snapshot
// still empty: on a live firewall the first measurement pass of a run sampled nothing and its
// interface counters were lost (N5, live validation of 17 October 2026). Such a pass waits for
// this instead.

// firstDiscoveryChannel returns the channel closed when the first discovery pass has ended,
// making it once.
func (c *Collector) firstDiscoveryChannel() chan struct{} {
	c.firstDiscoveryMade.Do(func() { c.firstDiscovery = make(chan struct{}) })
	return c.firstDiscovery
}

// FirstDiscovery is closed once the run's first discovery pass has ended, whether or not every
// endpoint answered: what answered is published, and a pass waiting on it reads that.
func (c *Collector) FirstDiscovery() <-chan struct{} { return c.firstDiscoveryChannel() }

// DiscoveryEnded records that a discovery pass has published its snapshot. The first call
// releases every pass waiting on FirstDiscovery; later calls change nothing.
func (c *Collector) DiscoveryEnded() {
	c.firstDiscoveryClosed.Do(func() { close(c.firstDiscoveryChannel()) })
}
