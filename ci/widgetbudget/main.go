// Command widgetbudget times every served widget type against a seeded database, for the
// pre-deployment command (A14 and AC18 of specs/SPEC-step-5b-widget-api.md).
//
// sql/schema-checks.sh runs it on copies of its 100 000- and 1 000 000-row seeds, after
// planning every statement: each served type is answered at its defaults for the periods
// 24h and 30d, at the seed's own instant, three times; the median is reported and compared
// with the budget of decision 9. A figure over its budget makes the command exit non-zero,
// which fails the run. It is a program rather than a test so the everyday suite never
// meets it and no test has to skip itself when no seed is there.
//
// A BUDGET TIMED ON AN EMPTY ANSWER MEASURES NOTHING. Every timed response must therefore
// come back with rows -- whatever the availability condition of its source -- or the run
// fails naming it: a seed that stops feeding a type, or a read that silently stops reading,
// cannot pass as fast. The one exception is
// custom_chart at its defaults, whose default is no series at all; it is reported, and
// custom_chart is timed a second time with one series of every source, which is the
// figure that measures its queries.
//
//	go run ./ci/widgetbudget -data-dir <dir holding opnview.db> -now <epoch> -budget-24h 1s -budget-30d 2s
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/widget"
)

func main() {
	dataDir := flag.String("data-dir", "", "the directory holding the seeded opnview.db")
	now := flag.Int64("now", 0, "the seed's instant, in UTC epoch seconds")
	label := flag.String("label", "", "the seed's name, for the report")
	budgetDay := flag.Duration("budget-24h", 0, "the budget of a 24h response")
	budgetMonth := flag.Duration("budget-30d", 0, "the budget of a 30d response")
	flag.Parse()
	if *dataDir == "" || *now == 0 || *budgetDay == 0 || *budgetMonth == 0 {
		fmt.Fprintln(os.Stderr, "widgetbudget: -data-dir, -now, -budget-24h and -budget-30d are required")
		os.Exit(2)
	}
	if err := run(*dataDir, *now, *label, map[string]time.Duration{"24h": *budgetDay, "30d": *budgetMonth}); err != nil {
		fmt.Fprintln(os.Stderr, "widgetbudget:", err)
		os.Exit(1)
	}
}

// timedCase is one request the run times.
type timedCase struct {
	// name is what the report calls it: the type, and what was added to its defaults.
	name       string
	widgetType string
	parameters map[string]any
	// mayBeEmpty is true only for custom_chart at its defaults, which draws no series.
	mayBeEmpty bool
}

// everySourceSeries is one series of every source a custom chart offers, unscoped, so the
// timed chart reads the filter log, the interface counters, the firewall's own gauges, the
// resolver's lookups and the security events.
var everySourceSeries = []any{
	map[string]any{"source": "traffic", "measure": "bytes"},
	map[string]any{"source": "interface_throughput", "measure": "bits_per_second"},
	map[string]any{"source": "firewall_metric", "metric": "memory"},
	map[string]any{"source": "dns", "measure": "lookups"},
	map[string]any{"source": "security_event", "measure": "events"},
}

// run times every type and reports; it fails when any median is over its budget or any
// timed response but custom_chart's default is empty.
func run(dataDir string, now int64, label string, budgets map[string]time.Duration) error {
	ctx := context.Background()
	database, err := store.Open(ctx, dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	service, err := widget.New(database, widget.Options{Now: func() time.Time { return time.Unix(now, 0) }})
	if err != nil {
		return err
	}
	client, err := anyClient(ctx, database)
	if err != nil {
		return err
	}
	var cases []timedCase
	for _, widgetType := range service.Types() {
		parameters := map[string]any{}
		if widgetType == "client_traffic_detail" {
			parameters["client"] = client
		}
		cases = append(cases, timedCase{name: widgetType, widgetType: widgetType, parameters: parameters,
			mayBeEmpty: widgetType == "custom_chart"})
		if widgetType == "custom_chart" {
			cases = append(cases, timedCase{name: "custom_chart+every_source", widgetType: widgetType,
				parameters: map[string]any{"series": everySourceSeries}})
		}
	}
	over, empty := 0, 0
	for _, timed := range cases {
		preset, _ := service.Preset(timed.widgetType)
		for _, period := range []string{"24h", "30d"} {
			parameters := map[string]any{}
			for key, value := range timed.parameters {
				parameters[key] = value
			}
			for _, param := range preset.Params {
				if param.Key == "period" {
					parameters["period"] = period
				}
			}
			encoded, _ := json.Marshal(parameters)
			var (
				times    []time.Duration
				response widget.Response
			)
			for attempt := 0; attempt < 3; attempt++ {
				started := time.Now()
				response, err = service.Respond(ctx, timed.widgetType, string(encoded))
				if err != nil {
					return fmt.Errorf("%s %s: %w", timed.name, period, err)
				}
				times = append(times, time.Since(started))
			}
			sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
			median := times[1]
			verdict := "ok"
			if median > budgets[period] {
				verdict = "OVER BUDGET"
				over++
			}
			if reason := emptiness(response); reason != "" && !timed.mayBeEmpty {
				verdict += "; EMPTY: " + reason
				empty++
			}
			fmt.Printf("  widget-budget %s %-30s %-3s %8.1f ms (budget %v, condition %s) %s\n", label, timed.name,
				period, float64(median.Microseconds())/1000, budgets[period], response.Condition, verdict)
		}
	}
	switch {
	case over > 0 && empty > 0:
		return fmt.Errorf("%d responses on %s are over their budget and %d timed an empty answer", over, label, empty)
	case over > 0:
		return fmt.Errorf("%d responses on %s are over their budget", over, label)
	case empty > 0:
		return fmt.Errorf("%d responses on %s timed an empty answer, which measures no query", empty, label)
	}
	return nil
}

// emptiness says why a response measured no query, or "" when it read rows: it carries no
// data, or its condition is reachable_no_rows, or, for a chart of series, one of its series
// carries no buckets or reached no rows. A degraded condition WITH stored rows is not
// empty: the source's availability is a state, and the rows were read all the same.
func emptiness(response widget.Response) string {
	switch {
	case response.Data == nil:
		return "no data, condition " + response.Condition
	case response.Condition == widget.ConditionReachableNoRows:
		return "condition " + response.Condition
	}
	if chart, isChart := response.Data.(widget.CustomChartData); isChart {
		if len(chart.Series) == 0 {
			return "no series"
		}
		for _, series := range chart.Series {
			if series.Buckets == nil || series.Condition == widget.ConditionReachableNoRows {
				return fmt.Sprintf("series %s condition %s", series.Key, series.Condition)
			}
		}
	}
	return ""
}

// anyClient is a reference to one client of the seed known by a MAC or a DHCP client
// identifier, which client_traffic_detail requires.
func anyClient(ctx context.Context, database *store.Store) (map[string]any, error) {
	var kind, key string
	err := database.DB().QueryRowContext(ctx, `SELECT identity_kind, identity_key FROM client
		WHERE identity_kind IN ('mac', 'dhcp_client_id') ORDER BY id LIMIT 1`).Scan(&kind, &key)
	if err != nil {
		return nil, fmt.Errorf("finding a client of the seed: %w", err)
	}
	return map[string]any{"kind": "client", "by": kind, "value": key}, nil
}
