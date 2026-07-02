package app

import (
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
	imextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/im"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/reference"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/route"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/impact"
	"gopkg.inshopline.com/bff/go-analyzer/internal/link"
	"gopkg.inshopline.com/bff/go-analyzer/internal/output"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func RunFacts(opts Options) ([]byte, error) {
	result, err := RunFactsWithMetrics(opts)
	if err != nil {
		return nil, err
	}
	return result.Output, nil
}

func RunFactsWithMetrics(opts Options) (RunResult, error) {
	if opts.ProjectPath == "" {
		return RunResult{}, errors.New("project path is required")
	}
	if opts.Format == "" {
		opts.Format = "json"
	}
	if opts.Format != "json" {
		return RunResult{}, fmt.Errorf("unsupported format %q", opts.Format)
	}
	recorder := &pipelineRecorder{}
	store, err := buildFactStore(opts.ProjectPath, opts.BuildContext, recorder)
	if err != nil {
		return RunResult{}, err
	}
	var out []byte
	if err := recorder.measure("facts_render", func() error {
		var renderErr error
		out, renderErr = output.RenderJSON(store)
		return renderErr
	}); err != nil {
		return RunResult{}, err
	}
	return RunResult{Output: out, Metrics: recorder.metrics()}, nil
}

func RunImpact(opts ImpactOptions) ([]byte, error) {
	result, err := RunImpactWithMetrics(opts)
	if err != nil {
		return nil, err
	}
	return result.Output, nil
}

func RunImpactWithMetrics(opts ImpactOptions) (RunResult, error) {
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
	var fileChanges []diff.FileChange
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
	built, err := buildFacts(opts.ProjectPath, opts.BuildContext, recorder)
	if err != nil {
		return RunResult{}, err
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
	if err := recorder.measure("deleted_route_recover", func() error {
		impact.RecoverDeletedRoutes(fileChanges, built.index, store, "git_diff")
		return nil
	}); err != nil {
		return RunResult{}, err
	}
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
	moduleDiffResolved := len(moduleChanges) > 0
	moduleChanges = cfg.FilterModuleChanges(moduleChanges)
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
	if err := recorder.measure("module_usage_map", func() error {
		moduleUsages = gomod.MapModuleUsage(built.project, built.index, store, moduleChanges)
		return nil
	}); err != nil {
		return RunResult{}, err
	}
	store.ModuleUsages = append(store.ModuleUsages, moduleUsages...)
	store.Changes = append(store.Changes, moduleUsageChanges(moduleUsages, store, "go_mod_diff")...)
	var result impact.TreeResult
	if err := recorder.measure("impact_analyze", func() error {
		result = impact.AnalyzeTrees(store)
		return nil
	}); err != nil {
		return RunResult{}, err
	}
	var doc output.ImpactDocument
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
	built, err := buildFacts(projectPath, buildContext, recorder)
	if err != nil {
		return nil, err
	}
	return built.store, nil
}

func buildFacts(projectPath string, buildContext project.BuildContextOptions, recorder *pipelineRecorder) (builtFacts, error) {
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
	return builtFacts{project: p, index: idx, store: store}, nil
}

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
