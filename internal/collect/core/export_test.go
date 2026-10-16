package core

// What this package's external tests read that the package keeps unexported.

// ReadDetail is readDetail.
func (c *Collector) ReadDetail(providerID int64) string { return c.readDetail(providerID) }

// Triggers is triggers.
type Triggers = triggers
