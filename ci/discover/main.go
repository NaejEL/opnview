// Command discover writes the generated file that links a family of implementation
// directories into the binary. It is run by `go generate`; see internal/discover for what it
// does and why.
package main

import (
	"fmt"
	"os"

	"github.com/NaejEL/opnview/internal/discover"
)

func main() {
	spec, err := discover.ParseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := discover.Generate(workingDirectory, spec); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
