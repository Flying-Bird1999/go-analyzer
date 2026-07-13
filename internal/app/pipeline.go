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
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/annotation"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/gomod"
	grpcextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/grpc"
	imextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/im"
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
// 流程按顺序为：diff 读取 -> diff 解析 -> diff 应用校验 -> 构建事实 -> 变更文件语法校验
// -> diff 语义映射 -> 删除路由恢复 -> go.mod module diff -> module usage 映射
// -> impact 树构建 -> impact 文档构建 -> impact JSON 渲染。
func RunImpactWithMetrics(opts ImpactOptions) (RunResult, error) {
	// project path 与 diff path 均为必填，缺失直接失败。
	if opts.ProjectPath == "" {
		return RunResult{}, errors.New("project path is required")
	}
	if opts.DiffPath == "" {
		return RunResult{}, errors.New("diff path is required")
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
	// 加载可选的 impact 配置（过滤 module 变更等）；严格字段校验，未知字段直接失败。
	cfg, err := config.LoadImpactConfig(opts.ProjectPath, opts.ImpactConfigPath)
	if err != nil {
		return RunResult{}, err
	}
	var diffBytes []byte
	// diff_read：读取 unified diff 原始字节。
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
	var fileChanges []diff.FileChange
	// diff_parse：解析 unified diff，得到文件级变更、删除块等信息。
	if err := recorder.measure("diff_parse", func() error {
		var parseErr error
		fileChanges, parseErr = diff.ParseUnified(diffBytes)
		return parseErr
	}); err != nil {
		return RunResult{}, err
	}
	// diff_validate：逐行校验 diff 是否已应用到 project 指向的变更后源码快照；
	// 路径越界、空 diff、旧快照或不匹配快照都会直接失败。
	if err := recorder.measure("diff_validate", func() error {
		return diff.ValidateApplied(opts.ProjectPath, fileChanges)
	}); err != nil {
		return RunResult{}, err
	}
	// 构建变更后项目的完整事实库（含 project/index/extractor/linker）。
	built, err := buildFacts(opts.ProjectPath, opts.BuildContext, recorder, buildFactsOptions{grpcMode: grpcModeOff})
	if err != nil {
		return RunResult{}, err
	}
	// 变更后 Go 文件解析失败属于致命输入错误，直接失败以避免静默输出不完整范围。
	if err := validateChangedGoFiles(built.project, fileChanges); err != nil {
		return RunResult{}, err
	}
	store := built.store
	// diff_map：把 diff 命中映射到正常语义根（annotation/route/middleware/symbol/file）。
	if err := recorder.measure("diff_map", func() error {
		store.Changes = append(store.Changes, diff.MapChanges(fileChanges, store, "git_diff")...)
		return nil
	}); err != nil {
		return RunResult{}, err
	}
	// deleted_route_recover：从 diff 删除块恢复已删除的 route registration，
	// 添加 synthetic route 与 route_deleted 根。
	if err := recorder.measure("deleted_route_recover", func() error {
		impact.RecoverDeletedRoutes(fileChanges, built.index, store, "git_diff")
		return nil
	}); err != nil {
		return RunResult{}, err
	}
	var moduleChanges []facts.ModuleChangeFact
	// gomod_diff：从 go.mod diff 的新增/删除行恢复 module changes（require/replace，含 block）。
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
	// moduleDiffResolved 表示 go.mod diff 是否成功解析出 module 语义变更，
	// 用于决定公开输出是否压制低置信度的 go.mod fileSources 噪音。
	moduleDiffResolved := len(moduleChanges) > 0
	// 应用 impact 配置过滤（如 ignoredModuleChanges），只作用于 moduleSources。
	moduleChanges = cfg.FilterModuleChanges(moduleChanges)
	// go.mod diff 存在但无法识别 module 变更时，记录可恢复诊断（仅 facts 阶段可见）。
	if hasGoModDiff(fileChanges) && !moduleDiffResolved {
		diagnostics.AddFact(store, diagnostics.Diagnostic{
			Code:     diagnostics.CodeModuleDiffUnresolved,
			Severity: diagnostics.SeverityWarning,
			Message:  "go.mod changed, but no require or replace module change could be resolved",
			Span:     facts.SourceSpan{File: "go.mod"},
		})
	}
	store.ModuleChanges = append(store.ModuleChanges, moduleChanges...)
	var moduleUsages []facts.ModuleUsageFact
	// module_usage_map：把变更后的 module 映射到本仓 import usage，得到 symbol/file 级入口。
	if err := recorder.measure("module_usage_map", func() error {
		moduleUsages = gomod.MapModuleUsage(built.project, built.index, store, moduleChanges)
		return nil
	}); err != nil {
		return RunResult{}, err
	}
	store.ModuleUsages = append(store.ModuleUsages, moduleUsages...)
	// 把 module usage 转换成 ChangeFact，纳入正常 impact 传播，source 标记为 go_mod_diff。
	store.Changes = append(store.Changes, moduleUsageChanges(moduleUsages, store, "go_mod_diff")...)
	var result impact.TreeResult
	// impact_analyze：以每个 ChangeFact 为根，沿 ReverseGraph / RouteGraph / IMGraph 扩散，
	// 产出原始传播树、endpoint 摘要和 IM event 摘要。
	if err := recorder.measure("impact_analyze", func() error {
		result = impact.AnalyzeTrees(store)
		return nil
	}); err != nil {
		return RunResult{}, err
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

type builtFacts struct {
	project *project.Project
	index   *astindex.Index
	store   *facts.Store
}

func buildFactStore(projectPath string, buildContext project.BuildContextOptions, recorder *pipelineRecorder) (*facts.Store, error) {
	built, err := buildFacts(projectPath, buildContext, recorder, buildFactsOptions{grpcMode: grpcModeDiagnostic})
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

type buildFactsOptions struct{ grpcMode grpcMode }

func buildFacts(projectPath string, buildContext project.BuildContextOptions, recorder *pipelineRecorder, options buildFactsOptions) (builtFacts, error) {
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
		grpcBuildContext := project.BuildContextOptions{GOOS: p.BuildContext.GOOS, GOARCH: p.BuildContext.GOARCH, Tags: append([]string(nil), p.BuildContext.Tags...)}
		cgo := p.BuildContext.CgoEnabled
		grpcBuildContext.CgoEnabled = &cgo
		var dependencies []project.DependencyPackage
		if err := recorder.measure("dependency_list", func() error {
			var dependencyErr error
			dependencies, dependencyErr = project.DiscoverDependencies(context.Background(), p.Root, grpcBuildContext)
			return dependencyErr
		}); err != nil {
			if options.grpcMode == grpcModeStrict {
				return builtFacts{}, err
			}
			diagnostics.AddFact(store, diagnostics.Diagnostic{ID: "diagnostic:grpc_dependency_load", Code: diagnostics.CodeGrpcDependencyLoadFailed, Severity: diagnostics.SeverityWarning, Message: err.Error()})
			return builtFacts{project: p, index: idx, store: store}, nil
		}
		if err := recorder.measure("grpc_extract", func() error {
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
	return builtFacts{project: p, index: idx, store: store}, nil
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
