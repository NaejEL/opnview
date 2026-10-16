package declared

import "time"

// OverriddenCadence is overriddenCadence, for this package's external tests.
func (c *DeclaredKindCoordinator) OverriddenCadence(settingKey string, declared, minimum time.Duration) func() time.Duration {
	return c.overriddenCadence(settingKey, declared, minimum)
}
