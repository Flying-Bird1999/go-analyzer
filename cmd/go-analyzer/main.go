package main

import (
	"flag"
	"fmt"
	"os"

	"gopkg.inshopline.com/bff/go-analyzer/internal/app"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("command is required")
	}
	switch args[0] {
	case "facts":
		return runFacts(args[1:])
	default:
		return fmt.Errorf("unsupported command %q", args[0])
	}
}

func runFacts(args []string) error {
	fs := flag.NewFlagSet("facts", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	projectPath := fs.String("project", "", "project path")
	format := fs.String("format", "json", "output format")
	if err := fs.Parse(args); err != nil {
		return err
	}
	out, err := app.RunFacts(app.Options{
		ProjectPath: *projectPath,
		Format:      *format,
	})
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(out)
	return err
}
