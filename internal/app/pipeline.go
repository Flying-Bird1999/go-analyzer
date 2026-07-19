// pipeline.go 实现 go-analyzer 的主流水线编排，是 facts 与 impact 两条命令的统一入口。
//
// Package app 是 go-analyzer 的流水线主编排模块。它对外暴露 RunFacts / RunImpact（以及
// 对应的 WithMetrics 变体）和内部的 buildFacts 共享事实构建流程，是整个项目中唯一
// 了解完整执行顺序的模块：从 project 加载、AST 索引、各 extractor 抽取、linker 关联，
// 到 diff 解析、语义映射、删除路由/go.mod 等补偿，再到 impact 树构建与稳定 JSON 输出。
// extractor 之间不互相调用，特殊 diff 逻辑也不下沉到 CLI；本包按既定顺序串起各模块。
// metrics API 记录各阶段耗时用于性能观测，默认的 facts/impact JSON 不携带耗时以保持
// 输出确定性。
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/config"
	"gopkg.inshopline.com/bff/go-analyzer/internal/dependency"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/annotation"
	dubboextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/dubbo"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/gomod"
	grpcextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/grpc"
	imextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/im"
	jobextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/job"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/reference"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/route"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/impact"
	"gopkg.inshopline.com/bff/go-analyzer/internal/link"
	"gopkg.inshopline.com/bff/go-analyzer/internal/output"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// RunFacts 是 facts 命令的入口，返回完整项目事实快照 JSON。
// 它等价于 RunFactsWithMetrics 的轻量封装：丢弃 metrics 后只返回渲染好的字节序列。
func RunFacts(opts Options) ([]byte, error) {
	result, err := RunFactsWithMetrics(opts)
	if err != nil {
		return nil, err
	}
	return result.Output, nil
}

// RunFactsWithMetrics 是 facts 命令的入口，返回渲染后的 JSON 字节和各阶段耗时指标。
// 流程：校验选项 -> 构建事实库 -> 渲染 JSON。
func RunFactsWithMetrics(opts Options) (RunResult, error) {
	// project path 为必填，缺失直接失败。
	if opts.ProjectPath == "" {
		return RunResult{}, errors.New("project path is required")
	}
	// format 缺省为 json；当前只支持 json，其它取值直接失败。
	if opts.Format == "" {
		opts.Format = "json"
	}
	if opts.Format != "json" {
		return RunResult{}, fmt.Errorf("unsupported format %q", opts.Format)
	}
	// pipelineRecorder 负责按阶段名累计耗时，最终填入 RunResult.Metrics。
	recorder := &pipelineRecorder{}
	// buildFactStore 复用 buildFacts 的完整事实构建链路，返回填充好的 facts.Store。
	store, err := buildFactStore(opts.ProjectPath, opts.BuildContext, recorder)
	if err != nil {
		return RunResult{}, err
	}
	var out []byte
	// facts_render：将 Store 渲染为稳定排序的 facts JSON。
	if err := recorder.measure("facts_render", func() error {
		var renderErr error
		out, renderErr = output.RenderJSON(store)
		return renderErr
	}); err != nil {
		return RunResult{}, err
	}
	return RunResult{Output: out, Metrics: recorder.metrics()}, nil
}

// RunImpact 是 impact 命令的入口，返回从 diff root 到 endpoint / IM event 的影响链路 JSON。
// 它等价于 RunImpactWithMetrics 的轻量封装：丢弃 metrics 后只返回渲染好的字节序列。
func RunImpact(opts ImpactOptions) ([]byte, error) {
	result, err := RunImpactWithMetrics(opts)
	if err != nil {
		return nil, err
	}
	return result.Output, nil
}

// RunImpactWithMetrics 是 impact 命令的入口，返回渲染后的 JSON 字节和各阶段耗时指标。
//
// diff 和 gRPC operation 都可以作为影响源并组合；两者共享一次 facts 构建。
func RunImpactWithMetrics(opts ImpactOptions) (RunResult, error) {
	if opts.ProjectPath == "" {
		return RunResult{}, errors.New("project path is required")
	}
	grpcInputs, err := parseImpactGrpcMethods(opts.GrpcMethods)
	if err != nil {
		return RunResult{}, err
	}
	hasDiff := opts.DiffPath != ""
	if !hasDiff && len(grpcInputs) == 0 {
		return RunResult{}, errors.New("at least one --diff or --grpc is required")
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
	fileChanges := []diff.FileChange{}
	if hasDiff {
		var diffBytes []byte
		if err := recorder.measure("diff_read", func() error {
			var readErr error
			diffBytes, readErr = os.ReadFile(opts.DiffPath)
			if readErr != nil {
				return fmt.Errorf("read diff: %w", readErr)
			}
			return nil
		}); err != nil {
			return RunResult{}, err
		}
		if err := recorder.measure("diff_parse", func() error {
			var parseErr error
			fileChanges, parseErr = diff.ParseUnified(diffBytes)
			return parseErr
		}); err != nil {
			return RunResult{}, err
		}
		if err := recorder.measure("diff_validate", func() error {
			return diff.ValidateApplied(opts.ProjectPath, fileChanges)
		}); err != nil {
			return RunResult{}, err
		}
	}
	grpcExtractionMode := grpcModeOff
	if len(grpcInputs) > 0 {
		grpcExtractionMode = grpcModeStrict
	}
	built, err := buildFacts(opts.ProjectPath, opts.BuildContext, recorder, buildFactsOptions{grpcMode: grpcExtractionMode})
	if err != nil {
		if grpcExtractionMode == grpcModeStrict {
			return RunResult{}, strictAnalysisError(err)
		}
		return RunResult{}, err
	}
	store := built.store
	var moduleChanges []facts.ModuleChangeFact
	var moduleUsages []facts.ModuleUsageFact
	var result impact.TreeResult
	moduleDiffResolved := false
	if hasDiff {
		if err := validateChangedGoFiles(built.project, fileChanges); err != nil {
			return RunResult{}, err
		}
		if err := recorder.measure("diff_map", func() error {
			store.Changes = append(store.Changes, diff.MapChanges(fileChanges, store, "git_diff")...)
			return nil
		}); err != nil {
			return RunResult{}, err
		}
		if err := recorder.measure("deleted_route_recover", func() error {
			impact.RecoverDeletedRoutes(fileChanges, built.index, store, "git_diff")
			return nil
		}); err != nil {
			return RunResult{}, err
		}
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
		if hasGoModDiff(fileChanges) && !moduleDiffResolved {
			diagnostics.AddFact(store, diagnostics.Diagnostic{Code: diagnostics.CodeModuleDiffUnresolved, Severity: diagnostics.SeverityWarning, Message: "go.mod changed, but no require or replace module change could be resolved", Span: facts.SourceSpan{File: "go.mod"}})
		}
		store.ModuleChanges = append(store.ModuleChanges, moduleChanges...)
		if err := recorder.measure("module_usage_map", func() error {
			moduleUsages = gomod.MapModuleUsage(built.project, built.index, store, moduleChanges)
			return nil
		}); err != nil {
			return RunResult{}, err
		}
		store.ModuleUsages = append(store.ModuleUsages, moduleUsages...)
		store.Changes = append(store.Changes, moduleUsageChanges(moduleUsages, store, "go_mod_diff")...)
		if err := recorder.measure("impact_analyze", func() error {
			result = impact.AnalyzeTrees(store)
			return nil
		}); err != nil {
			return RunResult{}, err
		}
	}
	var doc output.ImpactDocument
	// impact_document_build：把内部 tree 投影为稳定 JSON 文档，按 source file 聚合、
	// 去重 endpoint 与已解析 IM event，并在 module diff 已解析时压制 go.mod fileSources。
	if err := recorder.measure("impact_document_build", func() error {
		doc = output.BuildImpactDocument(fileChanges, result, output.ImpactDocumentOptions{
			ModuleChanges:           store.ModuleChanges,
			ModuleUsages:            store.ModuleUsages,
			SuppressGoModFileSource: moduleDiffResolved,
		})
		return nil
	}); err != nil {
		return RunResult{}, err
	}
	if len(grpcInputs) > 0 {
		if err := recorder.measure("grpc_impact_source_query", func() error {
			consumers, queryErr := dependency.FindGrpcImpactSources(store, grpcInputs)
			if queryErr != nil {
				return strictAnalysisError(queryErr)
			}
			output.AddGrpcSources(&doc, store, consumers)
			return nil
		}); err != nil {
			return RunResult{}, err
		}
	}
	var out []byte
	// impact_render：渲染最终 impact JSON，保证稳定排序以降低 golden/consumer 抖动。
	if err := recorder.measure("impact_render", func() error {
		var renderErr error
		out, renderErr = output.RenderImpactTreeJSON(doc)
		return renderErr
	}); err != nil {
		return RunResult{}, err
	}
	return RunResult{Output: out, Metrics: recorder.metrics()}, nil
}

func parseImpactGrpcMethods(rawMethods []string) ([]dependency.GrpcMethod, error) {
	inputs := make([]dependency.GrpcMethod, 0, len(rawMethods))
	for _, raw := range rawMethods {
		input, err := dependency.ParseGrpcMethod(raw)
		if err != nil {
			return nil, &AnalysisError{Code: "invalid_grpc_method", Err: err}
		}
		inputs = append(inputs, input)
	}
	return inputs, nil
}

type builtFacts struct {
	project *project.Project
	index   *astindex.Index
	store   *facts.Store
}

// buildFactStore 是 facts 命令的事实构建入口。除 BFF 域事实外，它还额外抽取
// job/dubbo/gRPC-server 三类服务入口事实（includeServiceEntry: true），使 facts
// 命令能同时排障 BFF 与后端服务两类项目——facts 是 handoff.md 中明确的"排障入口"，
// 若不产出这些事实，grpc-impact 最关键的服务端证据在 facts 视图里永远不可见。
// impact 命令通过 buildFacts 直接调用（见 RunImpactWithMetrics），不设置该选项，
// 行为与之前完全一致：这是纯粹的 facts 命令能力扩展，不影响 impact 的事实构建范围
// 或性能特征。
func buildFactStore(projectPath string, buildContext project.BuildContextOptions, recorder *pipelineRecorder) (*facts.Store, error) {
	built, err := buildFacts(projectPath, buildContext, recorder, buildFactsOptions{grpcMode: grpcModeDiagnostic, includeServiceEntry: true})
	if err != nil {
		return nil, err
	}
	return built.store, nil
}

type grpcMode uint8

const (
	grpcModeOff grpcMode = iota
	grpcModeDiagnostic
	grpcModeStrict
)

// buildFactsOptions.includeServiceEntry 控制是否额外抽取 job/dubbo/gRPC-server 三类
// 服务入口事实。仅 facts 命令（buildFactStore）设置为 true；impact 命令通过 buildFacts
// 直接调用且不设置该字段，行为保持不变。
type buildFactsOptions struct {
	grpcMode            grpcMode
	includeServiceEntry bool
}

func buildFacts(projectPath string, buildContext project.BuildContextOptions, recorder *pipelineRecorder, options buildFactsOptions) (builtFacts, error) {
	built, err := buildBaseFacts(projectPath, buildContext, recorder)
	if err != nil {
		return builtFacts{}, err
	}
	p, idx, store := built.project, built.index, built.store
	if err := recorder.measure("annotation_extract", func() error {
		return annotation.Extract(p, idx, store)
	}); err != nil {
		return builtFacts{}, err
	}
	if err := recorder.measure("route_extract", func() error {
		return route.Extract(p, idx, store)
	}); err != nil {
		return builtFacts{}, err
	}
	if err := recorder.measure("link", func() error {
		return link.Run(idx, store)
	}); err != nil {
		return builtFacts{}, err
	}
	if err := recorder.measure("reference_extract", func() error {
		return reference.Extract(p, idx, store)
	}); err != nil {
		return builtFacts{}, err
	}
	if err := recorder.measure("im_extract", func() error {
		return imextract.Extract(p, idx, store)
	}); err != nil {
		return builtFacts{}, err
	}
	if options.grpcMode != grpcModeOff {
		dependencies, dependencyErr := discoverProjectDependencies(p, recorder)
		if dependencyErr != nil {
			if options.grpcMode == grpcModeStrict {
				return builtFacts{}, dependencyErr
			}
			// 诊断模式：记录失败并继续（不 early return），使下方 includeServiceEntry
			// 抽取（facts 命令专属）在纯服务端项目（没有 gRPC client 依赖、
			// discoverProjectDependencies 因此失败）上仍然执行。
			diagnostics.AddFact(store, diagnostics.Diagnostic{ID: "diagnostic:grpc_dependency_load", Code: diagnostics.CodeGrpcDependencyLoadFailed, Severity: diagnostics.SeverityWarning, Message: dependencyErr.Error()})
		} else if err := recorder.measure("grpc_extract", func() error {
			catalog, catalogErr := grpcextract.BuildCatalog(dependencies)
			if catalogErr != nil {
				return catalogErr
			}
			calls, callErr := grpcextract.Extract(p, idx, catalog)
			if callErr != nil {
				return callErr
			}
			store.GrpcOperations = append(store.GrpcOperations, catalog.Operations...)
			store.GrpcCalls = append(store.GrpcCalls, calls...)
			return nil
		}); err != nil {
			if options.grpcMode == grpcModeStrict {
				return builtFacts{}, err
			}
			code := diagnostics.CodeGrpcCatalogFailed
			var ambiguity *grpcextract.CallAmbiguityError
			if errors.As(err, &ambiguity) {
				code = diagnostics.CodeGrpcCallAmbiguous
			}
			diagnostics.AddFact(store, diagnostics.Diagnostic{ID: "diagnostic:" + string(code), Code: code, Severity: diagnostics.SeverityWarning, Message: err.Error()})
		}
	}
	if options.includeServiceEntry {
		extractServiceEntryFacts(p, idx, store, recorder)
	}
	return builtFacts{project: p, index: idx, store: store}, nil
}

// dedupeNewGrpcOperations 从 additions 中过滤掉已存在于 existing 中的 ID，
// 避免 client catalog 与 server catalog 各自产出同一 canonical operation 时
// 在 store.GrpcOperations 里重复。
func dedupeNewGrpcOperations(existing, additions []facts.GrpcOperationFact) []facts.GrpcOperationFact {
	seen := make(map[string]bool, len(existing))
	for _, operation := range existing {
		seen[operation.ID] = true
	}
	out := make([]facts.GrpcOperationFact, 0, len(additions))
	for _, operation := range additions {
		if seen[operation.ID] {
			continue
		}
		seen[operation.ID] = true
		out = append(out, operation)
	}
	return out
}

// extractServiceEntryFacts 为 facts 命令额外抽取 job/dubbo/gRPC-server 三类服务入口
// 事实。facts 是排障入口而非严格分析命令，因此始终按诊断模式运行：任一子步骤失败
// 都记为诊断并继续，不中断 facts 输出，保持与 grpcMode=diagnostic 时既有 gRPC-client
// 抽取失败处理方式一致的容错策略。
func extractServiceEntryFacts(p *project.Project, idx *astindex.Index, store *facts.Store, recorder *pipelineRecorder) {
	_ = recorder.measure("job_extract", func() error {
		return jobextract.Extract(p, idx, store)
	})
	_ = recorder.measure("dubbo_provider_extract", func() error {
		return dubboextract.Extract(p, idx, store)
	})
	_ = recorder.measure("grpc_server_extract", func() error {
		dependencies, dependencyErr := discoverGrpcServerDependencies(p, recorder)
		if dependencyErr != nil {
			diagnostics.AddFact(store, diagnostics.Diagnostic{Code: diagnostics.CodeGrpcDependencyLoadFailed, Severity: diagnostics.SeverityWarning, Message: dependencyErr.Error()})
			return nil
		}
		catalog, catalogErr := grpcextract.BuildServerCatalog(p, dependencies)
		if catalogErr != nil {
			diagnostics.AddFact(store, diagnostics.Diagnostic{Code: diagnostics.CodeGrpcServerCatalogFailed, Severity: diagnostics.SeverityWarning, Message: catalogErr.Error()})
			return nil
		}
		providers, issues, extractErr := grpcextract.ExtractServerProviders(p, idx, catalog)
		if extractErr != nil {
			diagnostics.AddFact(store, diagnostics.Diagnostic{Code: diagnostics.CodeGrpcServerCatalogFailed, Severity: diagnostics.SeverityWarning, Message: extractErr.Error()})
			return nil
		}
		// facts 命令在 includeServiceEntry 模式下同时运行 gRPC client catalog（诊断模式
		// gRPC 抽取，见上方 grpcMode != grpcModeOff 分支）与 server catalog：若同一个
		// generated package 既被本项目作为 client 调用、又被注册为 server（服务网格中
		// 自调用/双向 RPC 的常见形态），两条 catalog 会各自产出同一 canonical full method
		// 的 GrpcOperationFact，ID 相同。RenderJSON 只排序不去重，直接 append 会让 facts
		// JSON 出现重复的 grpc_operations 条目。按 ID 去重后再合并。
		store.GrpcOperations = append(store.GrpcOperations, dedupeNewGrpcOperations(store.GrpcOperations, catalog.Operations)...)
		store.GrpcProviders = append(store.GrpcProviders, providers...)
		for _, issue := range issues {
			diagnostics.AddFact(store, diagnostics.Diagnostic{
				Code: diagnostics.CodeGrpcServerBindingUnresolved, Severity: diagnostics.SeverityWarning,
				Message: fmt.Sprintf("cannot resolve concrete implementation for %s (%s)", issue.RegisterFunction, issue.ServerInterface), Span: issue.Span,
			})
		}
		return nil
	})
}

// buildBaseFacts is shared by BFF impact and gRPC service impact. It owns only
// project loading, declaration indexing, module facts and symbol projection;
// domain extractors are added by their command-specific pipelines.
func buildBaseFacts(projectPath string, buildContext project.BuildContextOptions, recorder *pipelineRecorder) (builtFacts, error) {
	var p *project.Project
	if err := recorder.measure("project_load", func() error {
		var loadErr error
		p, loadErr = project.LoadWithOptions(projectPath, project.LoadOptions{BuildContext: buildContext})
		return loadErr
	}); err != nil {
		return builtFacts{}, err
	}
	var idx *astindex.Index
	if err := recorder.measure("ast_index", func() error {
		var indexErr error
		idx, indexErr = astindex.Build(p)
		return indexErr
	}); err != nil {
		return builtFacts{}, err
	}
	store := facts.NewStore(p.Root, p.ModulePath, facts.BuildContextFact{
		GOOS:       p.BuildContext.GOOS,
		GOARCH:     p.BuildContext.GOARCH,
		Tags:       append([]string(nil), p.BuildContext.Tags...),
		CgoEnabled: p.BuildContext.CgoEnabled,
	})
	var modBytes []byte
	if err := recorder.measure("gomod_read", func() error {
		var readErr error
		modBytes, readErr = os.ReadFile(filepath.Join(p.Root, "go.mod"))
		if readErr != nil {
			return fmt.Errorf("read go.mod dependencies: %w", readErr)
		}
		return nil
	}); err != nil {
		return builtFacts{}, err
	}
	var modules []facts.ModuleDependencyFact
	if err := recorder.measure("gomod_extract", func() error {
		var extractErr error
		modules, extractErr = gomod.ExtractDependencies(modBytes)
		if extractErr != nil {
			return fmt.Errorf("extract go.mod dependencies: %w", extractErr)
		}
		return nil
	}); err != nil {
		return builtFacts{}, err
	}
	store.Modules = append(store.Modules, modules...)
	for _, loadDiagnostic := range p.Diagnostics {
		diagnostics.AddFact(store, diagnostics.Diagnostic{
			ID:       fmt.Sprintf("diagnostic:%s:%s", loadDiagnostic.Code, loadDiagnostic.File),
			Code:     diagnostics.Code(loadDiagnostic.Code),
			Severity: diagnostics.SeverityWarning,
			Message:  loadDiagnostic.Message,
			Span:     facts.SourceSpan{File: loadDiagnostic.File},
		})
	}
	symbolIDs := make([]facts.SymbolID, 0, len(idx.Symbols))
	for id := range idx.Symbols {
		symbolIDs = append(symbolIDs, id)
	}
	sort.Slice(symbolIDs, func(i, j int) bool { return symbolIDs[i] < symbolIDs[j] })
	for _, id := range symbolIDs {
		store.AddSymbol(idx.Symbols[id])
	}
	return builtFacts{project: p, index: idx, store: store}, nil
}

func discoverProjectDependencies(p *project.Project, recorder *pipelineRecorder) ([]project.DependencyPackage, error) {
	grpcBuildContext := project.BuildContextOptions{GOOS: p.BuildContext.GOOS, GOARCH: p.BuildContext.GOARCH, Tags: append([]string(nil), p.BuildContext.Tags...)}
	cgo := p.BuildContext.CgoEnabled
	grpcBuildContext.CgoEnabled = &cgo
	var dependencies []project.DependencyPackage
	err := recorder.measure("dependency_list", func() error {
		var dependencyErr error
		dependencies, dependencyErr = project.DiscoverDependencies(context.Background(), p.Root, grpcBuildContext)
		return dependencyErr
	})
	return dependencies, err
}

// moduleUsageChanges 把模块 usage 事实转换为可传播的 ChangeFact 列表。
// unreferenced 的 usage 不产生根（本仓未引用该模块）；其余 usage 按是否定位到具体符号
// 分别生成 symbol_changed 或 file_changed 根，并在能解析到符号时回填其文件与行范围。
func moduleUsageChanges(usages []facts.ModuleUsageFact, store *facts.Store, source string) []facts.ChangeFact {
	var out []facts.ChangeFact
	symbols := map[facts.SymbolID]facts.SymbolFact{}
	for _, symbol := range store.Symbols {
		symbols[symbol.ID] = symbol
	}
	for _, usage := range usages {
		if usage.Basis == facts.ModuleUsageUnreferenced {
			continue
		}
		change := facts.ChangeFact{
			ID:           fmt.Sprintf("change:module_usage:%s:%d", usage.ID, len(out)),
			File:         usage.File,
			Source:       source,
			SourceFactID: usage.ID,
			Confidence:   usage.Confidence,
		}
		if usage.SymbolID != "" {
			change.Kind = facts.ChangeKindSymbolChanged
			change.TargetID = string(usage.SymbolID)
			change.SymbolID = usage.SymbolID
			if symbol, ok := symbols[usage.SymbolID]; ok {
				change.File = symbol.Span.File
				change.Ranges = []facts.ChangeRange{{StartLine: symbol.Span.StartLine, EndLine: symbol.Span.EndLine}}
			}
		} else {
			change.Kind = facts.ChangeKindFileChanged
			change.TargetID = usage.File
		}
		out = append(out, change)
	}
	return out
}

// hasGoModDiff 判断 diff 是否包含 go.mod 的实际内容变更（普通行范围或删除块）。
// 用于决定是否在无法解析出模块变化时输出 module_diff_unresolved 诊断。
func hasGoModDiff(changes []diff.FileChange) bool {
	for _, change := range changes {
		file := change.NewPath
		if file == "" {
			file = change.OldPath
		}
		if filepath.ToSlash(file) == "go.mod" && (len(change.Ranges) > 0 || len(change.DeletedBlocks) > 0) {
			return true
		}
	}
	return false
}

// validateChangedGoFiles 校验所有被 diff 命中的非删除 .go 文件都能成功解析。
// 变更后源码若存在语法错误，impact 会直接失败（而非静默输出不完整范围）：
// 这里通过比对待变更文件集合与项目加载诊断，命中即返回错误。
func validateChangedGoFiles(p *project.Project, changes []diff.FileChange) error {
	changed := map[string]struct{}{}
	for _, change := range changes {
		if change.Status == diff.StatusDeleted || !strings.HasSuffix(change.NewPath, ".go") {
			continue
		}
		changed[filepath.ToSlash(change.NewPath)] = struct{}{}
	}
	for _, diagnostic := range p.Diagnostics {
		if _, ok := changed[filepath.ToSlash(diagnostic.File)]; ok {
			return fmt.Errorf("changed Go source could not be parsed: %s", diagnostic.Message)
		}
	}
	return nil
}
