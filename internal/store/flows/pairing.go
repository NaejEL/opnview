package flows

// The pairing of the two legs of one connection is the clients plugin's (plugins/clients, ADR 0020
// wave 3), as one SQL statement. What stays here is the setting it reads and the outcomes it writes,
// which the core's configuration and the widget engine still name.

// PairingWindowKey is the setting row holding how far apart, in seconds, the two records of one
// connection may have been logged, and DefaultPairingWindowSeconds the window when the row is absent:
// the maintainer's decision 4. The schema writes the default row; internal/config refuses a malformed
// one.
const (
	PairingWindowKey            = "leg_pairing_window_seconds"
	DefaultPairingWindowSeconds = 1
)

// The pairing outcomes, stored in flow.pair_outcome. The terms are opnview's own: pf and OPNsense log
// records and have no word for one being the second of a pair.
const (
	PairFirstLeg  = "first_leg"
	PairSecondLeg = "second_leg"
	PairNotPaired = "not_paired"
)
