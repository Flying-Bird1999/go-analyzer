// usage.go 实现把变更 module 映射到本仓 import usage（gomod 三层职责中的第三层）。
package gomod

import (
	"fmt"
	"go/ast"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// MapModuleUsage 把每条 ModuleChangeFact 映射到本仓的使用点。
//
// 映射精度（basis）分三档：
//   - precise：函数/方法体直接使用 import alias，可定位到具体 symbol；
//   - module_reference_file_fallback：只能确认 import 所在文件，降级到该文件内的声明
//     （文件无声明时进一步降级为纯文件级 usage）；
//   - module_unreferenced：本仓没有 import 该 module，不产生 endpoint root。
//
// 降级情形会写入对应 diagnostic，便于 facts 调试时检查 usage 精度。
func MapModuleUsage(p *project.Project, idx *astindex.Index, store *facts.Store, changes []facts.ModuleChangeFact) []facts.ModuleUsageFact {
	var out []facts.ModuleUsageFact
	for _, change := range changes {
		matches := moduleImportMatches(p, change.Path)
		if len(matches) == 0 {
			// 本仓无 import：标记 unreferenced，不进入 endpoint 传播。
			out = append(out, facts.ModuleUsageFact{
				ID:         moduleUsageID(change.Path, "", "", facts.ModuleUsageUnreferenced),
				ModulePath: change.Path,
				Basis:      facts.ModuleUsageUnreferenced,
			})
			if store != nil {
				diagnostics.AddFact(store, diagnostics.Diagnostic{
					Code:     diagnostics.CodeModuleUnreferenced,
					Severity: diagnostics.SeverityInfo,
					Message:  "changed module is not imported by the project",
				})
			}
			continue
		}
		for _, match := range matches {
			// 优先尝试精确定位到使用了 alias 的处理函数/方法。
			precise := preciseUsages(p, idx, change.Path, match)
			if len(precise) > 0 {
				out = append(out, precise...)
				continue
			}
			// 精确匹配失败时降级到 importing file 内的声明。
			fallback := fallbackUsages(p, idx, change.Path, match)
			out = append(out, fallback...)
			if store != nil {
				for _, usage := range fallback {
					diagnostics.AddFact(store, diagnostics.Diagnostic{
						Code:           diagnostics.CodeModuleUsageFileFallback,
						Severity:       diagnostics.SeverityWarning,
						Message:        "module usage fell back to declarations in importing file",
						RelatedFactIDs: []string{usage.ID},
					})
				}
			}
		}
	}
	// 按 ID 排序，保证输出稳定。
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// importMatch 描述一个 import 与目标 module 的匹配结果。
type importMatch struct {
	File       *project.File
	Alias      string // import 别名（无显式别名时为包名）
	ImportPath string // 完整 import path
}

// moduleImportMatches 在项目所有文件中查找匹配目标 module 的 import。
// 命中条件：import path 等于 modulePath，或以其为前缀（modulePath + "/"），
// 因为变更的可能是 module 自身，也可能是其子包。
func moduleImportMatches(p *project.Project, modulePath string) []importMatch {
	var out []importMatch
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for alias, importPath := range file.Imports {
				if importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/") {
					out = append(out, importMatch{File: file, Alias: alias, ImportPath: importPath})
				}
			}
		}
	}
	return out
}

// preciseUsages 尝试在该 import 所在文件中精确定位使用 alias 的处理函数/方法。
// _ / . 形式的 import 无法定位到具体 symbol，直接返回 nil 触发降级。
func preciseUsages(p *project.Project, idx *astindex.Index, modulePath string, match importMatch) []facts.ModuleUsageFact {
	if match.Alias == "_" || match.Alias == "." {
		return nil
	}
	var out []facts.ModuleUsageFact
	for _, decl := range match.File.AST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || !usesAlias(fn.Body, match.Alias) {
			continue
		}
		symbolID := functionSymbol(match.File.Package.Path, fn)
		// 只采纳已被 astindex 收录的声明 symbol，保证后续传播一致。
		if _, ok := idx.Symbols[symbolID]; !ok {
			continue
		}
		out = append(out, facts.ModuleUsageFact{
			ID:         moduleUsageID(modulePath, match.ImportPath, string(symbolID), facts.ModuleUsagePrecise),
			ModulePath: modulePath,
			ImportPath: match.ImportPath,
			Alias:      match.Alias,
			Basis:      facts.ModuleUsagePrecise,
			SymbolID:   symbolID,
			File:       relFile(p, match.File.Path),
		})
	}
	return out
}

// fallbackUsages 在无法精确定位 symbol 时，把 import 所在文件内的所有声明
// 都作为 usage 候选；若文件内没有任何声明，则退化为仅以文件为粒度的 usage。
func fallbackUsages(p *project.Project, idx *astindex.Index, modulePath string, match importMatch) []facts.ModuleUsageFact {
	var out []facts.ModuleUsageFact
	file := relFile(p, match.File.Path)
	for _, symbol := range idx.Symbols {
		if symbol.Span.File != file {
			continue
		}
		out = append(out, facts.ModuleUsageFact{
			ID:         moduleUsageID(modulePath, match.ImportPath, string(symbol.ID), facts.ModuleUsageFileFallback),
			ModulePath: modulePath,
			ImportPath: match.ImportPath,
			Alias:      match.Alias,
			Basis:      facts.ModuleUsageFileFallback,
			SymbolID:   symbol.ID,
			File:       file,
		})
	}
	if len(out) == 0 {
		// 文件内没有可索引声明时，保留一个纯文件级 usage 作为最低粒度证据。
		out = append(out, facts.ModuleUsageFact{
			ID:         moduleUsageID(modulePath, match.ImportPath, file, facts.ModuleUsageFileFallback),
			ModulePath: modulePath,
			ImportPath: match.ImportPath,
			Alias:      match.Alias,
			Basis:      facts.ModuleUsageFileFallback,
			File:       file,
		})
	}
	return out
}

// usesAlias 判断函数体内是否使用了给定的 import alias（形如 alias.XXX 的 selector）。
// 找到首个匹配即短路返回，提升扫描效率。
func usesAlias(node ast.Node, alias string) bool {
	used := false
	ast.Inspect(node, func(n ast.Node) bool {
		if used {
			return false
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if ok && ident.Name == alias {
			used = true
			return false
		}
		return true
	})
	return used
}

// functionSymbol 根据函数是否带 receiver 返回 function 或 method 形式的稳定 symbol ID。
func functionSymbol(pkgPath string, fn *ast.FuncDecl) facts.SymbolID {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return astindex.FunctionSymbolID(pkgPath, fn.Name.Name)
	}
	return astindex.MethodSymbolID(pkgPath, astindex.ReceiverTypeName(fn.Recv.List[0].Type), fn.Name.Name)
}

// relFile 把绝对路径转换为项目相对路径（用 "/" 分隔），转换失败时回退为原始斜杠形式。
func relFile(p *project.Project, path string) string {
	rel, err := filepath.Rel(p.Root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// moduleUsageID 拼装形如 module_usage:<basis>:<module>:<import>:<target> 的稳定 usage ID。
// target 通常是 symbol ID，降级到纯文件时为文件相对路径。
func moduleUsageID(modulePath, importPath, target string, basis facts.ModuleUsageBasis) string {
	return fmt.Sprintf("module_usage:%s:%s:%s:%s", basis, modulePath, importPath, target)
}
