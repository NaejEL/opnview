// Command opnview is the single entry point: flags, wiring, signals, exit codes.
//
// WHAT IT CANNOT DO YET, STATED SO NOBODY LOOKS FOR IT. There is no HTTP server and
// no interface, and there is no way to give it a firewall URL, an API key or an API
// secret: those are entered in opnview's own interface, which is cycle 4B. So a run
// started today opens the database, applies the schema, starts the loops, and every
// loop reports that it has no firewall to talk to. That is the intended behaviour of
// this cycle and not a defect — the alternative would be an environment variable or
// a configuration file, which the maintainer ruled out precisely so that the
// settings surface has one place.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/NaejEL/opnview/internal/buildinfo"
	"github.com/NaejEL/opnview/internal/collect"
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
)

// shutdownDeadline bounds how long a stop may take after the signal. The loops all
// select on the context, so a clean stop is immediate; the deadline exists so a
// collector stuck in a call cannot hold the database open for ever, and so the
// systemd unit step 8 writes has a figure to set its own timeout above.
const shutdownDeadline = 20 * time.Second

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
		"directory holding the database. Required: there is deliberately no default, "+
			"because one would pre-empt the directory layout the installer chooses")
	version := flag.String("version-stamp", "",
		"version to report in the start-up line, for a build that stamps one")
	flag.Parse()

	if *dataDir == "" {
		return config.ErrNoDataDir
	}

	fmt.Printf("%s starting, data directory %s\n", buildinfo.Summary(*version), *dataDir)

	// The signals are wired before anything is opened, so a stop that arrives during
	// start-up is honoured rather than lost.
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

	// No credentials, by design: they arrive with the settings surface in 4B. The
	// client is built anyway, so the loops run, report that they cannot reach a
	// firewall, and record it — which is the same code path a wrong key will take.
	client := opnsense.NewClient(opnsense.Credentials{}, nil, 0)
	collector := collect.New(client, database, collect.SystemClock{})

	purge := func(ctx context.Context) error {
		return database.Purge(ctx, time.Now().UTC().Unix())
	}
	report := func(taskName string, err error) {
		if errors.Is(err, opnsense.ErrNoBaseURL) {
			// The expected state of this cycle. It is reported once per pass at a
			// lower volume than a real failure, because a loop shouting every ten
			// seconds about something the product has not been configured for yet is
			// noise that hides the failures that matter.
			return
		}
		fmt.Fprintf(os.Stderr, "%s: %s: %v\n", buildinfo.AppName, taskName, err)
	}

	fmt.Printf("%s: no firewall is configured yet, so every collector will report that it cannot reach one\n",
		buildinfo.AppName)

	if err := collect.Run(ctx, collect.SystemClock{},
		collector.Tasks(settings, purge), report); err != nil {
		return err
	}

	// The loops have stopped. The deferred close runs next, and the deadline below is
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
