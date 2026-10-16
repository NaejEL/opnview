package main

import (
	"fmt"
	"os"

	"golang.org/x/tools/imports"
)

func main() {
	imports.LocalPrefix = "github.com/NaejEL/opnview"
	for _, path := range os.Args[1:] {
		raw, err := os.ReadFile(path)
		if err != nil {
			fatalf("%v", err)
		}
		out, err := imports.Process(path, raw, &imports.Options{Comments: true, TabIndent: true, TabWidth: 8})
		if err != nil {
			fmt.Fprintln(os.Stderr, path, err)
			continue
		}
		if string(out) != string(raw) {
			if err := os.WriteFile(path, out, 0o644); err != nil {
				fatalf("%v", err)
			}
			fmt.Println("fixed", path)
		}
	}
}

// fatalf reports a failure and ends the command.
func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "fiximports: "+format+"\n", arguments...)
	os.Exit(1)
}
