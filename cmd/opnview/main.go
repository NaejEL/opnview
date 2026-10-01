// Command opnview is the single entry point: flags, wiring, signals, exit codes.
//
// WHAT THIS BINARY DOES NOW. It opens the database, applies the schema, creates the
// key file beside it if there is none, starts the collection loops and serves
// opnview's own interface. The firewall URL, the API key and secret, the MaxMind
// licence key and the theme are entered THROUGH THAT INTERFACE and through nothing
// else: there is no environment variable, no configuration file and no `configure`
// subcommand, which the maintainer ruled out precisely so that the settings surface
// has one place.
//
// So a first run prints a one-time setup token, and until somebody uses it to create
// the first account there is no way to configure a firewall and every collector
// reports that it has no firewall to talk to. That is the intended behaviour and not
// a defect.
//
// WHAT IT STILL CANNOT DO, STATED SO NOBODY LOOKS FOR IT. There is no canvas, no
// widget and no dashboard: the interface is three surfaces — setup, sign-in and
// settings — and the widget endpoints are step 5, the canvases step 7. The MaxMind
// licence key is stored and NOT verified, because verifying it means downloading and
// the download is cycle 4C.
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
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/secret"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
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
		"directory holding the database and the encryption key. Required: there is "+
			"deliberately no default, because one would pre-empt the directory layout "+
			"the installer chooses")
	listen := flag.String("listen", "",
		"address the interface listens on, as host:port. Required: there is "+
			"deliberately no default, for the same reason --data-dir has none, and "+
			"because no address of any real network belongs in this repository")
	version := flag.String("version-stamp", "",
		"version to report in the start-up line, for a build that stamps one")
	flag.Parse()

	if *dataDir == "" {
		return config.ErrNoDataDir
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

	database, err := store.Open(ctx, *dataDir)
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
	collector := collect.New(client, database, collect.SystemClock{})

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
	})
	if err != nil {
		return err
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

	purge := func(ctx context.Context) error {
		return database.Purge(ctx, time.Now().UTC().Unix())
	}
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

	// The two long-running things, stopped by the one cancellation above.
	var running sync.WaitGroup
	var serveErr error
	running.Add(1)
	go func() {
		defer running.Done()
		serveErr = server.ListenAndServe(ctx, *listen, httpGrace)
	}()

	fmt.Printf("%s: the interface is listening on %s\n", buildinfo.AppName, *listen)

	collectErr := collect.Run(ctx, collect.SystemClock{},
		collector.Tasks(settings, purge), report)
	running.Wait()

	switch {
	case collectErr != nil:
		return collectErr
	case serveErr != nil:
		return serveErr
	}

	// Both have stopped. The deferred close runs next, and the deadline below is
	// what stops a wedged checkpoint from hanging the process.
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), shutdownDeadline)
	defer cancelShutdown()
	if result, err := database.IntegrityCheck(shutdown); err != nil {
		fmt.Fprintf(os.Stderr, "%s: the integrity check did not run: %v\n", buildinfo.AppName, err)
	} else if result != "ok" {
		return fmt.Errorf("the database reported %q rather than ok on shutdown", result)
	}

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
