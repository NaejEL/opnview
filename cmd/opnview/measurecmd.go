package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/broker"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/measurecost"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/secret"
	"github.com/NaejEL/opnview/internal/store"
)

// The measure-cost subcommand: the LV-C measuring tool (internal/measurecost;
// docs/opnsense-api-survey.md, "LV-C: measuring the firewall-side cost").
//
//	opnview measure-cost --data-dir <dir> --database <connection> --endpoint <path>
//	    [--method GET] [--idle 15m] [--duration 15m] [--cadences 2s,5s,10s,30s,60s]
//	    [--query key=value]... [--form key=value]...
//
// It reads the firewall's address and credentials from the installation's database and key file
// without stopping the service -- no instance lock, no schema, read-only transactions -- and asks
// the firewall only through the broker, under its read-only rule. It prints the report as JSON on
// standard output: proportions and timings, never a field the firewall answered.

// repeated is a flag given several times, key=value each.
type repeated []string

func (r *repeated) String() string        { return strings.Join(*r, ",") }
func (r *repeated) Set(text string) error { *r = append(*r, text); return nil }

// values turns the key=value flags into url.Values.
func (r repeated) values() (url.Values, error) {
	if len(r) == 0 {
		return nil, nil
	}
	result := url.Values{}
	for _, pair := range r {
		key, value, found := strings.Cut(pair, "=")
		if !found || key == "" {
			return nil, fmt.Errorf("%q is not key=value", pair)
		}
		result.Add(key, value)
	}
	return result, nil
}

// runMeasureCost runs the subcommand with the arguments after "measure-cost".
func runMeasureCost(ctx context.Context, arguments []string, out, progress io.Writer) error {
	flags := flag.NewFlagSet("measure-cost", flag.ContinueOnError)
	flags.SetOutput(progress)
	dataDir := flags.String("data-dir", "", "the installation's data directory, holding the key file")
	database := flags.String("database", "", "the installation's database connection, as the service takes it")
	path := flags.String("endpoint", "", "the firewall endpoint to poll, by its path, e.g. /api/diagnostics/traffic/top")
	method := flags.String("method", "", "the endpoint's method when its path has two entries")
	idle := flags.Duration("idle", 15*time.Minute, "how long the idle baseline is sampled")
	duration := flags.Duration("duration", 15*time.Minute, "how long each candidate cadence is polled")
	cadenceList := flags.String("cadences", "2s,5s,10s,30s,60s", "the candidate cadences, comma-separated")
	var query, form repeated
	flags.Var(&query, "query", "a query parameter of each poll, key=value; repeatable")
	flags.Var(&form, "form", "a form field of each poll's body, key=value; repeatable")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *dataDir == "" || *database == "" || *path == "" {
		return errors.New("usage: opnview measure-cost --data-dir <dir> --database <connection> --endpoint <path> " +
			"[--method GET] [--idle 15m] [--duration 15m] [--cadences 2s,5s,10s,30s,60s] [--query k=v]... [--form k=v]...")
	}
	cadences, err := parseCadences(*cadenceList)
	if err != nil {
		return err
	}
	options := opnsense.RequestOptions{}
	if options.Query, err = query.values(); err != nil {
		return err
	}
	if options.Form, err = form.values(); err != nil {
		return err
	}

	// The built-in plugins declare the endpoints they read; loading them admits those to the client.
	plugin.BuiltinsLoaded()
	endpoint, err := findEndpoint(*path, *method)
	if err != nil {
		return err
	}

	reader, err := store.OpenSettingsReader(ctx, *database)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	box, err := secret.Open(*dataDir)
	if err != nil {
		return fmt.Errorf("the key file of %s cannot be opened, so the stored API secret cannot be read: %w", *dataDir, err)
	}
	credentials, state, err := config.LoadFirewallCredentials(ctx, reader, box)
	if err != nil {
		return err
	}
	if state != config.CredentialStateReady {
		return fmt.Errorf("the installation's firewall credentials are not usable (%s)", state)
	}
	holder := config.NewFirewallCredentials()
	holder.Set(credentials)
	gate := broker.New(opnsense.NewClientFromSource(holder, nil, 0), nil, broker.Options{})
	defer gate.Close()

	if endpoint.Argument == opnsense.InterfaceNameArgument && len(options.Arguments) == 0 {
		names, err := interfaceNames(ctx, gate)
		if err != nil {
			return err
		}
		options.Arguments = []string{strings.Join(names, ",")}
	}
	report, err := measurecost.Measure(ctx, measurecost.Options{Broker: gate, Endpoint: endpoint, Request: options,
		Idle: *idle, Duration: *duration, Cadences: cadences, Progress: progress})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

// parseCadences reads the comma-separated candidate cadences.
func parseCadences(list string) ([]time.Duration, error) {
	var cadences []time.Duration
	for _, text := range strings.Split(list, ",") {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		cadence, err := time.ParseDuration(text)
		if err != nil || cadence <= 0 {
			return nil, fmt.Errorf("the cadence %q is not a positive duration", text)
		}
		cadences = append(cadences, cadence)
	}
	if len(cadences) == 0 {
		return nil, measurecost.ErrNoCadence
	}
	return cadences, nil
}

// findEndpoint finds an endpoint the client admits, by its path and, where two share it, its
// method. A stream is not polled.
func findEndpoint(path, method string) (opnsense.Endpoint, error) {
	var found []opnsense.Endpoint
	for _, endpoint := range opnsense.Endpoints() {
		if endpoint.Path == path && (method == "" || strings.EqualFold(endpoint.Method, method)) && !endpoint.Stream {
			found = append(found, endpoint)
		}
	}
	switch len(found) {
	case 0:
		return opnsense.Endpoint{}, fmt.Errorf("%s is no endpoint the compiled registry or a built-in plugin declares, "+
			"or it is a stream", path)
	case 1:
		return found[0], nil
	}
	return opnsense.Endpoint{}, fmt.Errorf("%s is declared with several methods; name one with --method", path)
}

// interfaceNames reads the firewall's interface identifiers -- the configuration keys, never the
// devices, which traffic/top answers with an empty array -- from interfaces_info, as runtime
// discovery reads them: what an endpoint taking interface names is polled with, every interface at
// once, as the insight plugin polls traffic/top.
func interfaceNames(ctx context.Context, gate *broker.Broker) ([]string, error) {
	served, err := gate.Call(ctx, broker.Request{Requester: broker.Requester{Name: measurecost.Requester},
		Endpoint: opnsense.InterfacesInfo, Fresh: true, Whole: true})
	if err != nil {
		return nil, err
	}
	if served == nil || !served.Response.OK() {
		return nil, errors.New("the firewall did not answer its interfaces")
	}
	rows, err := decode.Rows(served.Response.Body)
	if err != nil {
		return nil, fmt.Errorf("reading the interfaces: %w", err)
	}
	var identifiers []string
	for _, row := range rows {
		if identifier, found := decode.String(row, "identifier"); found {
			identifiers = append(identifiers, identifier)
		}
	}
	sort.Strings(identifiers)
	if len(identifiers) == 0 {
		return nil, errors.New("the firewall reported no interface")
	}
	return identifiers, nil
}
