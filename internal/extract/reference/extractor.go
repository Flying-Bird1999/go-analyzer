package reference

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func Extract(p *project.Project, idx *astindex.Index, store *facts.Store) error {
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					extractFuncReferences(p, file, idx, store, pkg.Path, d)
				case *ast.GenDecl:
					extractGenDeclTypeReferences(p, file, idx, store, pkg.Path, d)
				}
			}
		}
	}
	return nil
}

func extractFuncReferences(p *project.Project, file *project.File, idx *astindex.Index, store *facts.Store, pkgPath string, fn *ast.FuncDecl) {
	from := functionSymbol(pkgPath, fn)
	scopedTypes := collectScopedValueTypes(file, idx, fn)
	if fn.Recv != nil {
		for _, field := range fn.Recv.List {
			addTypeReferences(p, file, idx, store, from, field.Type)
		}
	}
	if fn.Type.TypeParams != nil {
		for _, field := range fn.Type.TypeParams.List {
			addTypeReferences(p, file, idx, store, from, field.Type)
		}
	}
	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			addTypeReferences(p, file, idx, store, from, field.Type)
		}
	}
	if fn.Type.Results != nil {
		for _, field := range fn.Type.Results.List {
			addTypeReferences(p, file, idx, store, from, field.Type)
		}
	}
	if fn.Body == nil {
		return
	}
	extractValueReferences(p, file, idx, store, from, fn)
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.CallExpr:
			for _, typeArgument := range genericTypeArguments(x.Fun) {
				addTypeReferences(p, file, idx, store, from, typeArgument)
			}
			callee := unwrapGenericCallee(x.Fun)
			if len(collectTypeIDs(file, idx, callee)) > 0 {
				addTypeReferences(p, file, idx, store, from, callee)
			} else {
				addCallReference(p, file, idx, store, from, scopedTypes, x)
			}
		case *ast.CompositeLit:
			addTypeReferences(p, file, idx, store, from, x.Type)
		}
		return true
	})
}

func extractGenDeclTypeReferences(p *project.Project, file *project.File, idx *astindex.Index, store *facts.Store, pkgPath string, decl *ast.GenDecl) {
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			from := astindex.TypeSymbolID(pkgPath, s.Name.Name)
			addTypeReferences(p, file, idx, store, from, s.Type)
		case *ast.ValueSpec:
			kind := valueDeclarationKind(decl.Tok)
			if kind == "" {
				continue
			}
			for _, name := range s.Names {
				from := astindex.ValueSymbolID(kind, pkgPath, name.Name)
				addTypeReferences(p, file, idx, store, from, s.Type)
				for _, value := range s.Values {
					extractInitializerReferences(p, file, idx, store, from, value)
				}
			}
		}
	}
}

func extractInitializerReferences(p *project.Project, file *project.File, idx *astindex.Index, store *facts.Store, from facts.SymbolID, expr ast.Expr) {
	ignored := ignoredValuePositions(expr)
	callFuns := callFunPositions(expr)
	resolver := newResolver(file, idx, scopedValueTypes{})
	ast.Inspect(expr, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.CallExpr:
			for _, typeArgument := range genericTypeArguments(x.Fun) {
				addTypeReferences(p, file, idx, store, from, typeArgument)
			}
			callee := unwrapGenericCallee(x.Fun)
			if len(collectTypeIDs(file, idx, callee)) > 0 {
				addTypeReferences(p, file, idx, store, from, callee)
			} else {
				addCallReference(p, file, idx, store, from, scopedValueTypes{}, x)
			}
		case *ast.CompositeLit:
			addTypeReferences(p, file, idx, store, from, x.Type)
		case *ast.SelectorExpr:
			if ignored[x.Pos()] {
				return false
			}
			var targets []facts.SymbolID
			if callFuns[x.Pos()] {
				targets = resolver.ResolveReceiverValueIDs(x)
			} else {
				targets = resolver.ResolveValueIDs(x)
			}
			addValueReferenceFacts(p, file, store, from, x, targets)
			return false
		case *ast.Ident:
			if ignored[x.Pos()] || callFuns[x.Pos()] {
				return true
			}
			addValueReferenceFacts(p, file, store, from, x, resolver.ResolveValueIDs(x))
		}
		return true
	})
}

func addCallReference(p *project.Project, file *project.File, idx *astindex.Index, store *facts.Store, from facts.SymbolID, scopedTypes scopedValueTypes, call *ast.CallExpr) {
	resolver := newResolver(file, idx, scopedTypes)
	resolved, raw, ok := resolver.ResolveCall(call)
	if !ok || len(resolved) == 0 {
		callee := unwrapGenericCallee(call.Fun)
		if code, message, diagnosticOK := resolver.UnresolvedProjectCallDiagnostic(callee); !ok && diagnosticOK {
			span := spanFor(p, file, callee.Pos(), callee.End())
			diagnostics.AddFact(store, diagnostics.Diagnostic{
				Code:           code,
				Severity:       diagnostics.SeverityWarning,
				Message:        message,
				Span:           span,
				RelatedFactIDs: []string{string(from)},
			})
		}
		return
	}
	span := spanFor(p, file, call.Pos(), call.End())
	for _, candidate := range resolved {
		if candidate.ID == "" || candidate.ID == from {
			continue
		}
		store.References = append(store.References, facts.ReferenceFact{
			ID:         referenceID(from, candidate.ID, facts.ReferenceKindCall, span),
			Kind:       facts.ReferenceKindCall,
			FromSymbol: from,
			ToSymbol:   candidate.ID,
			ToRaw:      raw,
			Confidence: candidate.Confidence,
			Span:       span,
			Evidence: []facts.EvidenceFact{{
				Kind:       "call_expr",
				Raw:        raw,
				Span:       span,
				Confidence: candidate.Confidence,
			}},
		})
	}
}

func valueDeclarationKind(tok token.Token) string {
	switch tok {
	case token.VAR:
		return "var"
	case token.CONST:
		return "const"
	default:
		return ""
	}
}

func functionSymbol(pkgPath string, fn *ast.FuncDecl) facts.SymbolID {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return astindex.FunctionSymbolID(pkgPath, fn.Name.Name)
	}
	return astindex.MethodSymbolID(pkgPath, astindex.ReceiverTypeName(fn.Recv.List[0].Type), fn.Name.Name)
}

func referenceID(from, to facts.SymbolID, kind facts.ReferenceKind, span facts.SourceSpan) string {
	return fmt.Sprintf(
		"ref:%s:%s:%s:%s:%d:%d:%d:%d",
		kind,
		from,
		to,
		span.File,
		span.StartLine,
		span.StartCol,
		span.EndLine,
		span.EndCol,
	)
}

func spanFor(p *project.Project, file *project.File, start, end token.Pos) facts.SourceSpan {
	span := astindex.SourceSpanFor(file.FileSet, start, end)
	if rel, err := filepath.Rel(p.Root, span.File); err == nil {
		span.File = filepath.ToSlash(rel)
	}
	return span
}
