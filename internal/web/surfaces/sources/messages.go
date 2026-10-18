package sources

// The catalogue keys this surface's code chooses. A handler chooses a key, never a string; a
// JSON answer carries its closed code (web.ConditionOf), which the front translates through the
// catalogue. The OPNsense pages a provider links to are named in its plugin's own catalogue.

const (
	msgAvailabilityNotCollecting messageKey = "state.availability.not_collecting"
)

// keys is every key above, so a test can assert that each exists in the catalogue.
func keys() []messageKey {
	return []messageKey{
		msgAvailabilityNotCollecting,
	}
}
