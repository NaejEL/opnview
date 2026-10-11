package live

import (
	"sync"
	"time"
)

// Interest is the live-interest lease: renewed by every authenticated request for a live figure,
// held for the lease length after the last renewal. Its zero value is not usable; New is.
//
// It is safe for concurrent use: every widget request renewing it and the collector's live loop
// reading it run at once.
type Interest struct {
	mutex sync.Mutex
	// length returns the lease length in force, asked at every renewal and every read so a saved
	// live_interest_seconds takes effect at once.
	length func() time.Duration
	// until is when the lease lapses; zero until the first renewal.
	until time.Time
	// renewals counts the renewals, for a test.
	renewals uint64
}

// DefaultLength is the lease a holder built with no length function grants: the documented
// default of live_interest_seconds (internal/config, DefaultLiveInterestSeconds).
const DefaultLength = 30 * time.Second

// New returns a lease whose length the given function answers; nil grants DefaultLength.
func New(length func() time.Duration) *Interest {
	if length == nil {
		length = func() time.Duration { return DefaultLength }
	}
	return &Interest{length: length}
}

// lengthNow is the lease length in force, DefaultLength when the function answers nothing positive.
func (i *Interest) lengthNow() time.Duration {
	if length := i.length(); length > 0 {
		return length
	}
	return DefaultLength
}

// Renew extends the lease to the lease length after now. A nil holder renews nothing, so a
// service built without live sampling serves its live figures all the same.
func (i *Interest) Renew(now time.Time) {
	if i == nil {
		return
	}
	i.mutex.Lock()
	defer i.mutex.Unlock()
	until := now.Add(i.lengthNow())
	if until.After(i.until) {
		i.until = until
	}
	i.renewals++
}

// Held reports whether the lease holds at now: somebody asked for a live figure within the lease
// length. A nil holder never holds.
func (i *Interest) Held(now time.Time) bool {
	if i == nil {
		return false
	}
	i.mutex.Lock()
	defer i.mutex.Unlock()
	return !i.until.IsZero() && now.Before(i.until)
}

// Until is when the lease lapses, and false when it was never renewed.
func (i *Interest) Until() (time.Time, bool) {
	if i == nil {
		return time.Time{}, false
	}
	i.mutex.Lock()
	defer i.mutex.Unlock()
	return i.until, !i.until.IsZero()
}

// Renewals is how many times the lease was renewed.
func (i *Interest) Renewals() uint64 {
	if i == nil {
		return 0
	}
	i.mutex.Lock()
	defer i.mutex.Unlock()
	return i.renewals
}
