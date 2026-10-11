package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/NaejEL/opnview/internal/plugin"
)

// The plugin subcommand: `opnview plugin test <directory>` runs a plugin's own tests -- the
// tests/*.test.js files in its directory -- with the interpreter the service runs it with, against
// a fake firewall and a fake web, so a third party tests a plugin with nothing but this binary
// (docs/plugins.md, "Testing a plugin"). It touches no data directory and contacts nothing.

// errPluginTestsFailed is a test run in which a test failed.
var errPluginTestsFailed = errors.New("a plugin test failed")

// runPluginCommand runs the plugin subcommand with the arguments after "plugin".
func runPluginCommand(ctx context.Context, arguments []string, out io.Writer) error {
	if len(arguments) != 2 || arguments[0] != "test" {
		return errors.New("usage: opnview plugin test <plugin directory>")
	}
	directory := arguments[1]
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return fmt.Errorf("%s is not a directory", directory)
	}
	results, err := plugin.RunTests(ctx, directory)
	if err != nil {
		return err
	}
	failed := 0
	for _, result := range results {
		status := "ok  "
		if !result.Passed {
			status = "FAIL"
			failed++
		}
		fmt.Fprintf(out, "%s %s: %s\n", status, result.File, result.Name)
		for _, failure := range result.Failures {
			fmt.Fprintf(out, "     %s\n", failure)
		}
	}
	fmt.Fprintf(out, "%d tests, %d failed\n", len(results), failed)
	if len(results) == 0 {
		return errors.New("the plugin has no test: tests/*.test.js holds none")
	}
	if failed > 0 {
		return errPluginTestsFailed
	}
	return nil
}
