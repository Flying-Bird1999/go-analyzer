// loader.go 实现 project 包的项目加载器：从根目录读取 go.mod、按构建上下文过滤、
// 递归扫描 .go 文件并使用 go/parser 生成 AST。

// Package project 加载 Go module 供静态分析使用。
//
// 它负责读取根目录 go.mod 的 module path，递归扫描项目内的 .go 文件，跳过
// _test.go 以及 Go 工具链忽略的 _ / . 前缀文件和目录（如 vendor、testdata），
// 再用显式或默认的 Go 构建上下文过滤 build constraints，最后借助 go/parser
// （开启 parser.ParseComments）为每个文件生成 AST。
//
// 单个普通源码解析失败时只会记录 package_load_failed 诊断并继续分析其他文件，
// 而 impact 涉及的变更后 Go 文件若无法解析则由调用方直接失败，避免静默输出
// 不完整的影响范围。所有 project.File.Path 在内存中均为绝对路径，输出时再统一
// 转换为项目相对路径。
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

	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
)

// Load 以默认选项加载 root 目录下的 Go 项目。
func Load(root string) (*Project, error) {
	return LoadWithOptions(root, LoadOptions{})
}

// LoadWithOptions 按指定选项加载项目：解析 go.mod 得到 module path，构造构建上下文，
// 递归遍历目录树，对每个通过过滤的 .go 文件解析 AST 并归入对应 Package。
func LoadWithOptions(root string, opts LoadOptions) (*Project, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	modulePath, err := ReadModulePath(absRoot)
	if err != nil {
		return nil, err
	}
	// 合并显式选项与 go/build 默认值，得到实际用于过滤的构建上下文。
	buildCtx := buildContext(opts.BuildContext)
	p := &Project{
		Root:         absRoot,
		ModulePath:   modulePath,
		BuildContext: effectiveBuildContext(buildCtx),
		Packages:     map[string]*Package{},
		Diagnostics:  []LoadDiagnostic{},
		moduleRoots:  map[string]string{absRoot: modulePath},
	}
	if err := filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// 非根目录下命中 Go 工具链忽略规则或 vendor/testdata 等目录时整目录跳过。
			if path != absRoot && shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		// 跳过 _/. 前缀文件、非 .go 文件以及 _test.go。
		if isGoIgnoredName(d.Name()) || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// 按构建上下文与 build constraints 过滤。
		if !matchesBuildContext(buildCtx, path) {
			return nil
		}
		return p.loadFile(path)
	}); err != nil {
		return nil, err
	}
	return p, nil
}

// shouldSkipDir 判断目录是否应整体跳过：Go 工具链忽略的 _/. 前缀目录，
// 以及 vendor、node_modules、testdata 等不应进入分析的目录。
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

// buildContext 基于选项构造构建上下文，未指定的字段沿用 go/build.Default。
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

// effectiveBuildContext 将 go/build.Context 投影为对外稳定的 BuildContext 快照。
func effectiveBuildContext(ctx build.Context) BuildContext {
	return BuildContext{
		GOOS:       ctx.GOOS,
		GOARCH:     ctx.GOARCH,
		Tags:       normalizeBuildTags(ctx.BuildTags),
		CgoEnabled: ctx.CgoEnabled,
	}
}

// normalizeBuildTags 去重、去空白并清理构建标签列表。
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

// matchesBuildContext 利用构建上下文判断单个文件是否应被纳入分析。
// 出现解析错误时按 Go 工具链的兼容行为保守保留该文件，避免误删源码。
func matchesBuildContext(ctx build.Context, path string) bool {
	matched, err := ctx.MatchFile(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return true
	}
	return matched
}

// isGoIgnoredName 判断是否为 Go 工具链忽略的 . 或 _ 前缀文件/目录名。
func isGoIgnoredName(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// loadFile 解析单个 .go 文件并追加到所属 Package。
// 解析失败时不中断整体加载，而是记录 package_load_failed 诊断后继续。
func (p *Project) loadFile(path string) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		// 诊断中的路径转换为项目相对路径，便于在输出中阅读。
		rel, relErr := filepath.Rel(p.Root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		message := strings.ReplaceAll(err.Error(), path, rel)
		p.Diagnostics = append(p.Diagnostics, LoadDiagnostic{
			Code:    string(diagnostics.CodePackageLoadFailed),
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

// packagePathForDir 将磁盘目录转换为完整包路径（module path 加上相对子路径）。
func (p *Project) packagePathForDir(dir string) (string, error) {
	moduleRoot, modulePath := p.moduleForDir(dir)
	rel, err := filepath.Rel(moduleRoot, dir)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return modulePath, nil
	}
	return modulePath + "/" + filepath.ToSlash(rel), nil
}

func (p *Project) moduleForDir(dir string) (string, string) {
	for current := dir; ; current = filepath.Dir(current) {
		if modulePath := p.moduleRoots[current]; modulePath != "" {
			return current, modulePath
		}
		if current != p.Root {
			if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
				if modulePath, readErr := ReadModulePath(current); readErr == nil {
					p.moduleRoots[current] = modulePath
					return current, modulePath
				}
			}
		}
		if current == p.Root || filepath.Dir(current) == current {
			return p.Root, p.ModulePath
		}
	}
}

// importMap 从文件 import 列表构建“导入别名/包名 -> import path”的映射，
// 显式别名优先于 path 的 base 名，供后续引用解析使用。
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
