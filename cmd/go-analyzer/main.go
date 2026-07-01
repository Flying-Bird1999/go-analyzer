package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.inshopline.com/bff/go-analyzer/internal/app"
	"gopkg.inshopline.com/bff/go-analyzer/internal/output"
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
	case "help", "-h", "--help":
		return runHelp(args[1:])
	case "facts":
		return runFacts(args[1:])
	case "impact":
		return runImpact(args[1:])
	case "schema":
		return runSchema(args[1:])
	default:
		return fmt.Errorf("unsupported command %q", args[0])
	}
}

func runHelp(args []string) error {
	text := usage("")
	if len(args) > 0 {
		text = usage(args[0])
	}
	_, err := fmt.Fprint(os.Stdout, text)
	return err
}

func runFacts(args []string) error {
	fs := flag.NewFlagSet("facts", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	projectPath := fs.String("project", "", "project path")
	format := fs.String("format", "json", "output format")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateAbsPath("project path", *projectPath); err != nil {
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

func runImpact(args []string) error {
	fs := flag.NewFlagSet("impact", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	projectPath := fs.String("project", "", "absolute project path")
	diffPath := fs.String("diff", "", "absolute unified diff file path")
	impactConfigPath := fs.String("impact-config", "", "optional absolute impact config path")
	format := fs.String("format", "json", "output format")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateAbsPath("project path", *projectPath); err != nil {
		return err
	}
	if err := validateAbsPath("diff path", *diffPath); err != nil {
		return err
	}
	if *impactConfigPath != "" {
		if err := validateAbsPath("impact config path", *impactConfigPath); err != nil {
			return err
		}
	}
	out, err := app.RunImpact(app.ImpactOptions{
		ProjectPath:      *projectPath,
		DiffPath:         *diffPath,
		ImpactConfigPath: *impactConfigPath,
		Format:           *format,
	})
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(out)
	return err
}

func runSchema(args []string) error {
	fs := flag.NewFlagSet("schema", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	schemaType := fs.String("type", "facts", "schema type: facts or impact")
	if err := fs.Parse(args); err != nil {
		return err
	}
	out, err := output.SchemaJSON(*schemaType)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(out)
	return err
}

func validateAbsPath(name string, path string) error {
	if path == "" {
		return fmt.Errorf("%s is required", name)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s must be an absolute path: %s", name, path)
	}
	return nil
}

func usage(command string) string {
	switch command {
	case "facts":
		return `Usage:
  go-analyzer facts --project /absolute/path/to/project [--format json]

Extract project facts as JSON.
`
	case "impact":
		return `Usage:
  go-analyzer impact --project /absolute/path/to/project --diff /absolute/path/to/change.diff [--impact-config /absolute/path/to/go-impact.config.json] [--format json]

Analyze impacted HTTP endpoints and IM events from a unified diff.
`
	case "schema":
		return `Usage:
  go-analyzer schema --type facts
  go-analyzer schema --type impact

Print the JSON schema for a stable output contract.
`
	default:
		return `Usage:
 go-analyzer help [facts|impact|schema]
 go-analyzer facts --project /absolute/path/to/project [--format json]
 go-analyzer impact --project /absolute/path/to/project --diff /absolute/path/to/change.diff [--impact-config /absolute/path/to/go-impact.config.json] [--format json]
 go-analyzer schema --type facts|impact

Commands:
  facts   Extract analyzer facts as JSON.
  impact  Analyze impacted HTTP endpoints and IM events from a unified diff.
  schema  Print JSON schemas for output contracts.

CLI path flags require absolute paths.
`
	}
}
