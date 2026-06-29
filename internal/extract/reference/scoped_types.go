package reference

import (
	"go/ast"
	"go/token"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

type scopedValueType struct {
	declPos   token.Pos
	valueType astindex.ValueType
}

type scopedValueTypes map[string][]scopedValueType

func (s scopedValueTypes) add(name string, declPos token.Pos, valueType astindex.ValueType) {
	if name == "" || name == "_" || valueType.TypeName == "" {
		return
	}
	s[name] = append(s[name], scopedValueType{declPos: declPos, valueType: valueType})
}

func (s scopedValueTypes) resolve(name string, pos token.Pos) (astindex.ValueType, bool) {
	var (
		best    astindex.ValueType
		bestPos token.Pos
		found   bool
	)
	for _, candidate := range s[name] {
		if candidate.declPos != token.NoPos && candidate.declPos > pos {
			continue
		}
		if found && bestPos != token.NoPos && candidate.declPos <= bestPos {
			continue
		}
		best = candidate.valueType
		bestPos = candidate.declPos
		found = true
	}
	return best, found
}

func collectScopedValueTypes(file *project.File, idx *astindex.Index, fn *ast.FuncDecl) scopedValueTypes {
	out := scopedValueTypes{}
	addFields := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			valueType := scopedTypeFromTypeExpr(file, field.Type)
			for _, name := range field.Names {
				out.add(name.Name, token.NoPos, valueType)
			}
		}
	}
	addFields(fn.Recv)
	addFields(fn.Type.Params)
	addFields(fn.Type.Results)
	if fn.Body == nil {
		return out
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.AssignStmt:
			if x.Tok != token.DEFINE {
				return true
			}
			for i, lhs := range x.Lhs {
				name, ok := lhs.(*ast.Ident)
				if !ok || len(x.Rhs) == 0 {
					continue
				}
				if len(x.Rhs) == 1 && len(x.Lhs) > 1 && i > 0 {
					continue
				}
				valueIndex := i
				if valueIndex >= len(x.Rhs) {
					valueIndex = len(x.Rhs) - 1
				}
				out.add(name.Name, name.Pos(), scopedTypeFromValueExpr(file, idx, x.Rhs[valueIndex]))
			}
		case *ast.DeclStmt:
			decl, ok := x.Decl.(*ast.GenDecl)
			if !ok || decl.Tok != token.VAR {
				return true
			}
			for _, spec := range decl.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range value.Names {
					valueType := scopedTypeFromTypeExpr(file, value.Type)
					if valueType.TypeName == "" && len(value.Values) > 0 {
						if len(value.Values) == 1 && len(value.Names) > 1 && i > 0 {
							continue
						}
						valueIndex := i
						if valueIndex >= len(value.Values) {
							valueIndex = len(value.Values) - 1
						}
						valueType = scopedTypeFromValueExpr(file, idx, value.Values[valueIndex])
					}
					out.add(name.Name, name.Pos(), valueType)
				}
			}
		}
		return true
	})
	return out
}

func scopedTypeFromTypeExpr(file *project.File, expr ast.Expr) astindex.ValueType {
	switch x := expr.(type) {
	case *ast.Ident:
		return astindex.ValueType{
			PackagePath: file.Package.Path,
			TypeName:    x.Name,
			Confidence:  facts.ConfidenceHigh,
		}
	case *ast.SelectorExpr:
		pkg, ok := x.X.(*ast.Ident)
		if !ok {
			return astindex.ValueType{}
		}
		importPath := file.Imports[pkg.Name]
		if importPath == "" {
			return astindex.ValueType{}
		}
		return astindex.ValueType{
			PackagePath: importPath,
			TypeName:    x.Sel.Name,
			Confidence:  facts.ConfidenceHigh,
		}
	case *ast.StarExpr:
		return scopedTypeFromTypeExpr(file, x.X)
	case *ast.ParenExpr:
		return scopedTypeFromTypeExpr(file, x.X)
	case *ast.IndexExpr:
		return scopedTypeFromTypeExpr(file, x.X)
	case *ast.IndexListExpr:
		return scopedTypeFromTypeExpr(file, x.X)
	default:
		return astindex.ValueType{}
	}
}

func scopedTypeFromValueExpr(file *project.File, idx *astindex.Index, expr ast.Expr) astindex.ValueType {
	switch x := expr.(type) {
	case *ast.UnaryExpr:
		return scopedTypeFromValueExpr(file, idx, x.X)
	case *ast.CompositeLit:
		return scopedTypeFromTypeExpr(file, x.Type)
	case *ast.CallExpr:
		callableID := scopedCallableID(file, x.Fun)
		valueType, ok := idx.CallableReturnTypes[callableID]
		if !ok {
			return astindex.ValueType{}
		}
		valueType.Confidence = facts.ConfidenceMedium
		return valueType
	default:
		return astindex.ValueType{}
	}
}

func scopedCallableID(file *project.File, expr ast.Expr) facts.SymbolID {
	switch x := unwrapGenericCallee(expr).(type) {
	case *ast.Ident:
		return astindex.FunctionSymbolID(file.Package.Path, x.Name)
	case *ast.SelectorExpr:
		pkg, ok := x.X.(*ast.Ident)
		if !ok {
			return ""
		}
		importPath := file.Imports[pkg.Name]
		if importPath == "" {
			return ""
		}
		return astindex.FunctionSymbolID(importPath, x.Sel.Name)
	default:
		return ""
	}
}
