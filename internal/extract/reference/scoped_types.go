package reference

import (
	"go/ast"
	"go/token"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

type scopedValueType struct {
	declPos    token.Pos
	valueTypes []astindex.ValueType
}

type scopedValueTypes struct {
	byObject map[*ast.Object][]astindex.ValueType
	byName   map[string][]scopedValueType
}

func (s *scopedValueTypes) addAll(name *ast.Ident, declPos token.Pos, valueTypes []astindex.ValueType) {
	if name == nil || name.Name == "" || name.Name == "_" {
		return
	}
	if name.Obj != nil {
		if s.byObject == nil {
			s.byObject = map[*ast.Object][]astindex.ValueType{}
		}
		s.byObject[name.Obj] = valueTypes
	}
	if s.byName == nil {
		s.byName = map[string][]scopedValueType{}
	}
	s.byName[name.Name] = append(s.byName[name.Name], scopedValueType{declPos: declPos, valueTypes: valueTypes})
}

func (s scopedValueTypes) resolve(name *ast.Ident, pos token.Pos) (astindex.ValueType, bool) {
	valueTypes, ok := s.resolveAll(name, pos)
	if !ok {
		return astindex.ValueType{}, false
	}
	if len(valueTypes) != 1 {
		return astindex.ValueType{}, true
	}
	return valueTypes[0], true
}

func (s scopedValueTypes) resolveAll(name *ast.Ident, pos token.Pos) ([]astindex.ValueType, bool) {
	if name == nil {
		return nil, false
	}
	if name.Obj != nil {
		valueTypes, ok := s.byObject[name.Obj]
		return valueTypes, ok
	}
	var (
		best    []astindex.ValueType
		bestPos token.Pos
		found   bool
	)
	for _, candidate := range s.byName[name.Name] {
		if candidate.declPos != token.NoPos && candidate.declPos > pos {
			continue
		}
		if found && bestPos != token.NoPos && candidate.declPos <= bestPos {
			continue
		}
		best = candidate.valueTypes
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
			valueTypes := scopedTypesFromTypeExpr(file, field.Type)
			for _, name := range field.Names {
				out.addAll(name, token.NoPos, valueTypes)
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
				out.addAll(name, name.Pos(), scopedTypesFromValueExpr(file, idx, x.Rhs[valueIndex]))
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
					valueTypes := scopedTypesFromTypeExpr(file, value.Type)
					if len(valueTypes) == 0 && len(value.Values) > 0 {
						if len(value.Values) == 1 && len(value.Names) > 1 && i > 0 {
							continue
						}
						valueIndex := i
						if valueIndex >= len(value.Values) {
							valueIndex = len(value.Values) - 1
						}
						valueTypes = scopedTypesFromValueExpr(file, idx, value.Values[valueIndex])
					}
					out.addAll(name, name.Pos(), valueTypes)
				}
			}
		}
		return true
	})
	return out
}

func scopedTypesFromTypeExpr(file *project.File, expr ast.Expr) []astindex.ValueType {
	switch x := expr.(type) {
	case *ast.Ident:
		return []astindex.ValueType{{
			PackagePath: file.Package.Path,
			TypeName:    x.Name,
			Confidence:  facts.ConfidenceHigh,
		}}
	case *ast.SelectorExpr:
		pkg, ok := x.X.(*ast.Ident)
		if !ok {
			return nil
		}
		importPath := file.Imports[pkg.Name]
		if importPath == "" {
			return nil
		}
		return []astindex.ValueType{{
			PackagePath: importPath,
			TypeName:    x.Sel.Name,
			Confidence:  facts.ConfidenceHigh,
		}}
	case *ast.StarExpr:
		return scopedTypesFromTypeExpr(file, x.X)
	case *ast.ParenExpr:
		return scopedTypesFromTypeExpr(file, x.X)
	case *ast.IndexExpr:
		return scopedTypesFromTypeExpr(file, x.X)
	case *ast.IndexListExpr:
		return scopedTypesFromTypeExpr(file, x.X)
	default:
		return nil
	}
}

func scopedTypesFromValueExpr(file *project.File, idx *astindex.Index, expr ast.Expr) []astindex.ValueType {
	switch x := expr.(type) {
	case *ast.UnaryExpr:
		return scopedTypesFromValueExpr(file, idx, x.X)
	case *ast.CompositeLit:
		return scopedTypesFromTypeExpr(file, x.Type)
	case *ast.CallExpr:
		if valueType, ok := idx.ResolveBuiltinNewType(file, x); ok {
			return []astindex.ValueType{valueType}
		}
		callableID := scopedCallableID(file, x.Fun)
		valueType, ok := idx.CallableReturnTypes[callableID]
		if !ok {
			return nil
		}
		valueType.Confidence = facts.ConfidenceMedium
		return []astindex.ValueType{valueType}
	case *ast.IndexExpr:
		if valueTypes, ok := idx.ResolveMapIndexValueTypes(file, x); ok {
			return valueTypes
		}
	default:
		return nil
	}
	return nil
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
