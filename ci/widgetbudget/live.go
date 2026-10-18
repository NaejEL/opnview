package main

import (
	"context"
	"fmt"
	"time"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/samples"
)

// timeLive times the live part of a response (C-AC14 of specs/SPEC-backend-completion-plugins.md:
// a median of at most 100 ms at the 1 000 000-row seed): the read every live figure of a widget
// answer makes -- the current rates of every subject, split by direction, device, client and owner
// -- the median of three, beside its budget, at the seed's newest sample. The rolling strip is the
// live_strip widget, timed with every other served type (timeWidgets). A live part with no sample in
// the seed measures nothing, and is counted as an empty answer.
func timeLive(ctx context.Context, database *store.Store, now int64, label string, budget time.Duration,
	counts *tally) error {
	// The live part is read at the seed's newest sample, so it reads samples: the seed spreads its
	// samples over months, and a live figure reads only the last two intervals.
	var newest *int64
	if err := database.DB().QueryRowContext(ctx, `SELECT max(sampled_at) FROM measurement_sample
		WHERE subject_kind = 'interface_endpoint' AND sampled_at <= $1`, now).Scan(&newest); err != nil {
		return fmt.Errorf("the seed's newest sample: %w", err)
	}
	if newest != nil {
		now = *newest
	}
	var rates samples.CurrentRates
	median, err := medianOfThree(func() error {
		var err error
		rates, err = database.ReadCurrentRates(ctx, now)
		return err
	})
	if err != nil {
		return fmt.Errorf("the current rates: %w", err)
	}
	verdict := judge(median, budget, counts)
	if len(rates.ByDevice) == 0 {
		verdict, counts.empty = verdict+" (no current sample: measures nothing)", counts.empty+1
	}
	fmt.Printf("  live-budget %s %-30s %8.1f ms (budget %v) %s\n", label, "current_rates",
		float64(median.Microseconds())/1000, budget, verdict)
	return nil
}
