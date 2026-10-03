package decode

import (
	"testing"
	"time"
)

// TestTheSystemTimeTextValuesAreRead holds the two text shapes the survey recorded
// from a live firewall, and refuses what is not one of them.
func TestTheSystemTimeTextValuesAreRead(t *testing.T) {
	for text, want := range map[string]int64{
		"17 days, 23:24:10": 17*86400 + 23*3600 + 24*60 + 10,
		"1 day, 00:00:05":   86405,
		"03:04:05":          3*3600 + 4*60 + 5,
		"2 days, 10:30":     2*86400 + 10*3600 + 30*60,
	} {
		if got, err := UptimeSeconds(text); err != nil || got != want {
			t.Errorf("UptimeSeconds(%q) = %d, %v; want %d", text, got, err, want)
		}
	}
	for _, text := range []string{"", "soon", "17 days", "x days, 01:02:03", "1 week, 01:02:03", "01:aa:03"} {
		if _, err := UptimeSeconds(text); err == nil {
			t.Errorf("UptimeSeconds(%q) was accepted", text)
		}
	}
	if got, err := FirstLoadAverage("0.07, 0.14, 0.15"); err != nil || got != 0.07 {
		t.Errorf("FirstLoadAverage = %v, %v; want 0.07", got, err)
	}
	if _, err := FirstLoadAverage("high"); err == nil {
		t.Error("a load average that is not a number was accepted")
	}
}

// TestTheFirewallOffsetIsMeasuredFromItsWallClock is survey gap 7, on the shape a live
// firewall answered with: a CEST wall clock read at a UTC instant is two hours ahead,
// latency and drift are absorbed by the rounding, and a clock no zone explains is
// refused.
func TestTheFirewallOffsetIsMeasuredFromItsWallClock(t *testing.T) {
	readAt := time.Date(2026, 10, 3, 19, 25, 34, 0, time.UTC)
	for datetime, want := range map[string]int{
		"Sat Oct 3 21:25:37 CEST 2026":  7200,
		"Sat Oct  3 21:25:37 CEST 2026": 7200,
		"Sat Oct 3 19:25:31 UTC 2026":   0,
		"Sat Oct 3 14:25:40 CDT 2026":   -18000,
		"Sat Oct 3 23:55:30 IST 2026":   16200,
		"Sun Oct 4 00:25:34 XYZ 2026":   18000,
	} {
		if got, err := FirewallOffsetSeconds(datetime, readAt); err != nil || got != want {
			t.Errorf("FirewallOffsetSeconds(%q) = %d, %v; want %d", datetime, got, err, want)
		}
	}
	for _, datetime := range []string{"", "2026-10-03T21:25:37", "Mon Oct 19 21:25:37 CEST 2026"} {
		if _, err := FirewallOffsetSeconds(datetime, readAt); err == nil {
			t.Errorf("FirewallOffsetSeconds(%q) was accepted", datetime)
		}
	}
}
