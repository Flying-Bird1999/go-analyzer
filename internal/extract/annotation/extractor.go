package annotation

import (
	"go/ast"
	"path/filepath"
	"sort"
	"strconv"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/config"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func Extract(p *project.Project, _ *astindex.Index, store *facts.Store) error {
	return ExtractWithConfig(p, nil, store, config.Default())
}

func ExtractWithConfig(p *project.Project, _ *astindex.Index, store *facts.Store, cfg config.Config) error {
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				handler := handlerSymbolID(pkg.Path, fn)
				parsed := ParseAPIAnnotationsWithConfig(fn.Doc, cfg)
				for i, item := range parsed {
					span := astindex.SourceSpanFor(file.FileSet, fn.Pos(), fn.End())
					if rel, err := filepath.Rel(p.Root, span.File); err == nil {
						span.File = filepath.ToSlash(rel)
					}
					store.Annotations = append(store.Annotations, facts.AnnotationFact{
						ID:            annotationID(handler, item.Method, item.Path, i),
						Kind:          "annotation",
						Method:        item.Method,
						Path:          item.Path,
						Raw:           item.Raw,
						HandlerSymbol: handler,
						Span:          span,
					})
				}
			}
		}
	}
	sort.SliceStable(store.Annotations, func(i, j int) bool {
		return store.Annotations[i].ID < store.Annotations[j].ID
	})
	return nil
}

func handlerSymbolID(pkgPath string, fn *ast.FuncDecl) facts.SymbolID {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return astindex.FunctionSymbolID(pkgPath, fn.Name.Name)
	}
	return astindex.MethodSymbolID(pkgPath, receiverTypeName(fn.Recv.List[0].Type), fn.Name.Name)
}

func annotationID(handler facts.SymbolID, method, path string, index int) string {
	return "annotation:" + string(handler) + ":" + method + ":" + path + ":" + strconv.Itoa(index)
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.IndexExpr:
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	default:
		return ""
	}
}
