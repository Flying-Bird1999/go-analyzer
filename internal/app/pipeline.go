package app

import (
	"errors"
	"fmt"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/annotation"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/reference"
	"gopkg.inshopline.com/bff/go-analyzer/internal/extract/route"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
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
	p, err := project.Load(opts.ProjectPath, project.Options{})
	if err != nil {
		return nil, err
	}
	idx, err := astindex.Build(p)
	if err != nil {
		return nil, err
	}
	store := facts.NewStore(p.Root, p.ModulePath)
	for _, symbol := range idx.Symbols {
		store.AddSymbol(symbol)
	}
	if err := annotation.Extract(p, idx, store); err != nil {
		return nil, err
	}
	if err := route.Extract(p, idx, store); err != nil {
		return nil, err
	}
	if err := link.Run(idx, store); err != nil {
		return nil, err
	}
	if err := reference.Extract(p, idx, store); err != nil {
		return nil, err
	}
	return output.RenderJSON(store)
}
