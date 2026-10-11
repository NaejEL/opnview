package push

import (
	"net/netip"
	"sort"
	"sync"
)

// Senders is the accepted-sender set (PS6 of the push amendment): ONLY THE CONFIGURED FIREWALL.
//
// The set is derived at run time and never written down: the addresses the configured base URL's
// host resolves to, and every address discovery reports for the firewall's own interfaces. Whoever
// owns those facts calls Set when either changes, with no restart. A datagram or a connection from
// any other address is refused before it is decoded, and nothing of it is stored; each refusal is
// counted per source address, and the addresses are listed in the API catalogue, so a container
// network that rewrites source addresses is visible at once.
//
// WHAT THIS DOES NOT PROVE. It is a source-address check. It refuses every other sender; it does
// not authenticate the firewall, and a forged source address on the path passes it.
type Senders struct {
	mutex    sync.RWMutex
	accepted map[netip.Addr]bool
	derived  bool
	refused  map[netip.Addr]int64
	overflow int64
}

// maxRefusedListed bounds the refused addresses listed; any further one is counted in one figure.
const maxRefusedListed = 256

// NewSenders returns an empty set: until Set is called nothing is accepted.
func NewSenders() *Senders {
	return &Senders{accepted: map[netip.Addr]bool{}, refused: map[netip.Addr]int64{}}
}

// Set replaces the accepted addresses.
func (s *Senders) Set(addresses []netip.Addr) {
	accepted := make(map[netip.Addr]bool, len(addresses))
	for _, address := range addresses {
		if address.IsValid() {
			accepted[address.Unmap()] = true
		}
	}
	s.mutex.Lock()
	s.accepted, s.derived = accepted, true
	s.mutex.Unlock()
}

// Accepts reports whether an address may send, counting a refusal.
func (s *Senders) Accepts(address netip.Addr) bool {
	address = address.Unmap()
	s.mutex.RLock()
	accepted := s.accepted[address]
	s.mutex.RUnlock()
	if accepted {
		return true
	}
	s.mutex.Lock()
	if _, listed := s.refused[address]; listed || len(s.refused) < maxRefusedListed {
		s.refused[address]++
	} else {
		s.overflow++
	}
	s.mutex.Unlock()
	return false
}

// RefusedSender is one refused address and how often it was refused.
type RefusedSender struct {
	Address string `json:"address"`
	Count   int64  `json:"count"`
}

// SendersState is what the catalogue lists of the set: its size, whether it has been derived yet,
// and the refused senders. It never lists the accepted addresses, which are the firewall's.
type SendersState struct {
	Accepted        int             `json:"accepted_addresses"`
	Derived         bool            `json:"derived"`
	Refused         []RefusedSender `json:"refused_senders"`
	RefusedUnlisted int64           `json:"refused_unlisted"`
}

// State reports the set for the catalogue.
func (s *Senders) State() SendersState {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	state := SendersState{Accepted: len(s.accepted), Derived: s.derived, Refused: []RefusedSender{},
		RefusedUnlisted: s.overflow}
	for address, count := range s.refused {
		state.Refused = append(state.Refused, RefusedSender{Address: address.String(), Count: count})
	}
	sort.Slice(state.Refused, func(i, j int) bool { return state.Refused[i].Address < state.Refused[j].Address })
	return state
}
