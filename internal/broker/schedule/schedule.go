package schedule

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// Clock is the scheduler's clock: the system's, or a test's.
type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

// SystemClock is the wall clock.
type SystemClock struct{}

// Now is the current time.
func (SystemClock) Now() time.Time { return time.Now() }

// After waits on the wall clock.
func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Class is a datum's class.
type Class int

// The two classes.
const (
	Background Class = iota
	RealTime
)

// String names a class.
func (c Class) String() string {
	if c == RealTime {
		return "real_time"
	}
	return "background"
}

// Settings are the scheduler's settings, each a row of the setting table (docs/plugins.md).
type Settings struct {
	// MaxInFlight is the most calls in flight toward the firewall.
	MaxInFlight int
	// MaxPerSecond is the most calls that leave per second.
	MaxPerSecond float64
	// RealTimeShare is the share of calls reserved for real-time data while both classes wait.
	RealTimeShare float64
	// RealTimeMaxAge is the threshold: a datum whose shortest live max age is at or below it is
	// real-time.
	RealTimeMaxAge time.Duration
	// BackoffBase and BackoffMax bound a datum's, and the queue's, exponential back-off.
	BackoffBase time.Duration
	BackoffMax  time.Duration
	// QueueFailureEndpoints is how many endpoints failing together back off the whole queue.
	QueueFailureEndpoints int
}

// DefaultSettings are the defaults of A12.
func DefaultSettings() Settings {
	return Settings{MaxInFlight: 2, MaxPerSecond: 5, RealTimeShare: 0.7, RealTimeMaxAge: 5 * time.Second,
		BackoffBase: 2 * time.Second, BackoffMax: 5 * time.Minute, QueueFailureEndpoints: 3}
}

// Outcome is how a call ended, as the scheduler counts it.
type Outcome int

// The outcomes.
const (
	// Succeeded is an answer the firewall gave, whatever its body: it ends every back-off.
	Succeeded Outcome = iota
	// Failed is a server error, a transport failure or a time-out: it backs off.
	Failed
	// Refused is not_found or forbidden: a state, never backed off.
	Refused
)

// Refresh performs one call for a datum.
type Refresh func(ctx context.Context) Outcome

// ErrBackingOff is a call not made because its datum, or the queue, is backing off.
var ErrBackingOff = errors.New("schedule: backing off")

// datum is one datum's scheduling state.
type datum struct {
	key      string
	endpoint string
	demands  map[string]demand
	// renewals is each requester's last renewal, kept after its demand lapsed, so the time between
	// two renewals is known even when a demand lapses between them.
	renewals map[string]time.Time
	floor    time.Duration
	refresh  Refresh

	lastFetch    time.Time
	lastAttempt  time.Time
	failures     int
	backoffUntil time.Time

	queued   *job
	inFlight bool
}

type demand struct {
	maxAge  time.Duration
	renewed time.Time
	// every is the time between the requester's last two renewals, zero before a second one.
	every time.Duration
	// suspended says the demand does not refresh the datum now: a declared stream replaces it.
	suspended bool
}

// job is one call due.
type job struct {
	datum     *datum
	deadline  time.Time
	class     Class
	run       Refresh
	waiters   []chan result
	proactive bool
}

type result struct {
	outcome Outcome
	err     error
}

// Stats are the counters a test and the catalogue read.
type Stats struct {
	Calls           int
	RealTimeCalls   int
	BackgroundCalls int
	MaxInFlight     int
	// MaxPerWindow is the most calls that left within any one second.
	MaxPerWindow int
}

// Scheduler is the scheduler.
type Scheduler struct {
	clock    Clock
	mutex    sync.Mutex
	settings Settings
	data     map[string]*datum
	queue    []*job
	inFlight int
	starts   []time.Time
	// competing are the last decisions taken while both classes had a call due, true for a
	// real-time call.
	competing []bool
	// endpointFailures are the endpoints whose last call failed, with when.
	endpointFailures map[string]time.Time
	queueFailures    int
	queueUntil       time.Time
	wake             chan struct{}
	stats            Stats
	// started records the instant of every call, for the tests' rate maxima.
	window []time.Time
	// base is the context dispatched calls run under: Run's, or the background before Run.
	base context.Context
	// timer says a wait for the rate ceiling is pending.
	timer bool
}

// New returns a scheduler.
func New(clock Clock, settings Settings) *Scheduler {
	if clock == nil {
		clock = SystemClock{}
	}
	return &Scheduler{clock: clock, settings: normalise(settings), data: map[string]*datum{},
		endpointFailures: map[string]time.Time{}, wake: make(chan struct{}, 1), base: context.Background()}
}

func normalise(settings Settings) Settings {
	defaults := DefaultSettings()
	if settings.MaxInFlight <= 0 {
		settings.MaxInFlight = defaults.MaxInFlight
	}
	if settings.MaxPerSecond <= 0 {
		settings.MaxPerSecond = defaults.MaxPerSecond
	}
	if settings.RealTimeShare <= 0 || settings.RealTimeShare >= 1 {
		settings.RealTimeShare = defaults.RealTimeShare
	}
	if settings.RealTimeMaxAge <= 0 {
		settings.RealTimeMaxAge = defaults.RealTimeMaxAge
	}
	if settings.BackoffBase <= 0 {
		settings.BackoffBase = defaults.BackoffBase
	}
	if settings.BackoffMax <= 0 {
		settings.BackoffMax = defaults.BackoffMax
	}
	if settings.QueueFailureEndpoints <= 0 {
		settings.QueueFailureEndpoints = defaults.QueueFailureEndpoints
	}
	return settings
}

// SetSettings replaces the settings; a saved setting reaches the next decision.
func (s *Scheduler) SetSettings(settings Settings) {
	s.mutex.Lock()
	s.settings = normalise(settings)
	s.mutex.Unlock()
	s.signal()
}

// Settings returns the settings in force.
func (s *Scheduler) Settings() Settings {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.settings
}

func (s *Scheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// entry returns a datum's state, creating it.
func (s *Scheduler) entry(key, endpoint string) *datum {
	entry, found := s.data[key]
	if !found {
		entry = &datum{key: key, endpoint: endpoint, demands: map[string]demand{}, renewals: map[string]time.Time{}}
		s.data[key] = entry
	}
	return entry
}

// Demand registers or renews a requester's demand for a datum at a max age, and gives the datum
// the call that refreshes it and its freshness floor. It returns the max age in force: the
// requester's, raised to the floor.
func (s *Scheduler) Demand(key, endpoint, requester string, maxAge, floor time.Duration, refresh Refresh) time.Duration {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	entry := s.entry(key, endpoint)
	if floor > entry.floor {
		entry.floor = floor
	}
	if maxAge < entry.floor {
		maxAge = entry.floor
	}
	held := entry.demands[requester]
	now := s.clock.Now()
	var every time.Duration
	if last, renewed := entry.renewals[requester]; renewed && now.After(last) {
		every = now.Sub(last)
	}
	entry.renewals[requester] = now
	entry.demands[requester] = demand{maxAge: maxAge, renewed: now, every: every, suspended: held.suspended}
	if refresh != nil {
		entry.refresh = refresh
	}
	s.signal()
	return maxAge
}

// Suspend suspends or resumes one requester's demand for a datum: a declared stream replaces it
// while it runs (A14).
func (s *Scheduler) Suspend(key, requester string, suspended bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if entry, found := s.data[key]; found {
		if held, present := entry.demands[requester]; present {
			held.suspended = suspended
			entry.demands[requester] = held
		}
	}
	s.signal()
}

// lapse is how long a demand lives without renewal: twice its max age, and at least a minute,
// so a reader whose pass runs at its max age keeps its demand from one pass to the next.
//
// THE MINUTE DOES NOT OUTLIVE ITS INTENT. A requester whose renewals come a minute or more apart,
// and more than twice its max age apart, is a pass asking for a fresh answer at the moment it
// runs, not a reader that wants the datum kept fresh between its passes: the call itself already
// fetches when the held answer is too old. Its demand lapses at twice its max age. Without that, a
// pass every minute with a five-second max age renewed a demand that never lapsed, and the datum
// was refreshed every five seconds for good (live validation of 17 October 2026, D3).
func lapse(held demand) time.Duration {
	span := 2 * held.maxAge
	if span > time.Minute || (held.every >= time.Minute && held.every > span) {
		return span
	}
	return time.Minute
}

// liveMaxAge is the shortest max age among a datum's live demands, and whether there is one.
func (s *Scheduler) liveMaxAge(entry *datum, now time.Time) (time.Duration, bool) {
	shortest, found := time.Duration(0), false
	for requester, held := range entry.demands {
		if now.Sub(held.renewed) > lapse(held) {
			delete(entry.demands, requester)
			continue
		}
		if held.suspended {
			continue
		}
		if !found || held.maxAge < shortest {
			shortest, found = held.maxAge, true
		}
	}
	return shortest, found
}

// Interval is the refresh interval of a datum now: its shortest live max age, and false when no
// reader demands it.
func (s *Scheduler) Interval(key string) (time.Duration, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	entry, found := s.data[key]
	if !found {
		return 0, false
	}
	return s.liveMaxAge(entry, s.clock.Now())
}

// class is a datum's class from its live max age.
func (s *Scheduler) class(maxAge time.Duration) Class {
	if maxAge <= s.settings.RealTimeMaxAge {
		return RealTime
	}
	return Background
}

// Fetched records a datum's last fetch, so its next deadline follows from it.
func (s *Scheduler) Fetched(key string, at time.Time) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if entry, found := s.data[key]; found {
		if at.After(entry.lastFetch) {
			entry.lastFetch = at
		}
	}
}

// BackingOff reports whether a datum, or the queue, is backing off now.
func (s *Scheduler) BackingOff(key string) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	now := s.clock.Now()
	if now.Before(s.queueUntil) {
		return true
	}
	entry, found := s.data[key]
	return found && now.Before(entry.backoffUntil)
}

// Do runs one call for a datum now -- at the nearest deadline -- through the queue, and waits for
// it. A call already queued or in flight for the datum is joined, never repeated: the broker never
// has two calls in flight for one datum. A datum backing off is answered ErrBackingOff at once.
func (s *Scheduler) Do(ctx context.Context, key, endpoint string, maxAge time.Duration, run Refresh) (Outcome, error) {
	s.mutex.Lock()
	now := s.clock.Now()
	entry := s.entry(key, endpoint)
	if now.Before(s.queueUntil) || now.Before(entry.backoffUntil) {
		s.mutex.Unlock()
		return Failed, ErrBackingOff
	}
	waiter := make(chan result, 1)
	if entry.queued != nil {
		entry.queued.waiters = append(entry.queued.waiters, waiter)
		entry.queued.proactive = false
		if entry.queued.deadline.After(now) {
			entry.queued.deadline = now
		}
	} else {
		class := s.class(maxAge)
		if live, found := s.liveMaxAge(entry, now); found && s.class(live) == RealTime {
			class = RealTime
		}
		entry.queued = &job{datum: entry, deadline: now, class: class, run: run, waiters: []chan result{waiter}}
		s.queue = append(s.queue, entry.queued)
	}
	s.mutex.Unlock()
	s.kick()
	select {
	case got := <-waiter:
		return got.outcome, got.err
	case <-ctx.Done():
		return Failed, ctx.Err()
	}
}

// kick dispatches what the ceilings allow now, without waiting for the loop: a reader's call leaves
// at once when a slot is free, whether or not Run is running, and a finished call hands its slot
// to the next. A call the rate ceiling holds back leaves when its second has passed.
func (s *Scheduler) kick() {
	s.mutex.Lock()
	base := s.base
	s.mutex.Unlock()
	wait := s.step(base)
	s.mutex.Lock()
	blocked := len(s.queue) > 0 && !s.timer
	if blocked {
		s.timer = true
	}
	s.mutex.Unlock()
	if blocked {
		go func() {
			select {
			case <-s.clock.After(wait):
			case <-base.Done():
			}
			s.mutex.Lock()
			s.timer = false
			s.mutex.Unlock()
			if base.Err() == nil {
				s.kick()
			}
		}()
	}
}

// Run dispatches the queue until the context ends.
func (s *Scheduler) Run(ctx context.Context) {
	s.mutex.Lock()
	s.base = ctx
	s.mutex.Unlock()
	for {
		wait := s.step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-s.clock.After(wait):
		}
	}
}

// step queues the proactive refreshes that are due, dispatches what the ceilings allow, and says
// how long to wait before looking again.
func (s *Scheduler) step(ctx context.Context) time.Duration {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	now := s.clock.Now()
	next := time.Minute

	// The proactive refreshes: a datum with live demand whose deadline nears.
	for _, entry := range s.data {
		maxAge, live := s.liveMaxAge(entry, now)
		if !live || entry.refresh == nil || entry.queued != nil || entry.inFlight {
			continue
		}
		deadline := entry.lastFetch.Add(maxAge)
		if entry.lastFetch.IsZero() {
			deadline = now
		}
		// Refresh ahead of the deadline by a tenth of the max age, at most a second, so a
		// reader's pass finds the mirror fresh.
		lead := maxAge / 10
		if lead > time.Second {
			lead = time.Second
		}
		due := deadline.Add(-lead)
		if entry.backoffUntil.After(due) {
			due = entry.backoffUntil
		}
		if now.Before(due) {
			if wait := due.Sub(now); wait < next {
				next = wait
			}
			continue
		}
		entry.queued = &job{datum: entry, deadline: deadline, class: s.class(maxAge), run: entry.refresh,
			proactive: true}
		s.queue = append(s.queue, entry.queued)
	}

	if now.Before(s.queueUntil) {
		if wait := s.queueUntil.Sub(now); wait < next {
			next = wait
		}
		return next
	}

	for s.inFlight < s.settings.MaxInFlight && len(s.queue) > 0 {
		// The rate ceiling: at most MaxPerSecond starts in any second.
		cutoff := now.Add(-time.Second)
		kept := s.starts[:0]
		for _, at := range s.starts {
			if at.After(cutoff) {
				kept = append(kept, at)
			}
		}
		s.starts = kept
		if float64(len(s.starts)) >= s.settings.MaxPerSecond {
			if wait := s.starts[0].Add(time.Second).Sub(now); wait < next {
				next = wait
			}
			break
		}
		chosen := s.choose(now)
		if chosen == nil {
			break
		}
		s.dispatch(ctx, chosen, now)
	}
	return next
}

// choose picks the next call: nearest deadline first within a class, the real-time reserve
// between classes.
func (s *Scheduler) choose(now time.Time) *job {
	var realTime, background []*job
	for _, queued := range s.queue {
		// Never two calls in flight for one datum, and none during its back-off.
		if queued.datum.inFlight || now.Before(queued.datum.backoffUntil) {
			continue
		}
		if queued.class == RealTime {
			realTime = append(realTime, queued)
		} else {
			background = append(background, queued)
		}
	}
	earliest := func(jobs []*job) *job {
		sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].deadline.Before(jobs[j].deadline) })
		return jobs[0]
	}
	switch {
	case len(realTime) == 0 && len(background) == 0:
		return nil
	case len(realTime) == 0:
		return earliest(background)
	case len(background) == 0:
		return earliest(realTime)
	}
	// Both classes wait: real-time calls take at least the reserved share of the recent
	// competing decisions.
	taken := 0
	for _, wasRealTime := range s.competing {
		if wasRealTime {
			taken++
		}
	}
	pickRealTime := float64(taken) < s.settings.RealTimeShare*float64(len(s.competing)+1)
	s.competing = append(s.competing, pickRealTime)
	if len(s.competing) > 100 {
		s.competing = s.competing[len(s.competing)-100:]
	}
	if pickRealTime {
		return earliest(realTime)
	}
	return earliest(background)
}

// dispatch starts one call.
func (s *Scheduler) dispatch(ctx context.Context, chosen *job, now time.Time) {
	for index, queued := range s.queue {
		if queued == chosen {
			s.queue = append(s.queue[:index], s.queue[index+1:]...)
			break
		}
	}
	entry := chosen.datum
	entry.queued = nil
	entry.inFlight = true
	entry.lastAttempt = now
	s.inFlight++
	s.starts = append(s.starts, now)
	s.window = append(s.window, now)
	s.stats.Calls++
	if chosen.class == RealTime {
		s.stats.RealTimeCalls++
	} else {
		s.stats.BackgroundCalls++
	}
	if s.inFlight > s.stats.MaxInFlight {
		s.stats.MaxInFlight = s.inFlight
	}
	perWindow := 0
	for _, at := range s.window {
		if now.Sub(at) < time.Second {
			perWindow++
		}
	}
	if perWindow > s.stats.MaxPerWindow {
		s.stats.MaxPerWindow = perWindow
	}
	if len(s.window) > 1000 {
		s.window = s.window[len(s.window)-1000:]
	}
	run := chosen.run
	if run == nil {
		run = entry.refresh
	}
	go func() {
		outcome := Failed
		if run != nil {
			outcome = run(ctx)
		}
		s.finished(chosen, outcome)
	}()
}

// finished records a call's end: back-off on failure, the end of every back-off on success.
func (s *Scheduler) finished(done *job, outcome Outcome) {
	s.mutex.Lock()
	now := s.clock.Now()
	entry := done.datum
	entry.inFlight = false
	s.inFlight--
	switch outcome {
	case Succeeded, Refused:
		entry.failures = 0
		entry.backoffUntil = time.Time{}
		delete(s.endpointFailures, entry.endpoint)
		if outcome == Succeeded {
			s.queueFailures = 0
			s.queueUntil = time.Time{}
			entry.lastFetch = now
		}
		if outcome == Refused {
			// A state: retried at the readers' own cadence.
			entry.lastFetch = now
		}
	case Failed:
		entry.failures++
		entry.backoffUntil = now.Add(backoff(s.settings, entry.failures))
		s.endpointFailures[entry.endpoint] = now
		failing := 0
		for _, at := range s.endpointFailures {
			if now.Sub(at) <= s.settings.BackoffMax {
				failing++
			}
		}
		if failing >= s.settings.QueueFailureEndpoints {
			s.queueFailures++
			s.queueUntil = now.Add(backoff(s.settings, s.queueFailures))
		}
	}
	waiters := done.waiters
	s.mutex.Unlock()
	for _, waiter := range waiters {
		waiter <- result{outcome: outcome}
	}
	s.kick()
}

// backoff is the wait after the nth consecutive failure: the base doubled each time, up to the
// maximum.
func backoff(settings Settings, failures int) time.Duration {
	wait := settings.BackoffBase
	for index := 1; index < failures && wait < settings.BackoffMax; index++ {
		wait *= 2
	}
	if wait > settings.BackoffMax {
		wait = settings.BackoffMax
	}
	return wait
}

// Stats returns the counters.
func (s *Scheduler) Stats() Stats {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.stats
}

// Forget drops every datum: the firewall changed, its data are another firewall's, and nothing
// of the previous one is fresh or refreshed again. A reader already waiting keeps its call.
func (s *Scheduler) Forget() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	kept := s.queue[:0]
	for _, queued := range s.queue {
		if len(queued.waiters) > 0 {
			kept = append(kept, queued)
		} else {
			queued.datum.queued = nil
		}
	}
	s.queue = kept
	for key, entry := range s.data {
		if entry.queued == nil && !entry.inFlight {
			delete(s.data, key)
		}
	}
	s.queueUntil = time.Time{}
	s.queueFailures = 0
}
