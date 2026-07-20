// interface_bindings.go 实现包级 interface 变量的严格证据绑定。
package astindex

import (
	"go/ast"
	"go/token"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// InterfaceBinding 记录一个声明类型为 interface 的包级 var 的候选具体类型集合。
// 只有当所有赋值证据都能明确解析（Resolved）、且最终只剩唯一具体类型时，才会被
// selector 解析采纳；出现多实现或未知 RHS 时 HasUnknownBinding 置位，绑定被拒绝。
type InterfaceBinding struct {
	// DeclaredType 是该变量声明的 interface 类型。
	DeclaredType ValueType
	// ConcreteTypes 按类型键保存已发现的具体类型候选。
	ConcreteTypes map[string]ValueType
	// HasUnknownBinding 表示出现了无法高置信度解析或多实现的赋值，绑定被拒绝。
	HasUnknownBinding bool
}

// indexInterfaceBindings 建立 interface 变量绑定。
// 先扫描所有声明类型为项目内 interface 的包级 var，建好空 binding，
// 再扫描全部 loader 已加载源码中的直接赋值，按严格证据填充具体类型候选。
func (idx *Index) indexInterfaceBindings() {
	for id, symbol := range idx.Symbols {
		if symbol.Kind != "var" {
			continue
		}
		declaredType, ok := idx.ValueReceiverTypes[string(id)]
		if !ok || !idx.isInterfaceType(declaredType) {
			continue
		}
		idx.InterfaceBindings[id] = &InterfaceBinding{
			DeclaredType:  declaredType,
			ConcreteTypes: map[string]ValueType{},
		}
	}
	for _, pkg := range idx.Project.Packages {
		for _, file := range pkg.Files {
			idx.indexFileInterfaceAssignments(file)
		}
	}
}

// indexFileInterfaceAssignments 处理单个文件中可能出现的接口赋值。
// 包级 var 的初始化表达式和函数体内部对包级 var 的赋值都会作为候选证据。
func (idx *Index) indexFileInterfaceAssignments(file *project.File) {
	for _, decl := range file.AST.Decls {
		switch node := decl.(type) {
		case *ast.GenDecl:
			if node.Tok == token.VAR {
				idx.indexPackageInitializers(file, node)
			}
		case *ast.FuncDecl:
			if node.Body != nil {
				idx.indexAssignmentBlock(file, node.Body)
			}
		}
	}
}

// indexPackageInitializers 收集包级 var 初始化表达式中的赋值证据。
// 多值赋值（如 `var A, B = f(), g()`）按位置对齐，缺少 RHS 时复用最后一个值。
func (idx *Index) indexPackageInitializers(file *project.File, decl *ast.GenDecl) {
	for _, rawSpec := range decl.Specs {
		spec, ok := rawSpec.(*ast.ValueSpec)
		if !ok || len(spec.Values) == 0 {
			continue
		}
		for i, name := range spec.Names {
			id := ValueSymbolID("var", file.Package.Path, name.Name)
			if _, ok := idx.InterfaceBindings[id]; !ok {
				continue
			}
			valueIndex := i
			if valueIndex >= len(spec.Values) {
				valueIndex = len(spec.Values) - 1
			}
			idx.addInterfaceAssignment(file, id, spec.Values[valueIndex])
		}
	}
}

// indexAssignmentBlock 在函数体内递归查找对包级 interface var 的赋值。
// 嵌套 block / 闭包内的赋值也算作候选证据，因为初始化路径常由
// Register/Init/starter 闭包触发；具体类型仍由 addInterfaceAssignment 严格判断。
func (idx *Index) indexAssignmentBlock(file *project.File, block *ast.BlockStmt) {
	ast.Inspect(block, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || assign.Tok != token.ASSIGN || len(assign.Rhs) == 0 {
			return true
		}
		for i, lhs := range assign.Lhs {
			id, ok := idx.packageVariableAssignmentTarget(file, lhs)
			if !ok {
				continue
			}
			if _, ok := idx.InterfaceBindings[id]; !ok {
				continue
			}
			valueIndex := i
			if valueIndex >= len(assign.Rhs) {
				valueIndex = len(assign.Rhs) - 1
			}
			idx.addInterfaceAssignment(file, id, assign.Rhs[valueIndex])
		}
		return true
	})
}

// packageVariableAssignmentTarget 判断一个赋值左值是否指向包级 var。
// 利用 go/parser 建立的 ast.Object 词法对象关系区分包级变量和内层遮蔽变量：
// 同名局部变量退出作用域后不会污染外层包级变量的方法解析。
func (idx *Index) packageVariableAssignmentTarget(file *project.File, expr ast.Expr) (facts.SymbolID, bool) {
	switch target := expr.(type) {
	case *ast.Ident:
		// 优先用 ast.Object 查询词法对象：若该标识符解析到包级 var 的 Obj，
		// 则走精确身份映射；否则再退化为按名查询。
		if target.Obj != nil {
			id, ok := idx.packageValueObjects[target.Obj]
			return id, ok
		}
		id := ValueSymbolID("var", file.Package.Path, target.Name)
		_, ok := idx.Symbols[id]
		return id, ok
	case *ast.SelectorExpr:
		// 跨包赋值 pkg.Var：仅当根标识符解析为包名时才认定为包级 var。
		root, ok := target.X.(*ast.Ident)
		if !ok || (root.Obj != nil && root.Obj.Kind != ast.Pkg) {
			return "", false
		}
		importPath := file.Imports[root.Name]
		if importPath == "" {
			return "", false
		}
		id := ValueSymbolID("var", importPath, target.Sel.Name)
		_, ok = idx.Symbols[id]
		return id, ok
	default:
		return "", false
	}
}

// addInterfaceAssignment 把一条 RHS 表达式作为具体类型证据加入 binding。
// 任何无法明确解析、或 RHS 仍是 interface 类型的赋值都会把 binding 标记为
// HasUnknownBinding，从而拒绝猜测具体方法。
func (idx *Index) addInterfaceAssignment(file *project.File, id facts.SymbolID, expr ast.Expr) {
	ident, isIdent := expr.(*ast.Ident)
	if isIdent && ident.Name == "nil" {
		// 显式 nil 赋值不构成具体类型证据，跳过。
		return
	}
	binding := idx.InterfaceBindings[id]
	var valueType ValueType
	if call, ok := expr.(*ast.CallExpr); ok {
		// 优先识别 new(T) 形式的 builtin 构造，避免误判为外部 constructor。
		valueType, _ = idx.ResolveBuiltinNewType(file, call)
	}
	if valueType.TypeName == "" {
		valueType = valueTypeFromExpr(file, expr)
	}
	if valueType.TypeName == "" || !valueType.Resolved || idx.isInterfaceType(valueType) {
		// 未知 RHS、无法明确解析，或 RHS 仍是接口类型，一律视为不可绑定的证据。
		binding.HasUnknownBinding = true
		return
	}
	binding.ConcreteTypes[valueTypeKey(valueType)] = valueType
}

// resolveUniqueInterfaceBinding 仅当 binding 存在唯一高置信度具体类型候选、
// 且没有任何未知/多实现证据时，返回该具体类型用于 selector 方法解析。
func (idx *Index) resolveUniqueInterfaceBinding(id facts.SymbolID) (ValueType, bool) {
	binding := idx.InterfaceBindings[id]
	if binding == nil || binding.HasUnknownBinding || len(binding.ConcreteTypes) != 1 {
		return ValueType{}, false
	}
	for _, valueType := range binding.ConcreteTypes {
		if valueType.Resolved {
			return valueType, true
		}
	}
	return ValueType{}, false
}

// isInterfaceType 判断某 ValueType 是否为项目内已索引的 interface 类型。
// 项目外的接口不在闭世界模型内，因此不会触发严格绑定逻辑。
func (idx *Index) isInterfaceType(valueType ValueType) bool {
	_, ok := idx.InterfaceTypes[TypeSymbolID(valueType.PackagePath, valueType.TypeName)]
	return ok
}

// valueTypeKey 用 "\x00" 分隔 package path 与类型名，生成具体类型的去重键。
// 分隔符选用 NUL 字节以避免 package path 中合法字符与类型名发生碰撞。
func valueTypeKey(valueType ValueType) string {
	return valueType.PackagePath + "\x00" + valueType.TypeName
}
