//go:build njreplay

package collect_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// explainUnmatched compares the records the join left unmatched with the analysis's Rule B as its own
// script ran it (scratchpad/capture-analysis/phase2.go, ruleB): the latest leg of the case's device and
// pf direction within a day, with no continuity, SYN, second-leg or reverse-check rule. Each record the
// analysis would have joined is put under the rule of NJ4 that refused it.
func explainUnmatched(t *testing.T, h *netflowWorld) string {
	t.Helper()
	ctx := context.Background()
	type leg struct {
		device, dir, pair string
		src, dst          string
		sport, dport      int64
		at                int64
	}
	legRows, err := h.Store.DB().QueryContext(ctx, `SELECT interface_device, direction, coalesce(pair_outcome, ''),
		canonical_address(src_address), canonical_address(dst_address), coalesce(src_port, 0), coalesce(dst_port, 0),
		observed_at FROM flow`)
	if err != nil {
		t.Fatal(err)
	}
	byTuple := map[string][]leg{}
	for legRows.Next() {
		var l leg
		if err := legRows.Scan(&l.device, &l.dir, &l.pair, &l.src, &l.dst, &l.sport, &l.dport, &l.at); err != nil {
			t.Fatal(err)
		}
		byTuple[fmt.Sprintf("%s|%d|%s|%d", l.src, l.sport, l.dst, l.dport)] =
			append(byTuple[fmt.Sprintf("%s|%d|%s|%d", l.src, l.sport, l.dst, l.dport)], l)
	}
	_ = legRows.Close()
	records, err := h.Store.DB().QueryContext(ctx, `SELECT coalesce(unmatched_reason, ''), src_address,
		dst_address, coalesce(src_port, 0), coalesce(dst_port, 0), protocol, coalesce(tcp_flags, 0), start_at,
		ingress_role, coalesce(ingress_device, ''), egress_role, coalesce(egress_device, ''), octets
		FROM plugin_netflow.flow_record WHERE join_state = 'unmatched' AND unmatched_reason IN ('no_logged_leg', 'reverse_unconfirmed')`)
	if err != nil {
		t.Fatal(err)
	}
	type tally struct{ records, octets int64 }
	why := map[string]*tally{}
	for records.Next() {
		var reason, src, dst, inRole, inDevice, outRole, outDevice string
		var sport, dport, protocol, flags, start, octets int64
		if err := records.Scan(&reason, &src, &dst, &sport, &dport, &protocol, &flags, &start, &inRole, &inDevice,
			&outRole, &outDevice, &octets); err != nil {
			t.Fatal(err)
		}
		find := func(a string, ap int64, b string, bp int64, device, dir string) []leg {
			var best []leg
			for _, l := range byTuple[fmt.Sprintf("%s|%d|%s|%d", a, ap, b, bp)] {
				if l.device != device || l.dir != dir || l.at > start+3 || l.at < start-86400 {
					continue
				}
				switch {
				case len(best) == 0 || l.at > best[0].at:
					best = []leg{l}
				case l.at == best[0].at:
					best = append(best, l)
				}
			}
			return best
		}
		var candidates []leg
		if inRole == "no_receiving_interface" {
			candidates = append(candidates, find(src, sport, dst, dport, outDevice, "out")...)
			candidates = append(candidates, find(dst, dport, src, sport, outDevice, "in")...)
		} else {
			candidates = append(candidates, find(src, sport, dst, dport, inDevice, "in")...)
			candidates = append(candidates, find(dst, dport, src, sport, outDevice, "in")...)
			if outRole == "local_delivery" {
				candidates = append(candidates, find(dst, dport, src, sport, inDevice, "out")...)
			}
		}
		latest, count := int64(-1), 0
		var chosen leg
		for _, c := range candidates {
			switch {
			case c.at > latest:
				latest, count, chosen = c.at, 1, c
			case c.at == latest:
				count++
			}
		}
		name := reason + ": no leg under the analysis's Rule B either"
		switch {
		case reason == "reverse_unconfirmed":
			name = "reverse_unconfirmed: the analysis joins it; NJ4 rule 4 refuses (no confirming forward record)"
		case count > 1:
			name = "no_logged_leg: the analysis finds two legs at once"
		case count == 1 && chosen.pair == "second_leg":
			name = "no_logged_leg: the analysis joins a second leg, never a candidate (NJ4 rule 3)"
		case count == 1 && protocol == 6 && flags&2 != 0 && chosen.at < start-3:
			name = "no_logged_leg: the analysis joins it; the SYN rule refuses (PS19 rule 4)"
		case count == 1:
			name = "no_logged_leg: the analysis joins it; the continuity rule refuses (pf's state timeouts, S12)"
		}
		if why[name] == nil {
			why[name] = &tally{}
		}
		why[name].records++
		why[name].octets += octets
	}
	_ = records.Close()
	var lines []string
	for name, value := range why {
		lines = append(lines, fmt.Sprintf("| %s | %d | %d |", name, value.records, value.octets))
	}
	sort.Strings(lines)
	return "\n| unmatched, against the analysis's Rule B | records | octets |\n|---|---|---|\n" +
		strings.Join(lines, "\n") + "\n"
}
