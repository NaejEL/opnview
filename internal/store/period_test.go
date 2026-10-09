package store

import (
	"testing"
	"time"
)

// AC18 — slot boundaries are calendar boundaries in UTC.

func utc(year int, month time.Month, day, hour, minute int) int64 {
	return time.Date(year, month, day, hour, minute, 0, 0, time.UTC).Unix()
}

// TestAMonthSlotIsTheCalendarMonthOfAnyLength covers months of 28, 29, 30 and 31 days.
func TestAMonthSlotIsTheCalendarMonthOfAnyLength(t *testing.T) {
	t.Parallel()
	for _, month := range []struct {
		name  string
		start int64
		end   int64
		days  int64
	}{
		{"a 28-day February", utc(2027, time.February, 1, 0, 0), utc(2027, time.March, 1, 0, 0), 28},
		{"a 29-day February", utc(2028, time.February, 1, 0, 0), utc(2028, time.March, 1, 0, 0), 29},
		{"a 30-day April", utc(2026, time.April, 1, 0, 0), utc(2026, time.May, 1, 0, 0), 30},
		{"a 31-day January", utc(2026, time.January, 1, 0, 0), utc(2026, time.February, 1, 0, 0), 31},
	} {
		middle := month.start + (month.end-month.start)/2
		start := PeriodMonth.SlotStart(middle)
		if start != month.start {
			t.Errorf("%s: the slot starts at %d, not %d", month.name, start, month.start)
		}
		end := PeriodMonth.SlotEnd(start)
		if end != month.end {
			t.Errorf("%s: the slot ends at %d, not %d", month.name, end, month.end)
		}
		if (end-start)/86400 != month.days {
			t.Errorf("%s: the slot is %d days long", month.name, (end-start)/86400)
		}
		if PeriodMonth.SlotStart(month.end-1) != month.start || PeriodMonth.SlotStart(month.end) != month.end {
			t.Errorf("%s: the last second and the next month's first are on the wrong sides", month.name)
		}
	}
}

// TestAnISOWeekSpanningAYearBoundaryIsOneSlot is the ISO week that starts on Monday 28
// December 2026 and ends on Sunday 3 January 2027.
func TestAnISOWeekSpanningAYearBoundaryIsOneSlot(t *testing.T) {
	t.Parallel()
	monday := utc(2026, time.December, 28, 0, 0)
	if weekday := time.Unix(monday, 0).UTC().Weekday(); weekday != time.Monday {
		t.Fatalf("the reference day is a %s", weekday)
	}
	for _, instant := range []int64{
		monday, utc(2026, time.December, 31, 23, 59), utc(2027, time.January, 1, 0, 0),
		utc(2027, time.January, 3, 23, 59),
	} {
		if start := PeriodWeek.SlotStart(instant); start != monday {
			t.Errorf("%s falls in the week starting %s",
				time.Unix(instant, 0).UTC(), time.Unix(start, 0).UTC())
		}
	}
	if end := PeriodWeek.SlotEnd(monday); end != utc(2027, time.January, 4, 0, 0) {
		t.Errorf("the week ends at %s", time.Unix(end, 0).UTC())
	}
	if start := PeriodWeek.SlotStart(utc(2027, time.January, 4, 0, 0)); start != utc(2027, time.January, 4, 0, 0) {
		t.Error("Monday 4 January 2027 does not start the next week")
	}
	if start := PeriodWeek.SlotStart(utc(2026, time.December, 27, 23, 59)); start != utc(2026, time.December, 21, 0, 0) {
		t.Error("Sunday 27 December 2026 is not in the week before")
	}
}

// TestAnHourCrossingMidnightUTCEndsAtMidnight puts the last hour of a day and the first of
// the next on the right sides of the day boundary.
func TestAnHourCrossingMidnightUTCEndsAtMidnight(t *testing.T) {
	t.Parallel()
	lateHour := utc(2026, time.October, 3, 23, 0)
	midnight := utc(2026, time.October, 4, 0, 0)
	if start := PeriodHour.SlotStart(utc(2026, time.October, 3, 23, 30)); start != lateHour {
		t.Errorf("23:30 is in the hour starting %s", time.Unix(start, 0).UTC())
	}
	if end := PeriodHour.SlotEnd(lateHour); end != midnight {
		t.Errorf("the last hour ends at %s", time.Unix(end, 0).UTC())
	}
	if start := PeriodDay.SlotStart(utc(2026, time.October, 3, 23, 30)); start != utc(2026, time.October, 3, 0, 0) {
		t.Error("23:30 is not in its own day")
	}
	if start := PeriodDay.SlotStart(midnight); start != midnight {
		t.Error("midnight does not start the next day")
	}
	// Every slot of every period is a whole number of hours, which is what lets a rolling
	// window be assembled from hour slots.
	for _, period := range Periods() {
		for _, instant := range []int64{lateHour + 1800, midnight, utc(2028, time.February, 29, 12, 0)} {
			start := period.SlotStart(instant)
			if start%3600 != 0 || period.SlotEnd(start)%3600 != 0 {
				t.Errorf("a %s slot is not a whole number of UTC hours", period.Name)
			}
		}
	}
}

// TestSlotsCoverAWindowInOrder lists the slots overlapping a window.
func TestSlotsCoverAWindowInOrder(t *testing.T) {
	t.Parallel()
	from := utc(2026, time.October, 3, 22, 30)
	to := utc(2026, time.October, 4, 1, 15)
	slots := PeriodHour.Slots(from, to)
	if len(slots) != 4 || slots[0] != utc(2026, time.October, 3, 22, 0) ||
		slots[3] != utc(2026, time.October, 4, 1, 0) {
		t.Errorf("the hour slots of the window are %v", slots)
	}
	if _, err := PeriodNamed("2w"); err == nil {
		t.Error("a period that does not exist was accepted")
	}
}
