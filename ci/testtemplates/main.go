// Command testtemplates drops the stale test templates of the test database instances, each by its
// exact name (decision 6 of the performance cycle of specs/SPEC-backend-completion-plugins.md, 18
// October 2026). Every test template carries a comment naming the base template it was built from
// (internal/store/testdatabase.go); one whose base is not this build's was left by an earlier schema,
// template revision or set of declarations, and is never cloned again. ci/checks.sh runs it on the
// everyday instance before go test, and ci/pre-deployment.sh on the instances its two go test runs
// use, so a tmpfs instance does not fill with templates no run reads.
//
//	go run ./ci/testtemplates '<connection string>' ...
//
// It prints every template it dropped, and exits non-zero only when an instance cannot be read.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/NaejEL/opnview/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "testtemplates: give the connection string of at least one test instance")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	failed := false
	for _, admin := range os.Args[1:] {
		dropped, err := store.RemoveStaleTestTemplates(ctx, admin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "testtemplates:", err)
			failed = true
			continue
		}
		for _, name := range dropped {
			fmt.Printf("  dropped the stale test template %s\n", name)
		}
	}
	if failed {
		os.Exit(1)
	}
}
