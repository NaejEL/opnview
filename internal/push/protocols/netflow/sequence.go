package netflow

import (
	"fmt"
	"time"

	"github.com/NaejEL/opnview/internal/push"
)

// The sequence numbers (NJ7 of the amendment "the NetFlow join with every interface captured" to
// specs/SPEC-backend-completion-plugins.md). RFC 3954 and RFC 7011 give one sequence to an observation
// domain, but an exporter may run several counters under one (survey, Push sources, S11), so the decoder
// follows several:
//
//   - a datagram continues the counter whose expected next sequence number is its own;
//   - otherwise it opens a counter of its own, while fewer are open than the exporter runs;
//   - with as many open as the exporter runs, a datagram continuing none shows a loss: the counter
//     nearest behind it, within the forward window, lost the datagrams between, and a gap of that many
//     is asserted;
//   - a datagram behind every counter is a counter recreated, which replaces the one continued longest
//     ago. A datagram reordered on the way can be taken for a loss of another counter: the decoder has
//     no way to tell, and UDP on one link seldom reorders.
//
// How many counters the exporter runs is told by whoever reads its configuration (SetSequenceCounters);
// the decoder names nothing of any exporter. 0 is a bound not known: no loss is then asserted. The
// exporter's uptime going backwards is a restart, which every counter is followed afresh after, and
// which is told to the subscribers as the condition ReasonExporterRestarted.

const (
	// sequenceCounterLimit is the most counters followed under one observation domain when the bound is
	// not known.
	sequenceCounterLimit = 64
	// restartMarginMilliseconds is how far the boot instant a datagram implies may move forward before
	// the exporter is taken as restarted: its export time and uptime have whole-second resolution
	// (survey S10), so one datagram's may differ from another's by up to a second each way.
	restartMarginMilliseconds = 2000
)

// sequenceCounter is one counter followed under an observation domain.
type sequenceCounter struct {
	expected   uint32
	lastExport time.Time
	// loose says the counter's next sequence number is not known -- the datagram that set it held sets
	// whose records were not counted -- so the next datagram it meets resumes it without a loss.
	loose bool
	// lastUsed orders the counters by how recently a datagram continued them.
	lastUsed uint64
}

// sequenceState follows one scope's sequence numbers.
type sequenceState struct {
	counters []*sequenceCounter
	used     uint64
	// boot is the exporter's boot instant, in milliseconds, as its export time and uptime imply it.
	boot      int64
	bootKnown bool
}

// SetSequenceCounters sets how many counters the exporter runs under one observation domain; 0 is not
// known (push.SequenceBounder).
func (d *decoder) SetSequenceCounters(counters int) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.counters = max(counters, 0)
}

// ResetSequences forgets every scope's counters: each is followed afresh from the next datagram.
// Whoever reads the exporter's configuration calls it when that configuration changes, the counters
// being recreated then (push.SequenceResetter).
func (d *decoder) ResetSequences() {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	for _, state := range d.scopes {
		state.sequence = sequenceState{}
	}
}

// followSequence follows a datagram's sequence number. advance is how far it moves its counter -- one
// datagram for v9, its data records for IPFIX -- and uncounted says the datagram held records that
// were not counted, so its counter's next number is not known.
func (d *decoder) followSequence(state *scopeState, header packetHeader, advance uint32, uncounted bool,
	_ push.Meta, decoded *push.Decoded) {
	sequence := &state.sequence
	if header.version == versionV9 {
		boot := header.exportTime.UnixMilli() - int64(header.sysUptime)
		switch {
		case !sequence.bootKnown:
			sequence.boot, sequence.bootKnown = boot, true
		case boot > sequence.boot+restartMarginMilliseconds:
			// The uptime went backwards: the exporter restarted, and its counters with it.
			*sequence = sequenceState{boot: boot, bootKnown: true}
			decoded.Problems = append(decoded.Problems, push.Problem{Reason: ReasonExporterRestarted, Count: 1,
				Condition: true, From: header.exportTime, To: header.exportTime, Scope: state.name,
				Detail: "the exporter's uptime went backwards: it restarted, and its sequence counters with it"})
		case boot < sequence.boot:
			sequence.boot = boot
		}
	}
	sequence.used++
	set := func(counter *sequenceCounter) {
		counter.expected, counter.lastExport = header.sequence+advance, header.exportTime
		counter.loose, counter.lastUsed = uncounted, sequence.used
	}
	for _, counter := range sequence.counters {
		if counter.expected == header.sequence && !counter.loose {
			set(counter)
			return
		}
	}
	if loose := nearestBehind(sequence.counters, header.sequence, true); loose != nil {
		// The counter whose next number was not known resumes here, with no loss asserted.
		set(loose)
		return
	}
	if d.counters == 0 || len(sequence.counters) < d.counters {
		if d.counters == 0 && len(sequence.counters) >= sequenceCounterLimit {
			sequence.drop(sequence.leastRecentlyUsed())
		}
		counter := &sequenceCounter{}
		set(counter)
		sequence.counters = append(sequence.counters, counter)
		return
	}
	// As many counters are open as the exporter runs: the nearest counter behind this datagram lost the
	// datagrams between.
	nearest := nearestBehind(sequence.counters, header.sequence, false)
	if nearest == nil {
		// Behind every counter: a counter recreated. The one least recently continued gives way to it.
		set(sequence.leastRecentlyUsed())
		return
	}
	missing := header.sequence - nearest.expected
	decoded.Problems = append(decoded.Problems, push.Problem{Reason: ReasonSequenceGap, Count: int(missing),
		Gap: true, From: nearest.lastExport, To: header.exportTime, Sequence: true, Scope: state.name,
		Detail: fmt.Sprintf("an exporter's sequence counter jumped by %d", missing)})
	set(nearest)
}

// nearestBehind is the counter, loose or not as asked, whose expected next number is nearest behind a
// sequence number within the forward window; nil when none is.
func nearestBehind(counters []*sequenceCounter, sequence uint32, loose bool) *sequenceCounter {
	var nearest *sequenceCounter
	var distance uint32
	for _, counter := range counters {
		ahead := sequence - counter.expected
		if counter.loose != loose || ahead == 0 || ahead >= sequenceForwardWindow {
			continue
		}
		if nearest == nil || ahead < distance {
			nearest, distance = counter, ahead
		}
	}
	return nearest
}

// leastRecentlyUsed is the counter a datagram continued longest ago.
func (s *sequenceState) leastRecentlyUsed() *sequenceCounter {
	oldest := s.counters[0]
	for _, counter := range s.counters[1:] {
		if counter.lastUsed < oldest.lastUsed {
			oldest = counter
		}
	}
	return oldest
}

// drop removes a counter.
func (s *sequenceState) drop(gone *sequenceCounter) {
	kept := s.counters[:0]
	for _, counter := range s.counters {
		if counter != gone {
			kept = append(kept, counter)
		}
	}
	s.counters = kept
}
