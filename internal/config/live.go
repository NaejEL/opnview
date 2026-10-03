package config

import (
	"sync"
	"time"
)

// Live is the configuration a running service reads, as opposed to the one it
// loaded at start-up.
//
// IT IS THE SEAM THAT MAKES A SAVED SETTING TAKE EFFECT WITHOUT A RESTART, the
// same seam FirewallCredentials is for the credentials. Load runs once; the
// collection surface stores a new interval or page size, loads the whole
// configuration again and hands it to Set, and every reader picks it up on its
// next read: the scheduler before it waits for the next pass, the collectors
// before they size their next page.
//
// It holds a whole Config rather than one field per setting, so that a reader
// never sees an interval from one save and a page size from another.
//
// Rebuilt after the loss of 2 October 2026 from the compiled package of that
// evening: the declarations and their behaviour are the compiled ones, the
// comments are rewritten.
//
// The zero value is not usable; NewLive is.
type Live struct {
	mutex      sync.RWMutex
	current    Config
	generation uint64
}

// NewLive returns a holder carrying the configuration the service started with.
// It takes a value rather than loading one, so the caller decides when to read.
func NewLive(settings Config) *Live {
	return &Live{current: settings}
}

// Get returns the configuration in force now.
func (l *Live) Get() Config {
	l.mutex.RLock()
	defer l.mutex.RUnlock()
	return l.current
}

// Set replaces the configuration. The next Get returns it.
func (l *Live) Set(settings Config) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.current = settings
	l.generation++
}

// Generation is how many times Set has been called, for a test to assert on.
func (l *Live) Generation() uint64 {
	l.mutex.RLock()
	defer l.mutex.RUnlock()
	return l.generation
}

// Interval turns one of the configuration's durations into the function a
// scheduled task asks before each wait, so a saved interval changes the next
// wait rather than the next start. The argument picks the field, for example
//
//	settings.Interval(func(c Config) time.Duration { return c.FirewallLogInterval })
func (l *Live) Interval(of func(Config) time.Duration) func() time.Duration {
	return func() time.Duration { return of(l.Get()) }
}
