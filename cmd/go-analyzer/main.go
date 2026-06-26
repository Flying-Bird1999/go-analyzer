package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

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
	case "impact":
		return runImpact(args[1:])
	default:
		return fmt.Errorf("unsupported command %q", args[0])
	}
}

func runFacts(args []string) error {
	fs := flag.NewFlagSet("facts", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	projectPath := fs.String("project", "", "project path")
	configPath := fs.String("config", "", "absolute analyzer config path")
	format := fs.String("format", "json", "output format")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateAbsPath("project path", *projectPath); err != nil {
		return err
	}
	if err := validateOptionalAbsPath("config path", *configPath); err != nil {
		return err
	}
	out, err := app.RunFacts(app.Options{
		ProjectPath: *projectPath,
		ConfigPath:  *configPath,
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
	configPath := fs.String("config", "", "absolute analyzer config path")
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
	if err := validateOptionalAbsPath("config path", *configPath); err != nil {
		return err
	}
	out, err := app.RunImpact(app.ImpactOptions{
		ProjectPath: *projectPath,
		DiffPath:    *diffPath,
		ConfigPath:  *configPath,
		Format:      *format,
	})
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

func validateOptionalAbsPath(name string, path string) error {
	if path == "" {
		return nil
	}
	return validateAbsPath(name, path)
}
