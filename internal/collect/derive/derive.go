package derive

// BeginIngest returns the instant a pass stamps its rows' ingested_at with, and records it as in
// flight until EndIngest.
//
// WHY THE WATERMARK NEEDS IT. A pass stamps every row with the instant it began and commits them
// one by one afterwards, while the scheduler runs the other passes in their own goroutines. The
// rollups plugin's refresh reads the flows ingested since its last run and stamps each slot it
// rewrites with its watermark; a refresh that ran between the stamp and the commit, and moved its
// watermark to its own instant, would leave those rows stamped BEFORE the watermark and unread: no
// later run would select their slots. So the instant is taken under the same lock that records it,
// and the watermark the core hands a derivation (:ingest_watermark) never passes an instant still in
// flight.
func (c *Deriver) BeginIngest() (int64, uint64) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	now := c.Now()
	if c.Ingesting == nil {
		c.Ingesting = map[uint64]int64{}
	}
	c.IngestSequence++
	c.Ingesting[c.IngestSequence] = now
	return now, c.IngestSequence
}

// EndIngest records that the pass holding token has written every row it stamped. Ending twice is
// harmless.
func (c *Deriver) EndIngest(token uint64) {
	c.Mutex.Lock()
	defer c.Mutex.Unlock()
	delete(c.Ingesting, token)
}
