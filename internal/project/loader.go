package project

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func Load(root string) (*Project, error) {
	return LoadWithOptions(root, LoadOptions{})
}

func LoadWithOptions(root string, opts LoadOptions) (*Project, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	modulePath, err := ReadModulePath(absRoot)
	if err != nil {
		return nil, err
	}
	buildCtx := buildContext(opts.BuildContext)
	p := &Project{
		Root:         absRoot,
		ModulePath:   modulePath,
		BuildContext: effectiveBuildContext(buildCtx),
		Packages:     map[string]*Package{},
		Diagnostics:  []LoadDiagnostic{},
	}
	if err := filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != absRoot && shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if isGoIgnoredName(d.Name()) || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if !matchesBuildContext(buildCtx, path) {
			return nil
		}
		return p.loadFile(path)
	}); err != nil {
		return nil, err
	}
	return p, nil
}

func shouldSkipDir(name string) bool {
	if isGoIgnoredName(name) {
		return true
	}
	switch name {
	case "vendor", "node_modules", "testdata":
		return true
	}
	return false
}

func buildContext(opts BuildContextOptions) build.Context {
	ctx := build.Default
	if opts.GOOS != "" {
		ctx.GOOS = opts.GOOS
	}
	if opts.GOARCH != "" {
		ctx.GOARCH = opts.GOARCH
	}
	if opts.Tags != nil {
		ctx.BuildTags = normalizeBuildTags(opts.Tags)
	}
	if opts.CgoEnabled != nil {
		ctx.CgoEnabled = *opts.CgoEnabled
	}
	return ctx
}

func effectiveBuildContext(ctx build.Context) BuildContext {
	return BuildContext{
		GOOS:       ctx.GOOS,
		GOARCH:     ctx.GOARCH,
		Tags:       normalizeBuildTags(ctx.BuildTags),
		CgoEnabled: ctx.CgoEnabled,
	}
}

func normalizeBuildTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}

func matchesBuildContext(ctx build.Context, path string) bool {
	matched, err := ctx.MatchFile(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return true
	}
	return matched
}

func isGoIgnoredName(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func (p *Project) loadFile(path string) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		rel, relErr := filepath.Rel(p.Root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		message := strings.ReplaceAll(err.Error(), path, rel)
		p.Diagnostics = append(p.Diagnostics, LoadDiagnostic{
			Code:    "package_load_failed",
			File:    rel,
			Message: "failed to parse Go source: " + message,
		})
		return nil
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
