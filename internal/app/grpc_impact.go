package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	"gopkg.inshopline.com/bff/go-analyzer/internal/config"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	dubboextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/dubbo"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/gomod"
	grpcextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/grpc"
	jobextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/job"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/reference"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/route"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/link"
	"gopkg.inshopline.com/bff/go-analyzer/internal/output"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
	"gopkg.inshopline.com/bff/go-analyzer/internal/serviceimpact"
)

// RunGrpcImpact returns the service entry impact JSON without timing metadata.
func RunGrpcImpact(opts GrpcImpactOptions) ([]byte, error) {
	result, err := RunGrpcImpactWithMetrics(opts)
	if err != nil {
		return nil, err
	}
	return result.Output, nil
}

// RunGrpcImpactWithMetrics analyzes one already-applied diff in a Go service
// project and returns affected registered entry contracts.
func RunGrpcImpactWithMetrics(opts GrpcImpactOptions) (RunResult, error) {
	if opts.ProjectPath == "" {
		return RunResult{}, errors.New("project path is required")
	}
	if opts.DiffPath == "" {
		return RunResult{}, errors.New("diff path is required")
	}
	if opts.Format == "" {
		opts.Format = "json"
	}
	if opts.Format != "json" {
		return RunResult{}, fmt.Errorf("unsupported format %q", opts.Format)
	}
	recorder := &pipelineRecorder{}
	cfg, err := config.LoadImpactConfig(opts.ProjectPath, opts.ImpactConfigPath)
	if err != nil {
		return RunResult{}, err
	}
	var patch []byte
	if err := recorder.measure("diff_read", func() error {
		var readErr error
		patch, readErr = os.ReadFile(opts.DiffPath)
		if readErr != nil {
			return fmt.Errorf("read diff: %w", readErr)
		}
		return nil
	}); err != nil {
		return RunResult{}, err
	}
	var fileChanges []diff.FileChange
	if err := recorder.measure("diff_parse", func() error {
		var parseErr error
		fileChanges, parseErr = diff.ParseUnified(patch)
		return parseErr
	}); err != nil {
		return RunResult{}, err
	}
	if err := recorder.measure("diff_validate", func() error { return diff.ValidateApplied(opts.ProjectPath, fileChanges) }); err != nil {
		return RunResult{}, err
	}
	built, err := buildGrpcServiceFacts(opts.ProjectPath, opts.BuildContext, recorder)
	if err != nil {
		return RunResult{}, strictAnalysisError(err)
	}
	if err := validateChangedGoFiles(built.project, fileChanges); err != nil {
		return RunResult{}, err
	}
	store := built.store
	if err := recorder.measure("diff_map", func() error {
		store.Changes = append(store.Changes, diff.MapChanges(fileChanges, store, "git_diff")...)
		return nil
	}); err != nil {
		return RunResult{}, err
	}

	moduleDiffResolved := false
	var moduleChanges []facts.ModuleChangeFact
	if err := recorder.measure("gomod_diff", func() error {
		var moduleErr error
		moduleChanges, moduleErr = gomod.DiffModulesFromFileChanges(fileChanges)
		if moduleErr != nil {
			return fmt.Errorf("diff go.mod modules: %w", moduleErr)
		}
		return nil
	}); err != nil {
		return RunResult{}, err
	}
	moduleDiffResolved = len(moduleChanges) > 0
	moduleChanges = cfg.FilterModuleChanges(moduleChanges)
	store.ModuleChanges = append(store.ModuleChanges, moduleChanges...)
	var moduleUsages []facts.ModuleUsageFact
	if err := recorder.measure("module_usage_map", func() error {
		moduleUsages = gomod.MapModuleUsage(built.project, built.index, store, moduleChanges)
		store.ModuleUsages = append(store.ModuleUsages, moduleUsages...)
		store.Changes = append(store.Changes, moduleUsageChanges(moduleUsages, store, "go_mod_diff")...)
		return nil
	}); err != nil {
		return RunResult{}, err
	}

	var tree serviceimpact.TreeResult
	if err := recorder.measure("grpc_impact_analyze", func() error {
		tree = serviceimpact.AnalyzeTrees(store)
		return nil
	}); err != nil {
		return RunResult{}, err
	}
	doc := output.BuildGrpcImpactDocument(fileChanges, tree, output.GrpcImpactDocumentOptions{
		ModuleChanges: moduleChanges, ModuleUsages: moduleUsages, SuppressGoModFileSource: moduleDiffResolved,
	})
	var rendered []byte
	if err := recorder.measure("grpc_impact_render", func() error {
		var renderErr error
		rendered, renderErr = output.RenderGrpcImpactJSON(doc)
		return renderErr
	}); err != nil {
		return RunResult{}, err
	}
	return RunResult{Output: rendered, Metrics: recorder.metrics()}, nil
}

func buildGrpcServiceFacts(projectPath string, buildContext project.BuildContextOptions, recorder *pipelineRecorder) (builtFacts, error) {
	built, err := buildBaseFacts(projectPath, buildContext, recorder)
	if err != nil {
		return builtFacts{}, err
	}
	if err := recorder.measure("reference_extract", func() error {
		return reference.Extract(built.project, built.index, built.store)
	}); err != nil {
		return builtFacts{}, err
	}
	if err := recorder.measure("route_extract", func() error {
		return route.Extract(built.project, built.index, built.store)
	}); err != nil {
		return builtFacts{}, err
	}
	if err := recorder.measure("route_link", func() error {
		return link.Run(built.index, built.store)
	}); err != nil {
		return builtFacts{}, err
	}
	if err := recorder.measure("job_extract", func() error {
		return jobextract.Extract(built.project, built.index, built.store)
	}); err != nil {
		return builtFacts{}, err
	}
	if err := recorder.measure("dubbo_provider_extract", func() error {
		return dubboextract.Extract(built.project, built.index, built.store)
	}); err != nil {
		return builtFacts{}, err
	}
	dependencies, err := discoverGrpcServerDependencies(built.project, recorder)
	if err != nil {
		return builtFacts{}, err
	}
	if err := recorder.measure("grpc_server_extract", func() error {
		catalog, catalogErr := grpcextract.BuildServerCatalog(built.project, dependencies)
		if catalogErr != nil {
			return catalogErr
		}
		providers, issues := grpcextract.ExtractServerProviders(built.project, built.index, catalog)
		built.store.GrpcOperations = append(built.store.GrpcOperations, catalog.Operations...)
		built.store.GrpcProviders = append(built.store.GrpcProviders, providers...)
		for _, issue := range issues {
			addServerBindingIssueDiagnostic(built.store, issue)
		}
		return nil
	}); err != nil {
		return builtFacts{}, err
	}
	return built, nil
}

func discoverGrpcServerDependencies(p *project.Project, recorder *pipelineRecorder) ([]project.DependencyPackage, error) {
	buildContext := project.BuildContextOptions{GOOS: p.BuildContext.GOOS, GOARCH: p.BuildContext.GOARCH, Tags: append([]string(nil), p.BuildContext.Tags...)}
	cgo := p.BuildContext.CgoEnabled
	buildContext.CgoEnabled = &cgo
	imports := grpcextract.ServerRegistrationImportPaths(p)
	localImports := map[string]bool{}
	for _, path := range grpcextract.ProjectGeneratedServerImportPaths(p) {
		localImports[path] = true
	}
	// 用独立切片收集，不复用 imports 的底层数组（imports[:0] 原地覆写在
	// ServerRegistrationImportPaths 返回缓存/共享切片时会静默损坏上游数据）。
	remoteImports := make([]string, 0, len(imports))
	for _, path := range imports {
		if !localImports[path] {
			remoteImports = append(remoteImports, path)
		}
	}
	var dependencies []project.DependencyPackage
	err := recorder.measure("grpc_server_dependency_list", func() error {
		var dependencyErr error
		dependencies, dependencyErr = project.DiscoverDependencyPackages(context.Background(), p.Root, buildContext, remoteImports)
		return dependencyErr
	})
	return dependencies, err
}

// addServerBindingIssueDiagnostic records why a gRPC server registration's
// concrete implementation could not be bound. Unresolved (no candidate) and
// ambiguous (multiple candidates) get distinct diagnostic codes so operators
// can tell "we found nothing" apart from "we found too many and refused to
// guess" — either way the registration's provider facts were still emitted
// by ExtractServerProviders, just without a resolved implementation.
func addServerBindingIssueDiagnostic(store *facts.Store, issue grpcextract.ServerBindingIssue) {
	code := diagnostics.CodeGrpcServerBindingUnresolved
	template := "cannot resolve concrete implementation for %s (%s)"
	if issue.Kind == grpcextract.ServerBindingAmbiguous {
		code = diagnostics.CodeGrpcServerBindingAmbiguous
		template = "multiple concrete implementation types remain possible for %s (%s); skipped rather than guessing"
	}
	diagnostics.AddFact(store, diagnostics.Diagnostic{
		Code: code, Severity: diagnostics.SeverityWarning,
		Message: fmt.Sprintf(template, issue.RegisterFunction, issue.ServerInterface), Span: issue.Span,
	})
}
