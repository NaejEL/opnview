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
// THE OPERATOR'S EXTRA SENDERS (--push-extra-senders, open question 10 (b) of the push amendment) are
// kept apart from the derived set, so a refresh of the firewall's addresses never drops them. They
// exist for a network that rewrites the source of what reaches a published port -- Docker Desktop's
// port publishing hands every datagram and connection the bridge gateway's address -- and they widen
// the check to whatever reaches that port: the host has to filter the port to the firewall's address.
//
// WHAT THIS DOES NOT PROVE. It is a source-address check. It refuses every other sender; it does
// not authenticate the firewall, and a forged source address on the path passes it.
type Senders struct {
	mutex    sync.RWMutex
	accepted map[netip.Addr]bool
	extra    map[netip.Addr]bool
	derived  bool
	refused  map[netip.Addr]int64
	overflow int64
}

// maxRefusedListed bounds the refused addresses listed; any further one is counted in one figure.
const maxRefusedListed = 256

// NewSenders returns an empty set: until Set is called nothing is accepted.
func NewSenders() *Senders {
	return &Senders{accepted: map[netip.Addr]bool{}, extra: map[netip.Addr]bool{}, refused: map[netip.Addr]int64{}}
}

// Set replaces the derived accepted addresses; the operator's extra senders stay.
func (s *Senders) Set(addresses []netip.Addr) {
	accepted := addressSet(addresses)
	s.mutex.Lock()
	s.accepted, s.derived = accepted, true
	s.mutex.Unlock()
}

// SetExtra replaces the operator's extra accepted senders, which Set leaves in place.
func (s *Senders) SetExtra(addresses []netip.Addr) {
	extra := addressSet(addresses)
	s.mutex.Lock()
	s.extra = extra
	s.mutex.Unlock()
}

func addressSet(addresses []netip.Addr) map[netip.Addr]bool {
	set := make(map[netip.Addr]bool, len(addresses))
	for _, address := range addresses {
		if address.IsValid() {
			set[address.Unmap()] = true
		}
	}
	return set
}

// Accepts reports whether an address may send, counting a refusal.
func (s *Senders) Accepts(address netip.Addr) bool {
	address = address.Unmap()
	s.mutex.RLock()
	accepted := s.accepted[address] || s.extra[address]
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

// SendersState is what the catalogue lists of the set: its size, how many extra senders the operator
// added, whether it has been derived yet, and the refused senders. It never lists the accepted
// addresses, which are the firewall's and the operator's.
type SendersState struct {
	Accepted        int             `json:"accepted_addresses"`
	Extra           int             `json:"extra_accepted_addresses"`
	Derived         bool            `json:"derived"`
	Refused         []RefusedSender `json:"refused_senders"`
	RefusedUnlisted int64           `json:"refused_unlisted"`
}

// State reports the set for the catalogue.
func (s *Senders) State() SendersState {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	state := SendersState{Accepted: len(s.accepted), Extra: len(s.extra), Derived: s.derived,
		Refused: []RefusedSender{}, RefusedUnlisted: s.overflow}
	for address, count := range s.refused {
		state.Refused = append(state.Refused, RefusedSender{Address: address.String(), Count: count})
	}
	sort.Slice(state.Refused, func(i, j int) bool { return state.Refused[i].Address < state.Refused[j].Address })
	return state
}
