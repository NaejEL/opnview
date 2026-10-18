package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/NaejEL/opnview/internal/auth"
	"github.com/NaejEL/opnview/internal/buildinfo"
	"github.com/NaejEL/opnview/internal/collect"
	"github.com/NaejEL/opnview/internal/collect/core"
	"github.com/NaejEL/opnview/internal/config"
	livelease "github.com/NaejEL/opnview/internal/live"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/secret"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"

	// Every surface of the interface is a directory below this package that registers itself;
	// importing it links them all into the binary.
	_ "github.com/NaejEL/opnview/internal/web/surfaces"
)

// shutdownDeadline bounds how long a stop may take after the signal. The loops all
// select on the context, so a clean stop is immediate; the deadline exists so a
// collector stuck in a call cannot hold the database open for ever, and so the
// systemd unit step 8 writes has a figure to set its own timeout above.
const shutdownDeadline = 20 * time.Second

// httpGrace bounds how long the HTTP server may take to finish the requests it is
// already serving after the signal. It is well inside shutdownDeadline, because the
// database cannot be closed until the handlers have stopped touching it.
const httpGrace = 5 * time.Second

func main() {
	if len(os.Args) > 1 && os.Args[1] == "measure-cost" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		err := runMeasureCost(ctx, os.Args[2:], os.Stdout, os.Stderr)
		stop()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", buildinfo.AppName, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "plugin" {
		if err := runPluginCommand(context.Background(), os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", buildinfo.AppName, err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", buildinfo.AppName, err)
		os.Exit(1)
	}
}

// run is main with a NAMED error return, and the name is load-bearing.
//
// The database is closed in a deferred function, which runs after the return value has
// already been copied — so assigning to a local there could never reach the caller, and
// a database that failed to close could never set the exit code. A failed close is
// exactly what leaves a hot journal behind, which is the one failure the restart story
// cannot survive, so it has to be able to fail the process.
func run() (err error) {
	dataDir := flag.String("data-dir", "",
		"directory holding the encryption key and the plugins' data. Required: there is "+
			"deliberately no default, because one would pre-empt the directory layout "+
			"the installer chooses")
	databaseConnection := flag.String("database", "",
		"connection to the installation's own PostgreSQL database, as key=value pairs "+
			"(host, port, user, dbname, passfile, sslmode). Required: there is deliberately "+
			"no default, for the reason --data-dir has none")
	listen := flag.String("listen", "",
		"address the interface listens on, as host:port. Required: there is "+
			"deliberately no default, for the same reason --data-dir has none, and "+
			"because no address of any real network belongs in this repository")
	version := flag.String("version-stamp", "",
		"version to report in the start-up line, for a build that stamps one")
	var listens pushListens
	flag.Var(&listens, "push-listen",
		"a push listener, as <protocol>/<transport>=<host:port> -- syslog/udp, syslog/tcp or netflow/udp -- "+
			"repeatable. Optional and with no default, for the reason --listen has none: with none, no "+
			"push socket is opened and the firewall's exports are not received (README, \"Push sources\")")
	var extraSenders pushExtraSenders
	flag.Var(&extraSenders, "push-extra-senders",
		"IP addresses accepted as push senders beside the firewall's own, comma-separated, repeatable. "+
			"Empty by default: only the configured firewall may send. For a container network that rewrites "+
			"the source of a published port (Docker Desktop: the bridge network's gateway). Whatever reaches "+
			"the port from such an address is then accepted, so the host must filter the port to the "+
			"firewall's address (README, \"Push sources\")")
	flag.Parse()

	if *dataDir == "" {
		return config.ErrNoDataDir
	}
	if *databaseConnection == "" {
		return store.ErrNoDatabase
	}
	if *listen == "" {
		return errors.New("--listen is required and has no default")
	}

	fmt.Printf("%s starting, data directory %s\n", buildinfo.Summary(*version), *dataDir)

	// The signals are wired before anything is opened, so a stop that arrives during
	// start-up is honoured rather than lost. ONE CANCELLATION STOPS BOTH the HTTP
	// server and the collection scheduler.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	database, err := store.OpenWith(ctx, store.Options{DSN: *databaseConnection, Log: func(message string) {
		fmt.Printf("%s: %s\n", buildinfo.AppName, message)
	}, Timed: func(step string, took time.Duration) {
		fmt.Printf("%s: opening the database: %s in %s\n", buildinfo.AppName, step, took.Round(time.Millisecond))
	}})
	if err != nil {
		return err
	}
	// The close is deferred, reported, and allowed to fail the process. It does not
	// overwrite an earlier failure: the first thing that went wrong is the one worth
	// reporting, and a close error on top of it is noise.
	defer func() {
		closeErr := database.Close()
		if closeErr == nil {
			return
		}
		fmt.Fprintf(os.Stderr, "%s: %v\n", buildinfo.AppName, closeErr)
		if err == nil {
			err = closeErr
		}
	}()

	settings, err := config.Load(ctx, database)
	if err != nil {
		return err
	}

	// THE INTERVAL OPNVIEW WAS NOT RUNNING is recorded before anything collects, so the
	// first pass of every source already sits after it (SPEC-step-5b-widget-api.md, D2).
	// The clean stop is recorded once the loops have stopped, by this defer, which runs
	// before the database's own close above it.
	if absence, err := collect.RecordStart(ctx, database, settings, core.SystemClock{}); err != nil {
		return err
	} else if absence != nil {
		fmt.Printf("%s was not running from %s to %s; that interval is recorded as a gap of every "+
			"source\n", buildinfo.AppName, time.Unix(absence.From, 0).UTC().Format(time.RFC3339),
			time.Unix(absence.To, 0).UTC().Format(time.RFC3339))
	}
	defer func() {
		// The start context is cancelled by the stop signal, so the record gets its own.
		stopping, cancel := context.WithTimeout(context.Background(), shutdownDeadline)
		defer cancel()
		if stopErr := collect.RecordCleanStop(stopping, database, core.SystemClock{}); stopErr != nil {
			fmt.Fprintf(os.Stderr, "%s: recording the clean stop: %v\n", buildinfo.AppName, stopErr)
		}
	}()

	// The key file beside the database, created on first start with permissions
	// denying group and other.
	//
	// A FAILURE TO OPEN IT IS A STATE AND NOT A REFUSAL TO START. With the database
	// intact and the key file gone or replaced, the service has to come up: the
	// settings surface is where the credentials are re-entered, and refusing to start
	// would make the only remedy unreachable. What it must not do is report that as
	// "no firewall is configured", and it does not — the state is reported as its own.
	box, err := secret.OpenOrCreate(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"%s: the encryption key beside the database could not be opened, so the stored "+
				"credentials cannot be read until it is restored or they are entered again: %v\n",
			buildinfo.AppName, err)
		box = nil
	}

	// The credential seam. The client reads this holder on EVERY call, so a
	// credential entered or corrected in the settings surface reaches the next
	// collection pass in the same process, with no restart.
	credentials := config.NewFirewallCredentials()
	var unsealer config.Unsealer
	if box != nil {
		unsealer = box
	}
	initial, credentialState, err := config.LoadFirewallCredentials(ctx, database, unsealer)
	if err != nil {
		return err
	}
	credentials.Set(initial)

	client := opnsense.NewClientFromSource(credentials, nil, 0)

	// THE PLUGINS: the built-in ones embedded in the binary, then those dropped in
	// <data-dir>/plugins. A refused plugin is reported with its reason and the service starts
	// without it; a refused built-in is also a failure the probe round records.
	var sealer plugin.Sealer
	if box != nil {
		sealer = box
	}
	plugins, err := plugin.Load(ctx, plugin.Options{DataDir: *dataDir, Store: database, Sealer: sealer})
	if err != nil {
		return err
	}
	for _, refusal := range plugins.Refusals() {
		fmt.Fprintf(os.Stderr, "%s: plugin not loaded: %v\n", buildinfo.AppName, &refusal)
	}
	collector := collect.NewWithSources(client, database, core.SystemClock{}, plugins.Set)
	// The mirror stores what the loaded plugins read, by plugin and version.
	loadedReaders := map[string]string{}
	for _, loaded := range plugins.Plugins() {
		loadedReaders[loaded.Name] = loaded.Version
	}
	if mirrored := collector.Broker().Mirror(); mirrored != nil {
		mirrored.SetLoadedReaders(loadedReaders)
	}
	// The page sizes the collector asks for come from the configuration, like the
	// intervals the scheduler waits.
	collector.Configure(settings)

	// THE LIVE HOLDER. The scheduler reads each interval from it on every turn, and
	// the collection surface sets it, and the collector's page sizes, when a pair is
	// saved: a saved pair takes effect without a restart, loaded the way start-up
	// loads it.
	live := config.NewLive(settings)

	// The datasets the service plugins downloaded on a previous run are in use at once; a copy
	// that does not open is reported and left for the next refresh to replace.
	if err := collector.LoadServices(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", buildinfo.AppName, err)
	}

	setupToken, err := auth.NewSetupToken()
	if err != nil {
		return err
	}
	server, err := web.New(web.Options{
		Store:       database,
		DataDir:     *dataDir,
		Box:         box,
		Credentials: credentials,
		Client:      client,
		SetupToken:  setupToken,
		Settings:    live,
		Collector:   collector,
		GeoIP:       collector,
		// The widget API groups names through the active registrable_domain provider, which
		// answers from the copy its own refresh downloaded.
		SuffixLists: collector,
		Plugins:     plugins,
		Broker:      collector.Broker(),
	})
	if err != nil {
		return err
	}
	// THE LIVE-INTEREST LEASE: renewed by every request for a live figure, read by the live sampling
	// loop, its length the live_interest_seconds the live holder carries (internal/live).
	interest := livelease.New(func() time.Duration {
		return time.Duration(live.Get().LiveInterestSeconds) * time.Second
	})
	server.Widgets().SetLiveInterest(interest)
	collector.SetLiveInterest(interest)
	// A refused operator widget is reported with its reason and the service starts without it.
	for _, refusal := range server.Widgets().Refusals() {
		fmt.Fprintf(os.Stderr, "%s: operator widget not loaded: %v\n", buildinfo.AppName, &refusal)
	}
	if err := server.CollectExpiredSessions(ctx); err != nil {
		return err
	}

	accountExists, err := server.AccountExists(ctx)
	if err != nil {
		return err
	}
	// THE TOKEN IS PRINTED ONLY WHERE IT IS USABLE. An installation that already has
	// an account cannot use one, the setup surface is closed against it, and printing
	// one anyway would suggest otherwise.
	if !accountExists {
		fmt.Printf("%s: no account exists yet. Create the first one at %s using this "+
			"one-time setup token: %s\n",
			buildinfo.AppName, web.PathSetup, setupToken.Token())
	}
	reportCredentialState(credentialState)

	report := func(taskName string, err error) {
		if errors.Is(err, opnsense.ErrNoBaseURL) {
			// The expected state of an installation nobody has configured yet. It is
			// reported once per pass at a lower volume than a real failure, because a
			// loop shouting every ten seconds about something the product has not been
			// configured for yet is noise that hides the failures that matter.
			return
		}
		fmt.Fprintf(os.Stderr, "%s: %s: %v\n", buildinfo.AppName, taskName, err)
	}

	// THE PUSH RECEIVER: the declarations of the loaded plugins are routed whether or not a listener
	// is open, so each reports its listener state; a socket is opened only for a --push-listen given.
	pushReceiver, err := startPush(ctx, listens, extraSenders, database, collector, plugins, credentials,
		func(err error) {
			fmt.Fprintf(os.Stderr, "%s: push: %v\n", buildinfo.AppName, err)
		})
	if err != nil {
		return err
	}
	for _, listen := range listens {
		fmt.Printf("%s: receiving %s over %s on %s\n", buildinfo.AppName, listen.protocol, listen.transport,
			listen.address)
	}
	if len(extraSenders) > 0 {
		fmt.Printf("%s: push senders accepted beside the firewall's own: %s\n", buildinfo.AppName,
			extraSenders.String())
	}

	// The handler served: the server itself, or, in a review build only, the server behind the recorded
	// widget answers (review.go).
	handler, err := reviewHandler(server)
	if err != nil {
		return err
	}

	// The long-running things, stopped by the one cancellation above.
	var running sync.WaitGroup
	var serveErr error
	running.Add(1)
	go func() {
		defer running.Done()
		serveErr = server.ListenAndServeHandler(ctx, *listen, httpGrace, handler)
	}()

	fmt.Printf("%s: the interface is listening on %s\n", buildinfo.AppName, *listen)

	collectErr := core.Run(ctx, core.SystemClock{},
		append(append(collector.Tasks(live), collector.ServiceTasks(live)...),
			collect.HeartbeatTask(live, database, core.SystemClock{})), report)
	running.Wait()
	// Every record the push listeners accepted is flushed before the database closes.
	_ = pushReceiver.Wait()

	switch {
	case collectErr != nil:
		return collectErr
	case serveErr != nil:
		return serveErr
	}

	// Both have stopped. The deferred close runs next.
	fmt.Printf("%s stopped\n", buildinfo.AppName)
	// nil, not the close error: the close has not run yet. The deferred function above
	// is what turns a failed close into a non-zero exit, and it can only do that
	// because this function's error return is named.
	return nil
}

// reportCredentialState says on the console which of the three states the stored
// credentials are in.
//
// THE MIDDLE ONE IS WHY THIS FUNCTION EXISTS. "Nothing is configured" and "something
// is configured and cannot be read" are different facts, and collapsing them would
// send the operator to look for a setting that is already there.
func reportCredentialState(state config.CredentialState) {
	switch state {
	case config.CredentialStateReady:
		fmt.Printf("%s: the stored firewall credentials were read, so collection will use them\n",
			buildinfo.AppName)
	case config.CredentialStateUndecryptable:
		fmt.Fprintf(os.Stderr,
			"%s: a firewall credential is stored and cannot be decrypted. This is not the "+
				"same thing as no firewall being configured: enter the credential again in "+
				"the settings surface, or restore the key file beside the database\n",
			buildinfo.AppName)
	default:
		fmt.Printf("%s: no firewall is configured yet, so every collector will report that "+
			"it cannot reach one\n", buildinfo.AppName)
	}
}
