package decode

import (
	"fmt"
	"strconv"
	"strings"
)

// The two telemetry values /api/diagnostics/system/systemTime writes as TEXT, as a
// live OPNsense 26.7 answered on 3 October 2026 (see the survey's systemTime table):
// the uptime as "17 days, 23:24:10" and the load as "0.07, 0.14, 0.15".

// UptimeSeconds reads an uptime written as "[N day[s], ]HH:MM[:SS]", the BSD
// uptime(1) shape the endpoint uses, into seconds.
func UptimeSeconds(text string) (int64, error) {
	days := int64(0)
	clock := strings.TrimSpace(text)
	if before, after, found := strings.Cut(clock, ","); found {
		fields := strings.Fields(before)
		if len(fields) != 2 || !strings.HasPrefix(fields[1], "day") {
			return 0, fmt.Errorf("decode: %q is not an uptime", text)
		}
		count, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || count < 0 {
			return 0, fmt.Errorf("decode: %q is not an uptime", text)
		}
		days, clock = count, strings.TrimSpace(after)
	}
	parts := strings.Split(clock, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("decode: %q is not an uptime", text)
	}
	seconds := days * 86400
	for index, unit := range []int64{3600, 60, 1}[:len(parts)] {
		value, err := strconv.ParseInt(parts[index], 10, 64)
		if err != nil || value < 0 {
			return 0, fmt.Errorf("decode: %q is not an uptime", text)
		}
		seconds += value * unit
	}
	return seconds, nil
}

// FirstLoadAverage reads the one-minute figure out of "1m, 5m, 15m".
func FirstLoadAverage(text string) (float64, error) {
	first, _, _ := strings.Cut(text, ",")
	value, err := strconv.ParseFloat(strings.TrimSpace(first), 64)
	if err != nil {
		return 0, fmt.Errorf("decode: %q is not a load average", text)
	}
	return value, nil
}
