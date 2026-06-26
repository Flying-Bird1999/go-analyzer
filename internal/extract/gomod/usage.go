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

func MapModuleUsage(p *project.Project, idx *astindex.Index, store *facts.Store, changes []facts.ModuleChangeFact) []facts.ModuleUsageFact {
	var out []facts.ModuleUsageFact
	for _, change := range changes {
		matches := moduleImportMatches(p, change.Path)
		if len(matches) == 0 {
			out = append(out, facts.ModuleUsageFact{
				ID:         moduleUsageID(change.Path, "", "", facts.ModuleUsageUnreferenced),
				ModulePath: change.Path,
				Basis:      facts.ModuleUsageUnreferenced,
				Confidence: facts.ConfidenceHigh,
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
			precise := preciseUsages(p, idx, change.Path, match)
			if len(precise) > 0 {
				out = append(out, precise...)
				continue
			}
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
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

type importMatch struct {
	File       *project.File
	Alias      string
	ImportPath string
}

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
			Confidence: facts.ConfidenceHigh,
		})
	}
	return out
}

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
			Confidence: facts.ConfidenceMedium,
		})
	}
	if len(out) == 0 {
		out = append(out, facts.ModuleUsageFact{
			ID:         moduleUsageID(modulePath, match.ImportPath, file, facts.ModuleUsageFileFallback),
			ModulePath: modulePath,
			ImportPath: match.ImportPath,
			Alias:      match.Alias,
			Basis:      facts.ModuleUsageFileFallback,
			File:       file,
			Confidence: facts.ConfidenceLow,
		})
	}
	return out
}

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

func functionSymbol(pkgPath string, fn *ast.FuncDecl) facts.SymbolID {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return astindex.FunctionSymbolID(pkgPath, fn.Name.Name)
	}
	return astindex.MethodSymbolID(pkgPath, receiverTypeName(fn.Recv.List[0].Type), fn.Name.Name)
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	default:
		return ""
	}
}

func relFile(p *project.Project, path string) string {
	rel, err := filepath.Rel(p.Root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func moduleUsageID(modulePath, importPath, target string, basis facts.ModuleUsageBasis) string {
	return fmt.Sprintf("module_usage:%s:%s:%s:%s", basis, modulePath, importPath, target)
}
