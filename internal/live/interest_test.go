package live

import (
	"sync"
	"testing"
	"time"
)

func TestLiveCadenceAndInterestTheLeaseHoldsForItsLengthAfterTheLastRenewal(t *testing.T) {
	start := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	length := 30 * time.Second
	interest := New(func() time.Duration { return length })
	if interest.Held(start) {
		t.Fatal("a lease never renewed holds")
	}
	interest.Renew(start)
	if !interest.Held(start.Add(29 * time.Second)) {
		t.Error("the lease lapsed before its length")
	}
	if interest.Held(start.Add(30 * time.Second)) {
		t.Error("the lease holds at its length after the renewal")
	}
	// A renewal at 20 s extends it to 50 s; an older renewal arriving late shortens nothing.
	interest.Renew(start.Add(20 * time.Second))
	interest.Renew(start.Add(5 * time.Second))
	if !interest.Held(start.Add(49 * time.Second)) {
		t.Error("a renewal did not extend the lease")
	}
	// A saved length applies at the next renewal.
	length = 10 * time.Second
	interest.Renew(start.Add(60 * time.Second))
	if interest.Held(start.Add(70 * time.Second)) {
		t.Error("the lease ignored the saved length")
	}
}

func TestLiveCadenceAndInterestANilLeaseNeverHoldsAndRenewsSafely(t *testing.T) {
	var interest *Interest
	interest.Renew(time.Now())
	if interest.Held(time.Now()) {
		t.Error("a nil lease holds")
	}
}

func TestLiveCadenceAndInterestConcurrentRenewalsAreSafe(t *testing.T) {
	interest := New(nil)
	now := time.Now()
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			interest.Renew(now)
			_ = interest.Held(now)
		}()
	}
	group.Wait()
	if interest.Renewals() != 32 || !interest.Held(now.Add(DefaultLength-time.Second)) {
		t.Errorf("32 concurrent renewals counted %d", interest.Renewals())
	}
}
