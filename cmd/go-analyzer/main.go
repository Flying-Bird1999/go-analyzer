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
		return `用法:
  go-analyzer facts --project /absolute/path/to/project [--format json]

提取项目 facts JSON，用于调试 symbol、route、annotation、reference、IM event 和 linker 结果。
`
	case "impact":
		return `用法:
  go-analyzer impact --project /absolute/path/to/project --diff /absolute/path/to/change.diff [--impact-config /absolute/path/to/go-impact.config.json] [--format json]

基于已经应用到变更后源码的 unified diff，分析受影响的 HTTP 接口和出站 IM event。
--impact-config 为可选配置，仅用于 module 版本变更过滤；未传时自动尝试读取项目内 .analyzer/go-impact.config.json。
`
	case "schema":
		return `用法:
  go-analyzer schema --type facts
  go-analyzer schema --type impact

输出 facts/impact JSON Schema，用于校验稳定输出契约。
`
	default:
		return `用法:
 go-analyzer help impact
 go-analyzer impact --project /absolute/path/to/project --diff /absolute/path/to/change.diff [--impact-config /absolute/path/to/go-impact.config.json] [--format json]

对外接入命令:
  impact  从已应用到变更后源码的 unified diff 分析受影响 HTTP 接口和 IM event。

CLI 路径参数必须使用绝对路径。
`
	}
}
