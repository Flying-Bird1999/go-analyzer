package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/annotation"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/gomod"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/reference"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/route"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/impact"
	"gopkg.inshopline.com/bff/go-analyzer/internal/link"
	"gopkg.inshopline.com/bff/go-analyzer/internal/output"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func RunFacts(opts Options) ([]byte, error) {
	if opts.ProjectPath == "" {
		return nil, errors.New("project path is required")
	}
	if opts.Format == "" {
		opts.Format = "json"
	}
	if opts.Format != "json" {
		return nil, fmt.Errorf("unsupported format %q", opts.Format)
	}
	store, err := buildFactStore(opts.ProjectPath)
	if err != nil {
		return nil, err
	}
	return output.RenderJSON(store)
}

func RunImpact(opts ImpactOptions) ([]byte, error) {
	if opts.ProjectPath == "" {
		return nil, errors.New("project path is required")
	}
	if opts.DiffPath == "" {
		return nil, errors.New("diff path is required")
	}
	if opts.Format == "" {
		opts.Format = "json"
	}
	if opts.Format != "json" {
		return nil, fmt.Errorf("unsupported format %q", opts.Format)
	}
	diffBytes, err := os.ReadFile(opts.DiffPath)
	if err != nil {
		return nil, fmt.Errorf("read diff: %w", err)
	}
	fileChanges, err := diff.ParseUnified(diffBytes)
	if err != nil {
		return nil, err
	}
	if err := diff.ValidateApplied(opts.ProjectPath, fileChanges); err != nil {
		return nil, err
	}
	built, err := buildFacts(opts.ProjectPath)
	if err != nil {
		return nil, err
	}
	if err := validateChangedGoFiles(built.project, fileChanges); err != nil {
		return nil, err
	}
	store := built.store
	store.Changes = append(store.Changes, diff.MapChanges(fileChanges, store, "git_diff")...)
	impact.RecoverDeletedRoutes(fileChanges, built.index, store, "git_diff")
	moduleChanges, err := gomod.DiffModulesFromFileChanges(fileChanges)
	if err != nil {
		return nil, fmt.Errorf("diff go.mod modules: %w", err)
	}
	if hasGoModDiff(fileChanges) && len(moduleChanges) == 0 {
		diagnostics.AddFact(store, diagnostics.Diagnostic{
			Code:     diagnostics.CodeModuleDiffUnresolved,
			Severity: diagnostics.SeverityWarning,
			Message:  "go.mod changed, but no require or replace module change could be resolved",
			Span:     facts.SourceSpan{File: "go.mod"},
		})
	}
	store.ModuleChanges = append(store.ModuleChanges, moduleChanges...)
	moduleUsages := gomod.MapModuleUsage(built.project, built.index, store, moduleChanges)
	store.ModuleUsages = append(store.ModuleUsages, moduleUsages...)
	store.Changes = append(store.Changes, moduleUsageChanges(moduleUsages, store, "go_mod_diff")...)
	result := impact.AnalyzeTrees(store)
	doc := output.BuildImpactDocument(fileChanges, result, output.ImpactDocumentOptions{
		ModuleChanges: store.ModuleChanges,
		ModuleUsages:  store.ModuleUsages,
	})
	return output.RenderImpactTreeJSON(doc)
}

type builtFacts struct {
	project *project.Project
	index   *astindex.Index
	store   *facts.Store
}

func buildFactStore(projectPath string) (*facts.Store, error) {
	built, err := buildFacts(projectPath)
	if err != nil {
		return nil, err
	}
	return built.store, nil
}

func buildFacts(projectPath string) (builtFacts, error) {
	p, err := project.Load(projectPath, project.Options{})
	if err != nil {
		return builtFacts{}, err
	}
	idx, err := astindex.Build(p)
	if err != nil {
		return builtFacts{}, err
	}
	store := facts.NewStore(p.Root, p.ModulePath)
	modBytes, err := os.ReadFile(filepath.Join(p.Root, "go.mod"))
	if err != nil {
		return builtFacts{}, fmt.Errorf("read go.mod dependencies: %w", err)
	}
	modules, err := gomod.ExtractDependencies(modBytes)
	if err != nil {
		return builtFacts{}, fmt.Errorf("extract go.mod dependencies: %w", err)
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
	for _, symbol := range idx.Symbols {
		store.AddSymbol(symbol)
	}
	if err := annotation.Extract(p, idx, store); err != nil {
		return builtFacts{}, err
	}
	if err := route.Extract(p, idx, store); err != nil {
		return builtFacts{}, err
	}
	if err := link.Run(idx, store); err != nil {
		return builtFacts{}, err
	}
	if err := reference.Extract(p, idx, store); err != nil {
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
