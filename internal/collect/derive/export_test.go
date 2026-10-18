package derive

// What this package's external tests call that the package keeps unexported.

// RefreshInstant is refreshInstant.
func (c *Deriver) RefreshInstant() (int64, int64) { return c.refreshInstant() }
