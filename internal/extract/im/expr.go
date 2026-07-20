// expr.go 实现静态求值层：把 event/payload 表达式求值为字符串值或类型集合，
// 支持字符串 literal、typed const、字符串拼接、iota + String() 字符串表等模式。
// 无法静态确定的表达式保留为 unresolved，由上层摘要引擎决定如何处理。
package im

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// constDecl 记录一个 const 声明的关键字段，用于在求值时回溯引用链。
type constDecl struct {
	file     *project.File // 该 const 所在文件，跨包 selector 引用时需要切换 file 上下文
	expr     ast.Expr      // const 的初始化表达式
	typeName string        // 该 const 的本地类型名（用于枚举 String() 表查找）
	iota     int64         // 该 const 在声明块中的 iota 值
}

// evaluator 是静态求值器，负责把表达式求值为 event 字符串或类型集合。
// 它在构造时一次性扫描所有 const/var/String() 声明，建立索引供后续求值查询。
type evaluator struct {
	project       *project.Project             // 当前项目
	index         *astindex.Index              // 声明符号索引，用于类型解析
	consts        map[facts.SymbolID]constDecl // 所有 const 声明，按符号 ID 索引
	stringTables  map[facts.SymbolID][]string  // 字符串数组/切片 var，按符号 ID 索引
	stringMethods map[string]facts.SymbolID    // 形如 func (T) String() string 的方法，按 "pkg::T" 索引到字符串表
}

// newEvaluator 构造求值器，并立即扫描项目建立 const、字符串表和 String() 方法的索引。
func newEvaluator(p *project.Project, idx *astindex.Index) *evaluator {
	e := &evaluator{
		project:       p,
		index:         idx,
		consts:        map[facts.SymbolID]constDecl{},
		stringTables:  map[facts.SymbolID][]string{},
		stringMethods: map[string]facts.SymbolID{},
	}
	e.indexDeclarations()
	return e
}

// indexDeclarations 分两轮扫描：先建 const/var 索引，再建 String() 方法索引。
// 两轮分开是因为 String() 方法依赖字符串表（var）已经建好。
func (e *evaluator) indexDeclarations() {
	for _, pkg := range e.project.Packages {
		for _, file := range pkg.Files {
			for _, rawDecl := range file.AST.Decls {
				if decl, ok := rawDecl.(*ast.GenDecl); ok {
					e.indexGenDecl(file, decl)
				}
			}
		}
	}
	for _, pkg := range e.project.Packages {
		for _, file := range pkg.Files {
			for _, rawDecl := range file.AST.Decls {
				if decl, ok := rawDecl.(*ast.FuncDecl); ok {
					e.indexStringMethod(file, decl)
				}
			}
		}
	}
}

// indexGenDecl 处理单个 const/var 声明：const 建立到 constDecl 的索引，
// var 则尝试识别为静态字符串表（用于枚举 String() 查询）。
// const 块中支持 iota、省略 type、省略 RHS 等 Go const 块语法。
func (e *evaluator) indexGenDecl(file *project.File, decl *ast.GenDecl) {
	if decl.Tok == token.CONST {
		var previousValues []ast.Expr
		var previousType ast.Expr
		for i, rawSpec := range decl.Specs {
			spec, ok := rawSpec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			// Go const 块：省略 RHS 时复用上一个 spec 的 RHS。
			values := spec.Values
			if len(values) == 0 {
				values = previousValues
			} else {
				previousValues = values
			}
			// 省略 type 时复用上一个 spec 的 type。
			typeExpr := spec.Type
			if typeExpr == nil {
				typeExpr = previousType
			} else {
				previousType = typeExpr
			}
			for valueIndex, name := range spec.Names {
				if len(values) == 0 {
					continue
				}
				// 多名字共享少量 RHS 时取位置对应的 RHS；名字多于 RHS 时复用最后一个。
				index := valueIndex
				if index >= len(values) {
					index = len(values) - 1
				}
				e.consts[astindex.ValueSymbolID("const", file.Package.Path, name.Name)] = constDecl{
					file:     file,
					expr:     values[index],
					typeName: localTypeName(typeExpr),
					iota:     int64(i),
				}
			}
		}
		return
	}
	if decl.Tok != token.VAR {
		return
	}
	// var 声明：若初始化表达式是纯字符串字面量数组/切片，则登记为字符串表，
	// 供 enumConst 的 String() 方法按索引反查。
	for _, rawSpec := range decl.Specs {
		spec, ok := rawSpec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range spec.Names {
			if len(spec.Values) == 0 {
				continue
			}
			valueIndex := i
			if valueIndex >= len(spec.Values) {
				valueIndex = len(spec.Values) - 1
			}
			table, ok := staticStringTable(spec.Values[valueIndex])
			if !ok {
				continue
			}
			e.stringTables[astindex.ValueSymbolID("var", file.Package.Path, name.Name)] = table
		}
	}
}

// indexStringMethod 识别形如 func (T) String() string { return table[e] } 的方法，
// 并把它绑定到对应字符串表。用于支持 iota + String() 风格的枚举字符串求值。
func (e *evaluator) indexStringMethod(file *project.File, fn *ast.FuncDecl) {
	if fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Name.Name != "String" || fn.Body == nil {
		return
	}
	receiverType := astindex.ReceiverTypeName(fn.Recv.List[0].Type)
	if receiverType == "" {
		return
	}
	// 只识别单条 return table[idx] 形式：table 必须是已登记的字符串数组。
	for _, stmt := range fn.Body.List {
		ret, ok := stmt.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			continue
		}
		indexExpr, ok := ret.Results[0].(*ast.IndexExpr)
		if !ok {
			continue
		}
		table, ok := indexExpr.X.(*ast.Ident)
		if !ok {
			continue
		}
		tableID := astindex.ValueSymbolID("var", file.Package.Path, table.Name)
		if _, ok := e.stringTables[tableID]; !ok {
			continue
		}
		e.stringMethods[typeKey(file.Package.Path, receiverType)] = tableID
	}
}

// eventValue 尝试把 expr 静态求值为 event 字符串。无法确定时返回 ok=false。
// 内部委托给带 seen 防环的版本。
func (e *evaluator) eventValue(file *project.File, expr ast.Expr) (string, bool) {
	return e.eventValueSeen(file, expr, map[facts.SymbolID]bool{})
}

// eventValueSeen 是 eventValue 的递归实现，seen 记录已访问的 const 符号以避免循环。
// 支持：字符串 literal、本地/跨包 const 引用、字符串拼接（+）、类型转换 string(...)、
// 枚举 .String() 方法调用、括号包裹。
func (e *evaluator) eventValueSeen(file *project.File, expr ast.Expr, seen map[facts.SymbolID]bool) (string, bool) {
	switch value := expr.(type) {
	case *ast.BasicLit:
		// 字符串字面量：去掉引号即得值。
		if value.Kind != token.STRING {
			return "", false
		}
		out, err := strconv.Unquote(value.Value)
		return out, err == nil
	case *ast.Ident:
		// 本地 const：递归求值其初始化表达式。
		id := astindex.ValueSymbolID("const", file.Package.Path, value.Name)
		decl, ok := e.consts[id]
		if !ok || seen[id] {
			return "", false
		}
		nextSeen := copySeen(seen)
		nextSeen[id] = true
		return e.eventValueSeen(decl.file, decl.expr, nextSeen)
	case *ast.SelectorExpr:
		// 跨包 const 引用：通过 import alias 解析真实 import path 后查索引。
		pkg, ok := value.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		importPath := file.Imports[pkg.Name]
		if importPath == "" {
			return "", false
		}
		id := astindex.ValueSymbolID("const", importPath, value.Sel.Name)
		decl, ok := e.consts[id]
		if !ok || seen[id] {
			return "", false
		}
		nextSeen := copySeen(seen)
		nextSeen[id] = true
		return e.eventValueSeen(decl.file, decl.expr, nextSeen)
	case *ast.BinaryExpr:
		// 仅支持字符串拼接（+）：两侧分别求值后连接。
		if value.Op != token.ADD {
			return "", false
		}
		left, leftOK := e.eventValueSeen(file, value.X, seen)
		right, rightOK := e.eventValueSeen(file, value.Y, seen)
		if !leftOK || !rightOK {
			return "", false
		}
		return left + right, true
	case *ast.CallExpr:
		// string(x) 或本地具名类型转换 T(x) 视为透传，递归求值唯一实参。
		if len(value.Args) == 1 {
			if ident, ok := value.Fun.(*ast.Ident); ok && (ident.Name == "string" || e.localNamedType(file, ident.Name)) {
				return e.eventValueSeen(file, value.Args[0], seen)
			}
		}
		// 枚举 x.String()：交给 enumStringValue 处理。
		selector, ok := value.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "String" || len(value.Args) != 0 {
			return "", false
		}
		return e.enumStringValue(file, selector.X)
	case *ast.ParenExpr:
		// 括号包裹：递归求值内部表达式。
		return e.eventValueSeen(file, value.X, seen)
	default:
		return "", false
	}
}

// enumStringValue 把 x.String() 求值为具体字符串。
// 流程：解析 x 到 const 声明 -> 把 const 的 iota 表达式求值为整数下标 ->
// 按类型查找 String() 方法绑定的字符串表 -> 按下标返回字符串。
func (e *evaluator) enumStringValue(file *project.File, expr ast.Expr) (string, bool) {
	id, decl, ok := e.enumConst(file, expr)
	if !ok || decl.typeName == "" {
		return "", false
	}
	index, ok := e.integerValue(decl.file, decl.expr, decl.iota, map[facts.SymbolID]bool{id: true})
	if !ok || index < 0 {
		return "", false
	}
	tableID, ok := e.stringMethods[typeKey(decl.file.Package.Path, decl.typeName)]
	if !ok {
		return "", false
	}
	table := e.stringTables[tableID]
	if index >= int64(len(table)) {
		return "", false
	}
	return table[index], true
}

// enumConst 把表达式解析为 const 符号 ID 与对应的 constDecl。
// 支持本地 ident 与跨包 selector 两种形式。
func (e *evaluator) enumConst(file *project.File, expr ast.Expr) (facts.SymbolID, constDecl, bool) {
	switch value := expr.(type) {
	case *ast.Ident:
		id := astindex.ValueSymbolID("const", file.Package.Path, value.Name)
		decl, ok := e.consts[id]
		return id, decl, ok
	case *ast.SelectorExpr:
		pkg, ok := value.X.(*ast.Ident)
		if !ok {
			return "", constDecl{}, false
		}
		importPath := file.Imports[pkg.Name]
		if importPath == "" {
			return "", constDecl{}, false
		}
		id := astindex.ValueSymbolID("const", importPath, value.Sel.Name)
		decl, ok := e.consts[id]
		return id, decl, ok
	default:
		return "", constDecl{}, false
	}
}

// integerValue 把整数表达式静态求值为 int64，用于计算枚举下标。
// 支持 iota、整数 literal、括号包裹、加减乘除和取模。
// 除法和取模会检查除数为 0。seen 用于 const 引用链防环。
func (e *evaluator) integerValue(file *project.File, expr ast.Expr, iotaValue int64, seen map[facts.SymbolID]bool) (int64, bool) {
	switch value := expr.(type) {
	case *ast.Ident:
		// iota 在 const 块中的取值由外层传入。
		if value.Name == "iota" {
			return iotaValue, true
		}
		id := astindex.ValueSymbolID("const", file.Package.Path, value.Name)
		decl, ok := e.consts[id]
		if !ok || seen[id] {
			return 0, false
		}
		nextSeen := copySeen(seen)
		nextSeen[id] = true
		return e.integerValue(decl.file, decl.expr, decl.iota, nextSeen)
	case *ast.BasicLit:
		if value.Kind != token.INT {
			return 0, false
		}
		out, err := strconv.ParseInt(value.Value, 0, 64)
		return out, err == nil
	case *ast.ParenExpr:
		return e.integerValue(file, value.X, iotaValue, seen)
	case *ast.BinaryExpr:
		left, ok := e.integerValue(file, value.X, iotaValue, seen)
		if !ok {
			return 0, false
		}
		right, ok := e.integerValue(file, value.Y, iotaValue, seen)
		if !ok {
			return 0, false
		}
		switch value.Op {
		case token.ADD:
			return left + right, true
		case token.SUB:
			return left - right, true
		case token.MUL:
			return left * right, true
		case token.QUO:
			// 整除：除数为 0 时返回未确定，避免运行时除零。
			if right == 0 {
				return 0, false
			}
			return left / right, true
		case token.REM:
			// 取模：除数为 0 时同样返回未确定。
			if right == 0 {
				return 0, false
			}
			return left % right, true
		default:
			return 0, false
		}
	default:
		return 0, false
	}
}

// expressionTypeIDs 把表达式求值为一组去重、排序后的类型符号 ID。
// 这是 payload 依赖分析的核心入口：payload 的所有可能类型都会成为该 IM 事件的
// payload 依赖，保证同一 sender 发多个 event 时只命中真正使用变更 payload 的 event。
func (e *evaluator) expressionTypeIDs(file *project.File, fn *ast.FuncDecl, expr ast.Expr) []facts.SymbolID {
	types := e.expressionTypes(file, fn, expr)
	ids := make([]facts.SymbolID, 0, len(types))
	seen := map[facts.SymbolID]bool{}
	for _, valueType := range types {
		if valueType.TypeName == "" {
			continue
		}
		id := astindex.TypeSymbolID(valueType.PackagePath, valueType.TypeName)
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// expressionTypes 把表达式求值为 ValueType 集合（去类型 ID 化的中间结果）。
// 支持本地 ident、selector 字段链、解引用、括号、组合字面量、类型转换和泛型 Unmarshal。
func (e *evaluator) expressionTypes(file *project.File, fn *ast.FuncDecl, expr ast.Expr) []astindex.ValueType {
	switch value := expr.(type) {
	case *ast.Ident:
		// 优先解析为函数参数/接收者/返回值，便于追踪传入的 payload 类型。
		if valueType, ok := functionValueType(file, fn, value.Name); ok {
			return []astindex.ValueType{valueType}
		}
	case *ast.SelectorExpr:
		// selector：先求值 base 类型集合，再按结构体字段表查找字段类型。
		parents := e.expressionTypes(file, fn, value.X)
		var out []astindex.ValueType
		for _, parent := range parents {
			fields := e.index.StructFieldTypes[astindex.TypeSymbolID(parent.PackagePath, parent.TypeName)]
			if field, ok := fields[value.Sel.Name]; ok {
				out = append(out, field)
			}
		}
		return out
	case *ast.StarExpr:
		// 解引用：透传 base 的类型集合。
		return e.expressionTypes(file, fn, value.X)
	case *ast.UnaryExpr:
		// 一元（如取地址 &x）：透传 base 的类型集合。
		return e.expressionTypes(file, fn, value.X)
	case *ast.ParenExpr:
		// 括号包裹：透传 base 的类型集合。
		return e.expressionTypes(file, fn, value.X)
	case *ast.CompositeLit:
		// 组合字面量 T{...}：类型来自字面量的类型表达式。
		if valueType, ok := typeExprValueType(file, value.Type); ok {
			return []astindex.ValueType{valueType}
		}
	case *ast.CallExpr:
		// 泛型 jsonx.Unmarshal[T](...)：结果类型来自类型实参 T。
		if valueType, ok := e.genericJSONXResultType(file, value); ok {
			return []astindex.ValueType{valueType}
		}
		// 单实参的显式类型转换 T(x)：结果类型来自被调用表达式 T。
		if len(value.Args) == 1 {
			if valueType, ok := typeExprValueType(file, value.Fun); ok {
				return []astindex.ValueType{valueType}
			}
		}
	}
	return nil
}

// genericJSONXResultType 识别 jsonx.Unmarshal[T](data) 调用并返回类型实参 T 的 ValueType。
// 仅匹配精确 import path gopkg.inshopline.com/sc1/commons/utils/jsonx 和函数名 Unmarshal，
// 单类型实参。
func (e *evaluator) genericJSONXResultType(file *project.File, call *ast.CallExpr) (astindex.ValueType, bool) {
	var callable ast.Expr
	var typeArgs []ast.Expr
	switch fun := call.Fun.(type) {
	case *ast.IndexExpr:
		callable = fun.X
		typeArgs = []ast.Expr{fun.Index}
	case *ast.IndexListExpr:
		callable = fun.X
		typeArgs = fun.Indices
	default:
		return astindex.ValueType{}, false
	}
	if len(typeArgs) != 1 {
		return astindex.ValueType{}, false
	}
	selector, ok := callable.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Unmarshal" {
		return astindex.ValueType{}, false
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok || file.Imports[pkg.Name] != "gopkg.inshopline.com/sc1/commons/utils/jsonx" {
		return astindex.ValueType{}, false
	}
	return typeExprValueType(file, typeArgs[0])
}

// functionValueType 在函数的参数/接收者/返回值中查找名为 name 的字段并返回其 ValueType。
// 用于把函数体内的局部标识符解析回函数签名声明的类型。
func functionValueType(file *project.File, fn *ast.FuncDecl, name string) (astindex.ValueType, bool) {
	if fn == nil {
		return astindex.ValueType{}, false
	}
	for _, fields := range []*ast.FieldList{fn.Recv, fn.Type.Params, fn.Type.Results} {
		if fields == nil {
			continue
		}
		for _, field := range fields.List {
			for _, fieldName := range field.Names {
				if fieldName.Name == name {
					return typeExprValueType(file, field.Type)
				}
			}
		}
	}
	return astindex.ValueType{}, false
}

// typeExprValueType 把类型表达式解析为 ValueType。支持本地 ident、跨包 selector、
// 指针、括号、泛型索引等常见形式。
func typeExprValueType(file *project.File, expr ast.Expr) (astindex.ValueType, bool) {
	vt := astindex.ValueTypeFromTypeExpr(file, expr)
	if vt.TypeName == "" {
		return astindex.ValueType{}, false
	}
	return vt, true
}

// staticStringTable 判断 expr 是否是纯字符串字面量数组/切片，是则返回字符串切片。
// 任一元素不是字符串字面量即视为非静态字符串表。
func staticStringTable(expr ast.Expr) ([]string, bool) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(lit.Elts))
	for _, elt := range lit.Elts {
		value, ok := elt.(*ast.BasicLit)
		if !ok || value.Kind != token.STRING {
			return nil, false
		}
		item, err := strconv.Unquote(value.Value)
		if err != nil {
			return nil, false
		}
		out = append(out, item)
	}
	return out, true
}

// localTypeName 从类型表达式提取本地类型名，仅支持 ident 与解引用形式。
// 用于 const 声明中的类型，供枚举 String() 表查找。
func localTypeName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return localTypeName(value.X)
	default:
		return ""
	}
}

// localNamedType 判断当前包内是否存在名为 name 的 type 声明。
// 用于识别 T(x) 形式的本地类型转换（区别于普通函数调用）。
func (e *evaluator) localNamedType(file *project.File, name string) bool {
	_, ok := e.index.Symbols[astindex.TypeSymbolID(file.Package.Path, name)]
	return ok
}

// typeKey 构造 String() 方法的查找键，由包路径和 receiver 类型名拼接。
func typeKey(packagePath, typeName string) string {
	return filepath.ToSlash(packagePath) + "::" + typeName
}

// copySeen 复制一份已访问集合，递归求值时在新副本上记录，避免污染外层状态。
func copySeen(in map[facts.SymbolID]bool) map[facts.SymbolID]bool {
	out := make(map[facts.SymbolID]bool, len(in)+1)
	for key, value := range in {
		out[key] = value
	}
	return out
}
