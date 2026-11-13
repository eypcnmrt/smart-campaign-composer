// Command composer is the single backend binary: api, worker and seed are
// subcommands of it. Each one is a stub until its component is implemented.
package main

import (
	"fmt"
	"io"
	"os"
)

const usage = "usage: composer <api|worker|seed>"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches the subcommand and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "api", "worker", "seed":
		fmt.Fprintf(stdout, "composer %s: not implemented yet\n", args[0])
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n%s\n", args[0], usage)
		return 2
	}
}
