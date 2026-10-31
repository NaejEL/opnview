package figures

import "github.com/NaejEL/opnview/internal/widget/envelope"

// The outside peers and their unplaced bins are the figures library's peers area since ADR 0021
// (plugins/figures); the two types below stay for the declarative shape engine (shapes/shape_map.go)
// until ADR 0017 deletes it.

// UnplacedBin is the volume one reason left unplaced.
type UnplacedBin struct {
	Reason    string          `json:"reason"`
	Addresses int64           `json:"addresses"`
	Volume    envelope.Volume `json:"volume"`
	// Members lists the bin's addresses with their figures, which sum to the bin, for bins that
	// list them: an unnamed destination is shown by its address, never only folded into a count.
	Members *[]UnplacedMember `json:"members,omitempty"`
}

// UnplacedMember is one address of a listed bin.
type UnplacedMember struct {
	Address string          `json:"address"`
	Volume  envelope.Volume `json:"volume"`
}
