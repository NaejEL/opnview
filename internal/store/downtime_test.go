package store

import (
	"context"
	"testing"
)

// The intervals during which opnview was not running (SPEC-step-5b-widget-api.md, D2,
// AD3). Every instant is driven by the test; nothing sleeps.

const (
	downtimeThreshold int64 = 300
	downtimeStart     int64 = 1750000000
)

// downtimeCount counts the recorded intervals.
func downtimeCount(t *testing.T, database *Store) int {
	t.Helper()
	var count int
	if err := database.DB().QueryRow("SELECT count(*) FROM opnview_downtime").Scan(&count); err != nil {
		t.Fatalf("counting the intervals: %v", err)
	}
	return count
}

// TestAStopAndARestartAfterTheThresholdRecordExactlyOneInterval is AD3: after a clean stop
// and after a crash, a restart later than the threshold records one interval, bounded by the
// last heartbeat -- the stop instant, for a clean stop -- and the restart; a restart within
// the threshold records none; and a first start ever records none.
func TestAStopAndARestartAfterTheThresholdRecordExactlyOneInterval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, ending := range []struct {
		name  string
		clean bool
	}{{"a clean shutdown", true}, {"a crash", false}} {
		for _, gap := range []int64{downtimeThreshold + 1, 7 * 86400} {
			database, _ := openTestStore(t)

			// A first start ever: nothing to bound an interval by.
			if absence, err := database.RecordStart(ctx, downtimeStart, downtimeThreshold); err != nil ||
				absence != nil {
				t.Fatalf("%s: a first start recorded %+v (%v)", ending.name, absence, err)
			}
			// The service runs and beats; the last beat is the last instant it is known alive.
			lastBeat := downtimeStart + 3*60
			for beat := downtimeStart + 60; beat <= lastBeat; beat += 60 {
				if absence, err := database.Heartbeat(ctx, beat, downtimeThreshold); err != nil || absence != nil {
					t.Fatalf("%s: a beat a minute after the last recorded %+v (%v)", ending.name, absence, err)
				}
			}
			stoppedAt := lastBeat
			if ending.clean {
				stoppedAt = lastBeat + 20
				if err := database.RecordCleanStop(ctx, stoppedAt); err != nil {
					t.Fatalf("recording the clean stop: %v", err)
				}
			}

			restart := stoppedAt + gap
			absence, err := database.RecordStart(ctx, restart, downtimeThreshold)
			if err != nil {
				t.Fatalf("%s: recording the restart: %v", ending.name, err)
			}
			if absence == nil || absence.From != stoppedAt || absence.To != restart ||
				absence.CleanShutdown != ending.clean {
				t.Errorf("%s after %d s: the restart recorded %+v, not [%d, %d) clean %t", ending.name,
					gap, absence, stoppedAt, restart, ending.clean)
			}
			if count := downtimeCount(t, database); count != 1 {
				t.Errorf("%s after %d s: %d intervals were recorded, not exactly one", ending.name, gap, count)
			}
			read, err := database.ReadNotRunning(ctx, stoppedAt-10, restart+10)
			if err != nil {
				t.Fatal(err)
			}
			if len(read) != 1 || read[0].From != stoppedAt || read[0].To != restart ||
				read[0].CleanShutdown != ending.clean || read[0].DetectedAt != restart {
				t.Errorf("%s: ReadNotRunning returned %+v", ending.name, read)
			}

			// A second restart within the threshold of the first records nothing more.
			if absence, err := database.RecordStart(ctx, restart+downtimeThreshold, downtimeThreshold); err != nil ||
				absence != nil {
				t.Errorf("%s: a restart within the threshold recorded %+v (%v)", ending.name, absence, err)
			}
			if count := downtimeCount(t, database); count != 1 {
				t.Errorf("%s: %d intervals after a restart within the threshold", ending.name, count)
			}
		}
	}
}

// TestARestartWithinTheThresholdRecordsNoInterval is AD3's second sentence, at the boundary:
// a stop and a restart exactly the threshold apart records nothing, for a clean stop and for
// a crash.
func TestARestartWithinTheThresholdRecordsNoInterval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, clean := range []bool{true, false} {
		database, _ := openTestStore(t)
		if _, err := database.RecordStart(ctx, downtimeStart, downtimeThreshold); err != nil {
			t.Fatal(err)
		}
		if clean {
			if err := database.RecordCleanStop(ctx, downtimeStart+30); err != nil {
				t.Fatal(err)
			}
		}
		last := downtimeStart
		if clean {
			last = downtimeStart + 30
		}
		if absence, err := database.RecordStart(ctx, last+downtimeThreshold, downtimeThreshold); err != nil ||
			absence != nil {
			t.Errorf("clean %t: a restart exactly the threshold later recorded %+v (%v)", clean, absence, err)
		}
		if count := downtimeCount(t, database); count != 0 {
			t.Errorf("clean %t: %d intervals recorded", clean, count)
		}
	}
}

// TestAHeartbeatFindingItsPredecessorTooOldRecordsTheInterval covers a process that did not
// run between two of its own beats -- a suspended host -- which every source experienced as
// a stop and a start.
func TestAHeartbeatFindingItsPredecessorTooOldRecordsTheInterval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, _ := openTestStore(t)
	if _, err := database.RecordStart(ctx, downtimeStart, downtimeThreshold); err != nil {
		t.Fatal(err)
	}
	resumed := downtimeStart + 3600
	absence, err := database.Heartbeat(ctx, resumed, downtimeThreshold)
	if err != nil {
		t.Fatal(err)
	}
	if absence == nil || absence.From != downtimeStart || absence.To != resumed || absence.CleanShutdown {
		t.Errorf("the late beat recorded %+v", absence)
	}
	if intervals, err := database.ReadNotRunning(ctx, resumed, resumed+60); err != nil || len(intervals) != 0 {
		t.Errorf("an interval ending at the window's start overlaps it: %+v (%v)", intervals, err)
	}
}
