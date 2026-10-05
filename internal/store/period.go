package store

import (
	"fmt"
	"time"
)

// Period is one of the four aggregate periods, and the calendar it slots time
// into.
//
// THE SLOTS ARE CALENDAR SLOTS, ALIGNED TO UTC, and that is the maintainer's
// decision: 1h is the hour, 24h the day, 7d the ISO week starting Monday 00:00
// UTC, and 30d the calendar MONTH. So a 30d slot is 28, 29, 30 or 31 days long,
// and its table name is a period a widget asks for rather than a duration; the
// end of every slot is stored beside its start, so nothing multiplies by thirty.
// Every slot of every period is a whole number of UTC hours, which is what lets
// a rolling window be assembled from 1h slots.
type Period struct {
	// Name is the table suffix and the period a widget names: 1h, 24h, 7d, 30d.
	Name string
	unit periodUnit
}

type periodUnit int

const (
	unitHour periodUnit = iota
	unitDay
	unitISOWeek
	unitMonth
)

// The four periods.
var (
	// PeriodHour is the 1h period: the UTC hour.
	PeriodHour = Period{Name: "1h", unit: unitHour}
	// PeriodDay is the 24h period: the UTC day.
	PeriodDay = Period{Name: "24h", unit: unitDay}
	// PeriodWeek is the 7d period: the ISO week, from Monday 00:00 UTC.
	PeriodWeek = Period{Name: "7d", unit: unitISOWeek}
	// PeriodMonth is the 30d period: the calendar month, in UTC.
	PeriodMonth = Period{Name: "30d", unit: unitMonth}
)

// Periods returns the four periods, finest first.
func Periods() []Period {
	return []Period{PeriodHour, PeriodDay, PeriodWeek, PeriodMonth}
}

// PeriodNamed returns the period with that name.
func PeriodNamed(name string) (Period, error) {
	for _, period := range Periods() {
		if period.Name == name {
			return period, nil
		}
	}
	return Period{}, fmt.Errorf("store: %q is not one of the periods 1h, 24h, 7d and 30d", name)
}

// SlotStart returns the start of the slot holding an instant, as a UTC epoch.
func (p Period) SlotStart(epoch int64) int64 {
	instant := time.Unix(epoch, 0).UTC()
	switch p.unit {
	case unitDay:
		return time.Date(instant.Year(), instant.Month(), instant.Day(), 0, 0, 0, 0, time.UTC).Unix()
	case unitISOWeek:
		day := time.Date(instant.Year(), instant.Month(), instant.Day(), 0, 0, 0, 0, time.UTC)
		// time.Weekday counts Sunday as 0; the ISO week starts on Monday.
		sinceMonday := (int(day.Weekday()) + 6) % 7
		return day.AddDate(0, 0, -sinceMonday).Unix()
	case unitMonth:
		return time.Date(instant.Year(), instant.Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
	default:
		return time.Date(instant.Year(), instant.Month(), instant.Day(), instant.Hour(), 0, 0, 0,
			time.UTC).Unix()
	}
}

// SlotEnd returns the end of the slot that starts at start, exclusive.
func (p Period) SlotEnd(start int64) int64 {
	instant := time.Unix(p.SlotStart(start), 0).UTC()
	switch p.unit {
	case unitDay:
		return instant.AddDate(0, 0, 1).Unix()
	case unitISOWeek:
		return instant.AddDate(0, 0, 7).Unix()
	case unitMonth:
		return instant.AddDate(0, 1, 0).Unix()
	default:
		return instant.Add(time.Hour).Unix()
	}
}

// Slots returns the starts of every slot overlapping [from, to), in order.
func (p Period) Slots(from, to int64) []int64 {
	var starts []int64
	for start := p.SlotStart(from); start < to; start = p.SlotEnd(start) {
		starts = append(starts, start)
	}
	return starts
}

// Purgeable reports whether the retention purge removes the slot starting at start
// when the horizon is horizon: the rule of internal/store/purge.sql, restated. A
// week or a month goes once it has ended before the horizon; an hour or a day only
// once its ISO week and its calendar month have ended before it as well, because a
// week and a month are composed from them.
func (p Period) Purgeable(start, horizon int64) bool {
	if p.SlotEnd(start) >= horizon {
		return false
	}
	if p.unit == unitHour || p.unit == unitDay {
		return PeriodWeek.SlotEnd(PeriodWeek.SlotStart(start)) < horizon &&
			PeriodMonth.SlotEnd(PeriodMonth.SlotStart(start)) < horizon
	}
	return true
}

// Child returns the finer period whose slots tile this period's slots exactly, and
// whether there is one: the hour for a day, the day for a week and for a month. The
// hour has none. Every slot of a period with a child is composed from its children
// (see RefreshAggregatesAt).
func (p Period) Child() (Period, bool) {
	switch p.unit {
	case unitDay:
		return PeriodHour, true
	case unitISOWeek, unitMonth:
		return PeriodDay, true
	default:
		return Period{}, false
	}
}
