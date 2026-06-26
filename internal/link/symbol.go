package link

import (
	"path/filepath"

	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func fileByRelativePath(p *project.Project, rel string) *project.File {
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			if got, err := filepath.Rel(p.Root, file.Path); err == nil && filepath.ToSlash(got) == rel {
				return file
			}
		}
	}
	return nil
}
