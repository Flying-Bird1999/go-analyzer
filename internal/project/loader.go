package project

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func Load(root string, opts Options) (*Project, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	modulePath, err := ReadModulePath(absRoot)
	if err != nil {
		return nil, err
	}
	p := &Project{
		Root:       absRoot,
		ModulePath: modulePath,
		Packages:   map[string]*Package{},
	}
	if err := filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != absRoot && shouldSkipDir(d.Name(), opts.ExcludeDirs) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		return p.loadFile(path)
	}); err != nil {
		return nil, err
	}
	return p, nil
}

func shouldSkipDir(name string, extra []string) bool {
	switch name {
	case ".git", ".cache", "vendor", "node_modules", "testdata":
		return true
	}
	for _, item := range extra {
		if strings.TrimSpace(item) == name {
			return true
		}
	}
	return false
}

func (p *Project) loadFile(path string) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	pkgPath, err := p.packagePathForDir(dir)
	if err != nil {
		return err
	}
	pkg := p.Packages[pkgPath]
	if pkg == nil {
		pkg = &Package{
			Path: pkgPath,
			Dir:  dir,
			Name: file.Name.Name,
		}
		p.Packages[pkgPath] = pkg
	}
	pkg.Files = append(pkg.Files, &File{
		Package: pkg,
		Path:    path,
		FileSet: fset,
		AST:     file,
		Imports: importMap(file),
	})
	return nil
}

func (p *Project) packagePathForDir(dir string) (string, error) {
	rel, err := filepath.Rel(p.Root, dir)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return p.ModulePath, nil
	}
	return p.ModulePath + "/" + filepath.ToSlash(rel), nil
}

func importMap(file *ast.File) map[string]string {
	imports := map[string]string{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imports[name] = path
	}
	return imports
}
