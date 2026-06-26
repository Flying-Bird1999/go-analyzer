package astindex

import (
	"go/ast"
	"go/token"
	"path/filepath"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

type Index struct {
	Project          *project.Project
	Symbols          map[facts.SymbolID]facts.SymbolFact
	VarReceiverTypes map[string]string
}

func Build(p *project.Project) (*Index, error) {
	idx := &Index{
		Project:          p,
		Symbols:          map[facts.SymbolID]facts.SymbolFact{},
		VarReceiverTypes: map[string]string{},
	}
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				switch d := decl.(type) {
				case *ast.GenDecl:
					idx.indexGenDecl(p, pkg, file, d)
				case *ast.FuncDecl:
					idx.indexFuncDecl(p, pkg, file, d)
				}
			}
		}
	}
	return idx, nil
}

func (idx *Index) indexGenDecl(p *project.Project, pkg *project.Package, file *project.File, decl *ast.GenDecl) {
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			id := TypeSymbolID(pkg.Path, s.Name.Name)
			idx.Symbols[id] = symbolFact(p, file, id, "type", pkg.Path, "", s.Name.Name, s.Pos(), s.End())
		case *ast.ValueSpec:
			kind := valueKind(decl.Tok)
			if kind == "" {
				continue
			}
			for _, name := range s.Names {
				id := ValueSymbolID(kind, pkg.Path, name.Name)
				idx.Symbols[id] = symbolFact(p, file, id, kind, pkg.Path, "", name.Name, name.Pos(), name.End())
				if kind == "var" {
					if receiver := receiverTypeFromValueSpec(s); receiver != "" {
						idx.VarReceiverTypes[string(id)] = receiver
					}
				}
			}
		}
	}
}

func (idx *Index) indexFuncDecl(p *project.Project, pkg *project.Package, file *project.File, decl *ast.FuncDecl) {
	if decl.Recv == nil || len(decl.Recv.List) == 0 {
		id := FunctionSymbolID(pkg.Path, decl.Name.Name)
		idx.Symbols[id] = symbolFact(p, file, id, "func", pkg.Path, "", decl.Name.Name, decl.Pos(), decl.End())
		return
	}
	receiver := receiverTypeName(decl.Recv.List[0].Type)
	id := MethodSymbolID(pkg.Path, receiver, decl.Name.Name)
	idx.Symbols[id] = symbolFact(p, file, id, "method", pkg.Path, receiver, decl.Name.Name, decl.Pos(), decl.End())
}

func valueKind(tok token.Token) string {
	switch tok {
	case token.CONST:
		return "const"
	case token.VAR:
		return "var"
	default:
		return ""
	}
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.IndexExpr:
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	default:
		return ""
	}
}

func receiverTypeFromValueSpec(spec *ast.ValueSpec) string {
	if spec.Type != nil {
		return receiverTypeName(spec.Type)
	}
	for _, value := range spec.Values {
		if receiver := receiverTypeFromExpr(value); receiver != "" {
			return receiver
		}
	}
	return ""
}

func receiverTypeFromExpr(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.UnaryExpr:
		return receiverTypeFromExpr(x.X)
	case *ast.CompositeLit:
		return receiverTypeName(x.Type)
	case *ast.CallExpr:
		return receiverTypeName(x.Fun)
	default:
		return ""
	}
}

func symbolFact(p *project.Project, file *project.File, id facts.SymbolID, kind, pkgPath, receiver, name string, start, end token.Pos) facts.SymbolFact {
	span := SourceSpanFor(file.FileSet, start, end)
	if rel, err := filepath.Rel(p.Root, span.File); err == nil {
		span.File = filepath.ToSlash(rel)
	}
	return facts.SymbolFact{
		ID:          id,
		Kind:        kind,
		PackagePath: pkgPath,
		Receiver:    receiver,
		Name:        name,
		Span:        span,
	}
}
