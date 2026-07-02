package reference

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

type resolvedType struct {
	ID   facts.SymbolID
	Expr ast.Expr
}

func collectTypeIDs(file *project.File, idx *astindex.Index, expr ast.Expr) []resolvedType {
	if expr == nil {
		return nil
	}
	switch x := expr.(type) {
	case *ast.Ident:
		id := astindex.TypeSymbolID(file.Package.Path, x.Name)
		if _, ok := idx.Symbols[id]; ok {
			return []resolvedType{{ID: id, Expr: x}}
		}
	case *ast.SelectorExpr:
		if pkg, ok := x.X.(*ast.Ident); ok {
			if importPath := file.Imports[pkg.Name]; importPath != "" {
				id := astindex.TypeSymbolID(importPath, x.Sel.Name)
				if _, ok := idx.Symbols[id]; ok {
					return []resolvedType{{ID: id, Expr: x}}
				}
			}
		}
	case *ast.StarExpr:
		return collectTypeIDs(file, idx, x.X)
	case *ast.ArrayType:
		return collectTypeIDs(file, idx, x.Elt)
	case *ast.MapType:
		return mergeTypeIDs(
			collectTypeIDs(file, idx, x.Key),
			collectTypeIDs(file, idx, x.Value),
		)
	case *ast.ChanType:
		return collectTypeIDs(file, idx, x.Value)
	case *ast.Ellipsis:
		return collectTypeIDs(file, idx, x.Elt)
	case *ast.IndexExpr:
		return mergeTypeIDs(
			collectTypeIDs(file, idx, x.X),
			collectTypeIDs(file, idx, x.Index),
		)
	case *ast.IndexListExpr:
		out := collectTypeIDs(file, idx, x.X)
		for _, item := range x.Indices {
			out = mergeTypeIDs(out, collectTypeIDs(file, idx, item))
		}
		return out
	case *ast.StructType:
		return collectFieldListTypeIDs(file, idx, x.Fields)
	case *ast.InterfaceType:
		return collectFieldListTypeIDs(file, idx, x.Methods)
	case *ast.FuncType:
		return mergeTypeIDs(
			collectFieldListTypeIDs(file, idx, x.TypeParams),
			collectFieldListTypeIDs(file, idx, x.Params),
			collectFieldListTypeIDs(file, idx, x.Results),
		)
	case *ast.ParenExpr:
		return collectTypeIDs(file, idx, x.X)
	}
	return nil
}

func collectFieldListTypeIDs(file *project.File, idx *astindex.Index, fields *ast.FieldList) []resolvedType {
	if fields == nil {
		return nil
	}
	var out []resolvedType
	for _, field := range fields.List {
		out = mergeTypeIDs(out, collectTypeIDs(file, idx, field.Type))
	}
	return out
}

func mergeTypeIDs(groups ...[]resolvedType) []resolvedType {
	var out []resolvedType
	seen := map[facts.SymbolID]bool{}
	for _, group := range groups {
		for _, item := range group {
			if item.ID == "" || seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			out = append(out, item)
		}
	}
	return out
}

func addTypeReferences(p *project.Project, file *project.File, idx *astindex.Index, store *facts.Store, from facts.SymbolID, expr ast.Expr) {
	for _, resolved := range collectTypeIDs(file, idx, expr) {
		if resolved.ID == from {
			continue
		}
		span := spanFor(p, file, resolved.Expr.Pos(), resolved.Expr.End())
		store.References = append(store.References, facts.ReferenceFact{
			ID:         referenceID(from, resolved.ID, facts.ReferenceKindType, span),
			Kind:       facts.ReferenceKindType,
			FromSymbol: from,
			ToSymbol:   resolved.ID,
			ToRaw:      typeExprString(file, resolved.Expr),
			Confidence: facts.ConfidenceHigh,
			Span:       span,
			Evidence: []facts.EvidenceFact{{
				Kind:       "type_expr",
				Raw:        typeExprString(file, resolved.Expr),
				Span:       span,
				Confidence: facts.ConfidenceHigh,
			}},
		})
	}
	for _, unresolved := range unresolvedProjectTypes(file, idx, expr) {
		span := spanFor(p, file, unresolved.Pos(), unresolved.End())
		diagnostics.AddFact(store, diagnostics.Diagnostic{
			Code:           diagnostics.CodeTypeReferenceUnresolved,
			Severity:       diagnostics.SeverityWarning,
			Message:        fmt.Sprintf("project type reference %q could not be resolved", typeExprString(file, unresolved)),
			Span:           span,
			RelatedFactIDs: []string{string(from)},
		})
	}
}

func unresolvedProjectTypes(file *project.File, idx *astindex.Index, expr ast.Expr) []ast.Expr {
	if expr == nil {
		return nil
	}
	var out []ast.Expr
	ast.Inspect(expr, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		importPath := file.Imports[pkg.Name]
		modulePath := idx.Project.ModulePath
		if importPath != modulePath && !strings.HasPrefix(importPath, modulePath+"/") {
			return false
		}
		id := astindex.TypeSymbolID(importPath, selector.Sel.Name)
		if _, ok := idx.Symbols[id]; !ok {
			out = append(out, selector)
		}
		return false
	})
	return out
}

func typeExprString(file *project.File, expr ast.Expr) string {
	var out bytes.Buffer
	if err := printer.Fprint(&out, file.FileSet, expr); err != nil {
		return ""
	}
	return out.String()
}
