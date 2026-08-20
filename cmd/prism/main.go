// Command prism is the CLI entrypoint of the Prism agent harness.
package main

import (
	"flag"
	"fmt"
	"os"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

const usage = `prism - a trace-first agent harness

Usage:
  prism <command> [flags]

Commands:
  version    Print the version and exit

Run "prism <command> -h" for command-specific flags.
`

func main() {
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	switch flag.Arg(0) {
	case "version":
		fmt.Println(version)
	case "", "help":
		flag.Usage()
	default:
		fmt.Fprintf(os.Stderr, "prism: unknown command %q\n\n", flag.Arg(0))
		flag.Usage()
		os.Exit(2)
	}
}
