// Command prism is the CLI entrypoint of the Prism agent harness.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Aurelia-Zhang/Prism/internal/eval"
	"github.com/Aurelia-Zhang/Prism/internal/observability"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

const usage = `prism - a trace-first agent harness

Usage:
  prism <command> [flags]

Commands:
  version    Print the version and exit
  trace      Show or export a persisted trace
  eval       Run a versioned fixture suite

Run "prism <command> -h" for command-specific flags.
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	switch firstArg(args) {
	case "version":
		fmt.Println(version)
	case "", "help":
		fmt.Fprint(os.Stderr, usage)
	case "trace":
		return runTrace(args[1:])
	case "eval":
		return runEval(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "prism: unknown command %q\n\n%s", firstArg(args), usage)
		return 2
	}
	return 0
}

func runTrace(args []string) int {
	if len(args) != 3 || (args[0] != "show" && args[0] != "export") {
		fmt.Fprintln(os.Stderr, "usage: prism trace <show|export> <db-path> <trace-id>")
		return 2
	}
	store, err := observability.Open(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "prism trace: %v\n", err)
		return 1
	}
	defer store.Close()
	snapshot, err := store.Load(context.Background(), args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "prism trace: %v\n", err)
		return 1
	}
	timeline := observability.Replay(snapshot)
	if args[0] == "show" {
		fmt.Print(observability.RenderTimeline(timeline))
		return 0
	}
	encoded, err := observability.MarshalTimeline(timeline)
	if err != nil {
		fmt.Fprintf(os.Stderr, "prism trace: encode timeline: %v\n", err)
		return 1
	}
	fmt.Println(string(encoded))
	return 0
}

func runEval(args []string) int {
	if len(args) < 3 || args[0] != "run" {
		fmt.Fprintln(os.Stderr, "usage: prism eval run <suite-path> --output <report.json>")
		return 2
	}
	suitePath := args[1]
	output := ""
	for i := 2; i+1 < len(args); i++ {
		if args[i] == "--output" || args[i] == "-output" {
			output = args[i+1]
			break
		}
	}
	if output == "" {
		fmt.Fprintln(os.Stderr, "usage: prism eval run <suite-path> --output <report.json>")
		return 2
	}
	suite, err := eval.LoadSuite(suitePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "prism eval: %v\n", err)
		return 1
	}
	report, err := eval.RunSuite(context.Background(), suite)
	if err != nil {
		fmt.Fprintf(os.Stderr, "prism eval: %v\n", err)
		return 1
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "prism eval: encode report: %v\n", err)
		return 1
	}
	if err := writeReport(output, append(encoded, '\n'), eval.Markdown(report)); err != nil {
		fmt.Fprintf(os.Stderr, "prism eval: write report: %v\n", err)
		return 1
	}
	return 0
}

func writeReport(output string, encoded []byte, markdown string) error {
	directory := filepath.Dir(output)
	if directory != "." {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(output, encoded, 0o644); err != nil {
		return err
	}
	markdownPath := strings.TrimSuffix(output, filepath.Ext(output)) + ".md"
	return os.WriteFile(markdownPath, []byte(markdown), 0o644)
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
