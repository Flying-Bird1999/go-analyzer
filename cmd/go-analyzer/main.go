package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"go/build"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"gopkg.inshopline.com/bff/go-analyzer/internal/app"
	"gopkg.inshopline.com/bff/go-analyzer/internal/output"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, os.Args[1:]); err != nil {
		var analysisErr *app.AnalysisError
		if errors.As(err, &analysisErr) {
			fmt.Fprintf(os.Stderr, "error_code=%s message=%s\n", analysisErr.Code, analysisErr.Err)
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	return runContext(context.Background(), args)
}

func runContext(ctx context.Context, args []string) (err error) {
	defer func() {
		if err == nil {
			return
		}
		var analysisErr *app.AnalysisError
		if !errors.As(err, &analysisErr) {
			err = app.InvalidArgument(err)
		}
	}()
	if len(args) == 0 {
		return fmt.Errorf("command is required")
	}
	switch args[0] {
	case "help", "-h", "--help":
		return runHelp(args[1:])
	case "facts":
		return runFacts(ctx, args[1:])
	case "impact":
		return runImpact(ctx, args[1:])
	case "grpc-impact":
		return runGrpcImpact(ctx, args[1:])
	case "endpoint-assets":
		return runEndpointAssets(ctx, args[1:])
	case "schema":
		return runSchema(args[1:])
	default:
		return fmt.Errorf("unsupported command %q", args[0])
	}
}

func runGrpcImpact(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("grpc-impact", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	projectPath := fs.String("project", "", "absolute Go service project path")
	diffPath := fs.String("diff", "", "absolute unified diff file path")
	impactConfigPath := fs.String("impact-config", "", "optional absolute impact config path")
	format := fs.String("format", "json", "output format")
	timings := fs.Bool("timings", false, "write pipeline stage timings to stderr")
	diagnosticsOutput := fs.String("diagnostics-output", "", "optional absolute diagnostic sidecar path")
	buildFlags := registerBuildContextFlags(fs)
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
	if err := validateOptionalAbsPath("diagnostics output path", *diagnosticsOutput); err != nil {
		return err
	}
	buildContext, err := buildFlags.options()
	if err != nil {
		return err
	}
	result, err := app.RunGrpcImpactWithMetricsContext(ctx, app.GrpcImpactOptions{
		ProjectPath: *projectPath, DiffPath: *diffPath, ImpactConfigPath: *impactConfigPath,
		Format: *format, BuildContext: buildContext,
	})
	if err != nil {
		return err
	}
	return writeRunResult(result, *timings, *diagnosticsOutput)
}

type stringList []string

func (s *stringList) String() string         { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error { *s = append(*s, value); return nil }

func runEndpointAssets(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("endpoint-assets", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	projectPath := fs.String("project", "", "absolute project path")
	format := fs.String("format", "json", "output format")
	timings := fs.Bool("timings", false, "write pipeline stage timings to stderr")
	diagnosticsOutput := fs.String("diagnostics-output", "", "optional absolute diagnostic sidecar path")
	var endpoints stringList
	fs.Var(&endpoints, "endpoint", "endpoint as METHOD /exact/path; repeatable")
	buildFlags := registerBuildContextFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateAbsPath("project path", *projectPath); err != nil {
		return err
	}
	if err := validateOptionalAbsPath("diagnostics output path", *diagnosticsOutput); err != nil {
		return err
	}
	buildContext, err := buildFlags.options()
	if err != nil {
		return err
	}
	result, err := app.RunEndpointAssetsWithMetricsContext(ctx, app.EndpointAssetsOptions{ProjectPath: *projectPath, Endpoints: endpoints, Format: *format, BuildContext: buildContext})
	if err != nil {
		return err
	}
	return writeRunResult(result, *timings, *diagnosticsOutput)
}

func runHelp(args []string) error {
	text := usage("")
	if len(args) > 0 {
		text = usage(args[0])
	}
	_, err := fmt.Fprint(os.Stdout, text)
	return err
}

func runFacts(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("facts", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	projectPath := fs.String("project", "", "project path")
	format := fs.String("format", "json", "output format")
	timings := fs.Bool("timings", false, "write pipeline stage timings to stderr")
	diagnosticsOutput := fs.String("diagnostics-output", "", "optional absolute diagnostic sidecar path")
	buildFlags := registerBuildContextFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateAbsPath("project path", *projectPath); err != nil {
		return err
	}
	if err := validateOptionalAbsPath("diagnostics output path", *diagnosticsOutput); err != nil {
		return err
	}
	buildContext, err := buildFlags.options()
	if err != nil {
		return err
	}
	result, err := app.RunFactsWithMetricsContext(ctx, app.Options{
		ProjectPath:  *projectPath,
		Format:       *format,
		BuildContext: buildContext,
	})
	if err != nil {
		return err
	}
	return writeRunResult(result, *timings, *diagnosticsOutput)
}

func runImpact(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("impact", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	projectPath := fs.String("project", "", "absolute project path")
	diffPath := fs.String("diff", "", "optional absolute unified diff file path")
	impactConfigPath := fs.String("impact-config", "", "optional absolute impact config path")
	format := fs.String("format", "json", "output format")
	timings := fs.Bool("timings", false, "write pipeline stage timings to stderr")
	diagnosticsOutput := fs.String("diagnostics-output", "", "optional absolute diagnostic sidecar path")
	var grpcMethods stringList
	fs.Var(&grpcMethods, "grpc", "canonical changed gRPC full method; repeatable")
	buildFlags := registerBuildContextFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateAbsPath("project path", *projectPath); err != nil {
		return err
	}
	if *diffPath != "" {
		if err := validateAbsPath("diff path", *diffPath); err != nil {
			return err
		}
	}
	if *diffPath == "" && len(grpcMethods) == 0 {
		return fmt.Errorf("at least one --diff or --grpc is required")
	}
	if *impactConfigPath != "" {
		if err := validateAbsPath("impact config path", *impactConfigPath); err != nil {
			return err
		}
	}
	if err := validateOptionalAbsPath("diagnostics output path", *diagnosticsOutput); err != nil {
		return err
	}
	buildContext, err := buildFlags.options()
	if err != nil {
		return err
	}
	result, err := app.RunImpactWithMetricsContext(ctx, app.ImpactOptions{
		ProjectPath:      *projectPath,
		DiffPath:         *diffPath,
		GrpcMethods:      grpcMethods,
		ImpactConfigPath: *impactConfigPath,
		Format:           *format,
		BuildContext:     buildContext,
	})
	if err != nil {
		return err
	}
	return writeRunResult(result, *timings, *diagnosticsOutput)
}

func writeTimings(w interface{ Write([]byte) (int, error) }, metrics app.PipelineMetrics) {
	for _, stage := range metrics.Stages {
		_, _ = fmt.Fprintf(w, "timing %s=%s\n", stage.Name, stage.Duration)
	}
}

type buildContextFlags struct {
	goos   *string
	goarch *string
	tags   *string
	cgo    *string
}

func registerBuildContextFlags(fs *flag.FlagSet) buildContextFlags {
	return buildContextFlags{
		goos:   fs.String("goos", "", "Go build GOOS override"),
		goarch: fs.String("goarch", "", "Go build GOARCH override"),
		tags:   fs.String("tags", "", "comma or whitespace separated Go build tags"),
		cgo:    fs.String("cgo", strconv.FormatBool(build.Default.CgoEnabled), "enable cgo while matching build constraints"),
	}
}

func (f buildContextFlags) options() (project.BuildContextOptions, error) {
	cgoEnabled, err := strconv.ParseBool(*f.cgo)
	if err != nil {
		return project.BuildContextOptions{}, fmt.Errorf("invalid cgo value %q: %w", *f.cgo, err)
	}
	return project.BuildContextOptions{
		GOOS:       *f.goos,
		GOARCH:     *f.goarch,
		Tags:       parseBuildTags(*f.tags),
		CgoEnabled: &cgoEnabled,
	}, nil
}

func parseBuildTags(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" || seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}

func runSchema(args []string) error {
	fs := flag.NewFlagSet("schema", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	schemaType := fs.String("type", "facts", "schema type: facts, impact, grpc-impact, or endpoint-assets")
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

func validateOptionalAbsPath(name, path string) error {
	if path == "" {
		return nil
	}
	return validateAbsPath(name, path)
}

func writeRunResult(result app.RunResult, timings bool, diagnosticsPath string) error {
	if diagnosticsPath != "" {
		data, err := output.RenderDiagnosticsJSON(result.Diagnostics)
		if err != nil {
			return app.OutputError(err)
		}
		if err := writeAtomic(diagnosticsPath, data); err != nil {
			return app.OutputError(fmt.Errorf("write diagnostics: %w", err))
		}
	}
	if timings {
		writeTimings(os.Stderr, result.Metrics)
	}
	if _, err := os.Stdout.Write(result.Output); err != nil {
		return app.OutputError(err)
	}
	return nil
}

func writeAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer func() {
		_ = file.Close()
		if err != nil {
			_ = os.Remove(tempPath)
		}
	}()
	if err = file.Chmod(0o600); err != nil {
		return err
	}
	if _, err = file.Write(data); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func usage(command string) string {
	switch command {
	case "facts":
		return `用法:
  go-analyzer facts --project /absolute/path/to/project [--format json] [--diagnostics-output /absolute/path/to/diagnostics.json] [--timings]

提取项目 facts JSON，用于调试 symbol、route、annotation、reference、IM event 和 linker 结果。
可选传入 --goos、--goarch、--tags、--cgo 来指定 Go build context。
`
	case "impact":
		return `用法:
  go-analyzer impact --project /absolute/path/to/project [--diff /absolute/path/to/change.diff] [--grpc "/package.Service/Method"] [--impact-config /absolute/path/to/go-impact.config.json] [--diagnostics-output /absolute/path/to/diagnostics.json] [--format json] [--goos linux] [--goarch amd64] [--tags tag1,tag2] [--cgo false] [--timings]

基于已经应用到变更后源码的 unified diff 和/或上游 gRPC operation，分析受影响的 HTTP 接口和出站 IM event。
--diff 与 --grpc 至少提供一个；两者可组合，--grpc 可重复。
--impact-config 仅用于带 --diff 的 module 版本变更过滤；gRPC-only 不读取自动配置，也不接受显式配置。
--diagnostics-output 可选，原子写入本次会话诊断且不改变 stdout。
Go build context flag 会影响源码文件加载和 build constraint 过滤。
`
	case "grpc-impact":
		return `用法:
  go-analyzer grpc-impact --project /absolute/path/to/go-service --diff /absolute/path/to/change.diff [--impact-config /absolute/path/to/go-impact.config.json] [--diagnostics-output /absolute/path/to/diagnostics.json] [--format json] [--timings]

基于已经应用到变更后源码的 unified diff，分析当前服务受影响的 gRPC、HTTP、Dubbo 和 XXL-Job 入站契约。
命令只分析单个 Go 服务项目，不查询 BFF，也不执行跨仓编排；Pulsar/IM 为后续能力。
`
	case "endpoint-assets":
		return `用法:
  go-analyzer endpoint-assets --project /absolute/path/to/project --endpoint "GET /orders/:id" [--endpoint "POST /orders"] [--diagnostics-output /absolute/path/to/diagnostics.json] [--format json] [--goos linux] [--goarch amd64] [--tags tag1,tag2] [--cgo false] [--timings]

查询 BFF endpoint（annotation 格式 "METHOD /exact/path"）依赖的 gRPC operation；--endpoint 可重复。
--project 必须是绝对路径。Go build context flag 会影响源码文件加载和 build constraint 过滤，语义与 facts/impact/grpc-impact 一致。
`
	case "schema":
		return `用法:
  go-analyzer schema --type facts
  go-analyzer schema --type impact
  go-analyzer schema --type endpoint-assets
  go-analyzer schema --type grpc-impact

输出 facts/impact/endpoint-assets/grpc-impact JSON Schema，用于校验稳定输出契约。
`
	default:
		return `用法:
 go-analyzer help impact
 go-analyzer help grpc-impact
 go-analyzer help endpoint-assets
 go-analyzer impact --project /absolute/path/to/project [--diff /absolute/path/to/change.diff] [--grpc "/package.Service/Method"] [--impact-config /absolute/path/to/go-impact.config.json] [--format json]

对外接入命令:
  impact  从 unified diff 和/或上游 gRPC operation 分析受影响 HTTP 接口和 IM event。
  grpc-impact  从 Go 服务项目 unified diff 分析受影响的入站服务契约。
  endpoint-assets  查询 BFF endpoint 的 gRPC 依赖。

CLI 路径参数必须使用绝对路径。
`
	}
}
