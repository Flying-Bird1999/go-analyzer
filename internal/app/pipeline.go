package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/config"
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
	cfg, err := loadConfig(opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	store, err := buildFactStore(opts.ProjectPath, cfg)
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
	cfg, err := loadConfig(opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	store, err := buildFactStore(opts.ProjectPath, cfg)
	if err != nil {
		return nil, err
	}
	diffBytes, err := os.ReadFile(opts.DiffPath)
	if err != nil {
		return nil, fmt.Errorf("read diff: %w", err)
	}
	fileChanges, err := diff.ParseUnified(diffBytes)
	if err != nil {
		return nil, err
	}
	store.Changes = append(store.Changes, diff.MapChanges(fileChanges, store, "git_diff")...)
	result := impact.AnalyzeTrees(store, impact.TreeOptions{
		MaxDepth:        cfg.Analysis.MaxDepth,
		StopPropagation: cfg.Analysis.StopPropagation,
	})
	doc := output.BuildImpactDocument(store.Project, fileChanges, result, output.ImpactDocumentOptions{
		IncludeDiff:        cfg.Analysis.IncludeDiff,
		IncludeRawEvidence: cfg.Analysis.IncludeRawEvidence,
	})
	return output.RenderImpactTreeJSON(doc)
}

func loadConfig(path string) (config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return config.Config{}, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}

func buildFactStore(projectPath string, cfg config.Config) (*facts.Store, error) {
	p, err := project.Load(projectPath, project.Options{ExcludeDirs: cfg.Project.SkipDirs})
	if err != nil {
		return nil, err
	}
	idx, err := astindex.Build(p)
	if err != nil {
		return nil, err
	}
	store := facts.NewStore(p.Root, p.ModulePath)
	modBytes, err := os.ReadFile(filepath.Join(p.Root, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("read go.mod dependencies: %w", err)
	}
	modules, err := gomod.ExtractDependencies(modBytes)
	if err != nil {
		return nil, fmt.Errorf("extract go.mod dependencies: %w", err)
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
	if err := annotation.ExtractWithConfig(p, idx, store, cfg); err != nil {
		return nil, err
	}
	if err := route.ExtractWithConfig(p, idx, store, cfg); err != nil {
		return nil, err
	}
	if err := link.Run(idx, store); err != nil {
		return nil, err
	}
	if err := reference.Extract(p, idx, store); err != nil {
		return nil, err
	}
	return store, nil
}
