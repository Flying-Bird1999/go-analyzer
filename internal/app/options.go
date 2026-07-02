package app

import "gopkg.inshopline.com/bff/go-analyzer/internal/project"

type Options struct {
	ProjectPath  string
	Format       string
	BuildContext project.BuildContextOptions
}

type ImpactOptions struct {
	ProjectPath      string
	DiffPath         string
	ImpactConfigPath string
	Format           string
	BuildContext     project.BuildContextOptions
}
