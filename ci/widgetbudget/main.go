// Command widgetbudget times every served widget type, and the default query of every core
// dataset of the data catalogue, against a seeded database, for the pre-deployment command (A14
// and AC18 of specs/SPEC-step-5b-widget-api.md; P-AC20 of specs/SPEC-backend-completion-plugins.md).
//
// sql/schema-checks.sh runs it on its 100 000- and 1 000 000-row seeds, after planning every
// statement: each served type is answered at its defaults for the periods 24h and 30d, at the
// seed's own instant, three times; the median is reported beside its budget (decision 9). Each
// core dataset is queried the same way: its default query, which is the 24h period, and the same
// query for {"period": "30d"}, under the same two budgets; a dataset with no time column has no
// period, so it is timed once, under the 24h budget. A figure over its budget makes the command
// exit non-zero, which fails the run. It is a program rather than a test so the everyday suite
// never meets it and no test has to skip itself when no seed is there.
//
// A BUDGET TIMED ON AN EMPTY ANSWER MEASURES NOTHING. Every timed response must therefore
// come back with rows -- whatever the availability condition of its source -- or the run
// fails naming it: a seed that stops feeding a type, or a read that silently stops reading,
// cannot pass as fast. The one exception is
// custom_chart at its defaults, whose default is no series at all; it is reported, and
// custom_chart is timed a second time with one series of every source, which is the
// figure that measures its queries.
//
// The seed is named by a PostgreSQL connection string, the form the test databases take
// (internal/store/testdatabase.go): the instance OPNVIEW_TEST_DATABASE names, with the seed's
// database in dbname.
//
//	go run ./ci/widgetbudget -database '<connection string>' -now <epoch> -budget-24h 1s -budget-30d 2s
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/widget"
	_ "github.com/NaejEL/opnview/internal/widget/widgets"
)

func main() {
	database := flag.String("database", "", "the PostgreSQL connection string of the seeded database")
	now := flag.Int64("now", 0, "the seed's instant, in UTC epoch seconds")
	label := flag.String("label", "", "the seed's name, for the report")
	budgetDay := flag.Duration("budget-24h", 0, "the budget of a 24h response")
	budgetMonth := flag.Duration("budget-30d", 0, "the budget of a 30d response")
	flag.Parse()
	if *database == "" || *now == 0 || *budgetDay == 0 || *budgetMonth == 0 {
		fmt.Fprintln(os.Stderr, "widgetbudget: -database, -now, -budget-24h and -budget-30d are required")
		os.Exit(2)
	}
	if err := run(*database, *now, *label, map[string]time.Duration{"24h": *budgetDay, "30d": *budgetMonth}); err != nil {
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

// tally counts the figures over their budget and the timed answers that read nothing.
type tally struct{ over, empty int }

// run times every type and every core dataset and reports; it fails when any median is over its
// budget or any timed response but custom_chart's default is empty.
func run(dsn string, now int64, label string, budgets map[string]time.Duration) error {
	ctx := context.Background()
	database, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	service, err := widget.New(database, widget.Options{Now: func() time.Time { return time.Unix(now, 0) }})
	if err != nil {
		return err
	}
	var counts tally
	if err := timeWidgets(ctx, database, service, label, budgets, &counts); err != nil {
		return err
	}
	if err := timeDatasets(ctx, service, label, budgets, &counts); err != nil {
		return err
	}
	switch {
	case counts.over > 0 && counts.empty > 0:
		return fmt.Errorf("%d responses on %s are over their budget and %d timed an empty answer",
			counts.over, label, counts.empty)
	case counts.over > 0:
		return fmt.Errorf("%d responses on %s are over their budget", counts.over, label)
	case counts.empty > 0:
		return fmt.Errorf("%d responses on %s timed an empty answer, which measures no query", counts.empty, label)
	}
	return nil
}

// timeWidgets times every served widget type at its defaults, for 24h and 30d.
func timeWidgets(ctx context.Context, database *store.Store, service *widget.Service, label string,
	budgets map[string]time.Duration, counts *tally) error {
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
			var response widget.Response
			median, err := medianOfThree(func() error {
				var respondErr error
				response, respondErr = service.Respond(ctx, timed.widgetType, string(encoded))
				return respondErr
			})
			if err != nil {
				return fmt.Errorf("%s %s: %w", timed.name, period, err)
			}
			verdict := judge(median, budgets[period], counts)
			if reason := emptiness(response); reason != "" && !timed.mayBeEmpty {
				verdict += "; EMPTY: " + reason
				counts.empty++
			}
			fmt.Printf("  widget-budget %s %-30s %-3s %8.1f ms (budget %v, condition %s) %s\n", label, timed.name,
				period, float64(median.Microseconds())/1000, budgets[period], response.Condition, verdict)
		}
	}
	return nil
}

// timeDatasets times the default query of every core dataset of the catalogue, and the same
// query over 30 days when the dataset has a time column (P-AC20).
func timeDatasets(ctx context.Context, service *widget.Service, label string, budgets map[string]time.Duration,
	counts *tally) error {
	for _, declaration := range service.Datasets().Declarations() {
		if declaration.Origin != dataset.OriginCore {
			continue
		}
		periods := []string{"24h"}
		if _, timed := declaration.TimeColumn(); timed {
			periods = append(periods, "30d")
		}
		for _, period := range periods {
			// The default query is the empty parameter set; its period is the default, 24h.
			parameters := ""
			if period != dataset.DefaultPeriod {
				parameters = `{"period": "` + period + `"}`
			}
			var (
				response widget.DatasetResponse
				refusal  *dataset.Refusal
			)
			median, err := medianOfThree(func() error {
				var queryErr error
				response, refusal, queryErr = service.QueryDataset(ctx, declaration.Name, parameters)
				return queryErr
			})
			if err != nil {
				return fmt.Errorf("dataset %s %s: %w", declaration.Name, period, err)
			}
			if refusal != nil {
				return fmt.Errorf("dataset %s %s: the query was refused with %s", declaration.Name, period, refusal.Code)
			}
			verdict := judge(median, budgets[period], counts)
			if reason := datasetEmptiness(response); reason != "" {
				verdict += "; EMPTY: " + reason
				counts.empty++
			}
			fmt.Printf("  dataset-budget %s %-30s %-3s %8.1f ms (budget %v, condition %s) %s\n", label,
				declaration.Name, period, float64(median.Microseconds())/1000, budgets[period], response.Condition,
				verdict)
		}
	}
	return nil
}

// medianOfThree runs one request three times and returns the median wall time.
func medianOfThree(request func() error) (time.Duration, error) {
	var times []time.Duration
	for attempt := 0; attempt < 3; attempt++ {
		started := time.Now()
		if err := request(); err != nil {
			return 0, err
		}
		times = append(times, time.Since(started))
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	return times[1], nil
}

// judge compares a median with its budget and counts it when it is over.
func judge(median, budget time.Duration, counts *tally) string {
	if median > budget {
		counts.over++
		return "OVER BUDGET"
	}
	return "ok"
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
	// A chart of series is read through its JSON, as a client reads it: a widget plugin's script
	// answers it as JSON.
	var chart struct {
		Series []widget.Series `json:"series"`
	}
	if encoded, err := json.Marshal(response.Data); response.Type == "custom_chart" && err == nil &&
		json.Unmarshal(encoded, &chart) == nil {
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

// datasetEmptiness is emptiness for a dataset's answer: no data, or no row reached.
func datasetEmptiness(response widget.DatasetResponse) string {
	switch {
	case response.Data == nil:
		return "no data, condition " + response.Condition
	case response.Condition == widget.ConditionReachableNoRows:
		return "condition " + response.Condition
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
