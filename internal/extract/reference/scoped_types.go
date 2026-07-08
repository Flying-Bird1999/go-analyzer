// scoped_types.go 实现函数内局部变量的轻量类型推断：从参数、返回值与赋值语句收集
// 局部变量的可能类型，用于解析方法调用的接收者分发（含遮蔽场景）。
package reference

import (
	"go/ast"
	"go/token"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// scopedValueType 记录一个局部变量名在某声明位置推断出的类型集合。
type scopedValueType struct {
	declPos    token.Pos
	valueTypes []astindex.ValueType
}

// scopedValueTypes 维护函数作用域内所有局部变量名的类型推断结果，
// 同时按 ast.Object（精确身份）和按名字（回退匹配）两套索引。
type scopedValueTypes struct {
	// 仅基于 parser 的作用域推断在 parser 提供 ast.Object 时使用它做精确匹配。
	//nolint:staticcheck
	byObject map[*ast.Object][]astindex.ValueType
	byName   map[string][]scopedValueType
}

// addAll 登记一个局部变量的推断类型。同名变量按声明位置追加，支持遮蔽；
// 当 parser 提供 ast.Object 时同时按身份登记以保证精确。
func (s *scopedValueTypes) addAll(name *ast.Ident, declPos token.Pos, valueTypes []astindex.ValueType) {
	if name == nil || name.Name == "" || name.Name == "_" {
		return
	}
	if name.Obj != nil {
		if s.byObject == nil {
			//nolint:staticcheck // 与 byObject 字段保持身份一致。
			s.byObject = map[*ast.Object][]astindex.ValueType{}
		}
		s.byObject[name.Obj] = valueTypes
	}
	if s.byName == nil {
		s.byName = map[string][]scopedValueType{}
	}
	s.byName[name.Name] = append(s.byName[name.Name], scopedValueType{declPos: declPos, valueTypes: valueTypes})
}

// resolve 解析给定位置引用的变量类型；当推断出多个候选时返回 ok 但 ValueType 为零值。
func (s scopedValueTypes) resolve(name *ast.Ident, pos token.Pos) (astindex.ValueType, bool) {
	valueTypes, ok := s.resolveAll(name, pos)
	if !ok {
		return astindex.ValueType{}, false
	}
	if len(valueTypes) != 1 {
		// 多候选无法唯一确定接收者，调用方需自行枚举所有方法候选。
		return astindex.ValueType{}, true
	}
	return valueTypes[0], true
}

// resolveAll 返回变量在 pos 处所有可能命中的类型。
// 优先按 ast.Object 精确匹配；缺失时按名字回退，并使用最近一次声明位置解决遮蔽。
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
		// 跳过在 pos 之后才声明的同名变量（避免前向引用）。
		if candidate.declPos != token.NoPos && candidate.declPos > pos {
			continue
		}
		// 选择声明位置最接近 pos 的候选，以模拟最近作用域遮蔽。
		if found && bestPos != token.NoPos && candidate.declPos <= bestPos {
			continue
		}
		best = candidate.valueTypes
		bestPos = candidate.declPos
		found = true
	}
	return best, found
}

// collectScopedValueTypes 收集函数声明的所有局部变量类型推断：参数、返回值与函数体内的赋值/局部声明。
func collectScopedValueTypes(file *project.File, idx *astindex.Index, fn *ast.FuncDecl) scopedValueTypes {
	out := scopedValueTypes{}
	// 抽取 FieldList（接收者/参数/返回值）中具名变量的类型。
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
			// 仅 := 形式才引入新的局部变量。
			if x.Tok != token.DEFINE {
				return true
			}
			for i, lhs := range x.Lhs {
				name, ok := lhs.(*ast.Ident)
				if !ok || len(x.Rhs) == 0 {
					continue
				}
				// 多返回值赋值仅根据单一右值推断首个左值，其余跳过。
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
					// 无显式类型时回退到初始化值表达式推断。
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

// scopedTypesFromTypeExpr 从类型表达式推断变量类型，支持本包/导入包类型、指针、括号与泛型实参化形式。
func scopedTypesFromTypeExpr(file *project.File, expr ast.Expr) []astindex.ValueType {
	vt := astindex.ValueTypeFromTypeExpr(file, expr)
	if vt.TypeName == "" {
		return nil
	}
	return []astindex.ValueType{vt}
}

// scopedTypesFromValueExpr 从初始化值表达式推断变量类型：取地址、组合字面量、
// 构造函数返回类型与 map 索引等。构造函数推断置信度为 medium。
func scopedTypesFromValueExpr(file *project.File, idx *astindex.Index, expr ast.Expr) []astindex.ValueType {
	switch x := expr.(type) {
	case *ast.UnaryExpr:
		// 主要是 &T{...} 形式：剥去取地址运算。
		return scopedTypesFromValueExpr(file, idx, x.X)
	case *ast.CompositeLit:
		return scopedTypesFromTypeExpr(file, x.Type)
	case *ast.CallExpr:
		// 优先匹配内置 new(T) 的特殊处理。
		if valueType, ok := idx.ResolveBuiltinNewType(file, x); ok {
			return []astindex.ValueType{valueType}
		}
		callableID := scopedCallableID(file, x.Fun)
		valueType, ok := idx.CallableReturnTypes[callableID]
		if !ok {
			return nil
		}
		// 通过返回类型推断属于中等置信度。
		valueType.Confidence = facts.ConfidenceMedium
		return []astindex.ValueType{valueType}
	case *ast.IndexExpr:
		// map[k] 取值：返回 map 元素的候选类型集合（用于接口分发）。
		if valueTypes, ok := idx.ResolveMapIndexValueTypes(file, x); ok {
			return valueTypes
		}
	default:
		return nil
	}
	return nil
}

// scopedCallableID 将被调表达式映射为可查 CallableReturnTypes 的函数符号 ID。
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
