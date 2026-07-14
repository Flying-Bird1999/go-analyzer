// index.go 实现声明符号索引与轻量 value-type 推断的核心数据结构与构造入口。
//
// Package astindex 为 Go BFF 项目内的声明建立稳定的符号 ID（function、receiver
// method、type、package-level var/const），并维护 BFF 单例/provider 写法所需的
// 轻量 value-type 与 selector receiver 解析能力，以及严格证据的接口绑定。
//
// 索引在 buildFacts 阶段一次性构建，供 reference、link 等模块在解析 selector、
// handler 表达式、middleware 绑定时复用。它不是 go/types 的替代品，只覆盖项目内、
// 静态可解释的常见 BFF pattern；反射、运行时 DI、多实现动态分发不在当前精度目标内。
package astindex

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// parserObject 仅用于在赋值目标上做词法遮蔽判断。
// go/parser 即便不做类型检查也会填充 ast.Object 关系，足够区分同名局部变量
// 与包级变量，从而避免内层遮蔽变量污染外层包级变量的方法解析。
type parserObject = ast.Object //nolint:staticcheck // go/parser still populates this relation without type checking.

// Index 是整个声明符号与轻量 value-type 的内存索引。
// 它建立 function/method/type/var/const 五类声明，并维护 selector 链解析所需的
// 包级 var/const 静态类型、struct 字段类型、callable 首返回值类型，以及接口变量
// 的严格证据绑定和 map 字面量值类型。
type Index struct {
	// Project 是关联的已加载项目，保留 module path 与全部 packages/files。
	Project *project.Project
	// Symbols 是声明符号 ID -> SymbolFact 的主表，承载全部 function/method/type/var/const。
	Symbols map[facts.SymbolID]facts.SymbolFact
	// ValueReceiverTypes 按 var/const 符号 ID（字符串形式）保存其静态类型，
	// 是 selector 链 `pkg.Var.Method` 解析的起点。
	ValueReceiverTypes map[string]ValueType
	// CallableReturnTypes 按 function/method 符号 ID 保存首个返回值类型，
	// 用于推断 constructor 返回值类型，从而解析 constructor 注入的局部变量方法。
	CallableReturnTypes map[facts.SymbolID]ValueType
	// StructFieldTypes 按 type 符号 ID 保存 struct 字段名 -> 字段静态类型，
	// 用于解析 `pkg.Var.Field.Method` 形式的多层 selector 链。
	StructFieldTypes map[facts.SymbolID]map[string]ValueType
	// InterfaceTypes 记录项目内所有 interface 类型，供严格绑定判断闭世界。
	InterfaceTypes map[facts.SymbolID]struct{}
	// InterfaceBindings 保存声明类型为项目内 interface 的包级 var 的赋值证据集合。
	InterfaceBindings map[facts.SymbolID]*InterfaceBinding
	// MapValueTypes 按包级 var 符号 ID 保存静态 map 字面量的具体值类型候选集合，
	// 用于解析 `actionMap[key].Method(...)` 形式的接口分发。
	MapValueTypes map[facts.SymbolID][]ValueType
	// packageValueObjects 借助 ast.Object 词法对象身份把包级 var 标识符映射到符号 ID，
	// 用于在赋值目标上区分包级变量与同名局部变量。
	packageValueObjects map[*parserObject]facts.SymbolID
}

// ValueType 表示一个轻量静态类型：包路径 + 类型名 + 置信度。
// 它不携带泛型实参或完整类型系统信息，只够拼出 method symbol 的 receiver。
type ValueType struct {
	// PackagePath 是类型所在的导入路径；项目内类型为本包路径，外部类型为 import path。
	PackagePath string
	// TypeName 是剥离指针/泛型包装后的基础类型名。
	TypeName string
	// Confidence 表示该类型的静态证据强度，沿 selector 链向下传递。
	Confidence facts.Confidence
}

// ResolvedSymbol 是 selector 方法解析的返回结果，携带解析到的符号 ID 与累积置信度。
type ResolvedSymbol struct {
	ID         facts.SymbolID
	Confidence facts.Confidence
}

// IsProjectPackage 判断 packagePath 是否落在当前项目 module 下。
func (idx *Index) IsProjectPackage(packagePath string) bool {
	if idx == nil || idx.Project == nil || idx.Project.ModulePath == "" || packagePath == "" {
		return false
	}
	if _, ok := idx.Project.Packages[packagePath]; ok {
		return true
	}
	modulePath := idx.Project.ModulePath
	return packagePath == modulePath || strings.HasPrefix(packagePath, modulePath+"/")
}

// Build 遍历项目全部声明构建 Index。
// 第一轮建立声明主表、struct 字段与 callable 返回类型；随后三步分别补全
// 包级 var/const 的 value-type、map 字面量值类型，以及接口变量的严格绑定。
func Build(p *project.Project) (*Index, error) {
	idx := &Index{
		Project:             p,
		Symbols:             map[facts.SymbolID]facts.SymbolFact{},
		ValueReceiverTypes:  map[string]ValueType{},
		CallableReturnTypes: map[facts.SymbolID]ValueType{},
		StructFieldTypes:    map[facts.SymbolID]map[string]ValueType{},
		InterfaceTypes:      map[facts.SymbolID]struct{}{},
		InterfaceBindings:   map[facts.SymbolID]*InterfaceBinding{},
		MapValueTypes:       map[facts.SymbolID][]ValueType{},
		packageValueObjects: map[*parserObject]facts.SymbolID{},
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
	idx.indexValueReceiverTypes()
	idx.indexMapValueTypes()
	idx.indexInterfaceBindings()
	return idx, nil
}

// indexGenDecl 索引一条通用声明（type/var/const/import 等）。
// 仅处理 TypeSpec 与 ValueSpec 两类：type 建符号并尝试索引 struct 字段，
// var/const 建符号并登记 ast.Object 身份，供后续赋值目标遮蔽判断使用。
func (idx *Index) indexGenDecl(p *project.Project, pkg *project.Package, file *project.File, decl *ast.GenDecl) {
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			id := TypeSymbolID(pkg.Path, s.Name.Name)
			idx.Symbols[id] = symbolFact(p, file, id, "type", pkg.Path, "", s.Name.Name, s.Pos(), s.End())
			if _, ok := s.Type.(*ast.InterfaceType); ok {
				// 记录 interface 类型，接口变量绑定只在项目内 interface 上启用。
				idx.InterfaceTypes[id] = struct{}{}
			}
			idx.indexStructFields(file, id, s)
		case *ast.ValueSpec:
			kind := valueKind(decl.Tok)
			if kind == "" {
				continue
			}
			for _, name := range s.Names {
				id := ValueSymbolID(kind, pkg.Path, name.Name)
				idx.Symbols[id] = symbolFact(p, file, id, kind, pkg.Path, "", name.Name, s.Pos(), s.End())
				if name.Obj != nil {
					// 记录词法对象身份，便于后续按 ast.Object 精确区分同名变量。
					idx.packageValueObjects[name.Obj] = id
				}
			}
		}
	}
}

// indexValueReceiverTypes 第二轮扫描，为每个包级 var/const 计算静态类型。
// 类型来源包括显式类型标注、组合字面量、new(T)、项目内 constructor 返回值以及
// 外部 constructor 的 selector 名；这些信息是 selector 链解析的起点。
func (idx *Index) indexValueReceiverTypes() {
	for _, pkg := range idx.Project.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				genDecl, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				kind := valueKind(genDecl.Tok)
				if kind == "" {
					continue
				}
				for _, rawSpec := range genDecl.Specs {
					spec, ok := rawSpec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range spec.Names {
						id := ValueSymbolID(kind, pkg.Path, name.Name)
						if valueType := idx.valueTypeFromValueSpec(file, spec, i); valueType.TypeName != "" {
							idx.ValueReceiverTypes[string(id)] = valueType
						}
					}
				}
			}
		}
	}
}

// indexMapValueTypes 第三轮扫描，专门处理声明为 map[K]I（I 为项目内 interface）
// 且用字面量初始化的包级 var。把每个 key 的具体值类型收集成候选集合，
// 供 ResolveMapIndexValueTypes 解析 `m[key].Method(...)` 形式的接口分发。
func (idx *Index) indexMapValueTypes() {
	for _, pkg := range idx.Project.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				genDecl, ok := decl.(*ast.GenDecl)
				if !ok || genDecl.Tok != token.VAR {
					continue
				}
				for _, rawSpec := range genDecl.Specs {
					spec, ok := rawSpec.(*ast.ValueSpec)
					if !ok || len(spec.Values) == 0 {
						continue
					}
					for i, name := range spec.Names {
						valueIndex := i
						if valueIndex >= len(spec.Values) {
							valueIndex = len(spec.Values) - 1
						}
						valueTypes, ok := idx.staticMapConcreteValueTypes(file, spec.Values[valueIndex])
						if !ok {
							continue
						}
						idx.MapValueTypes[ValueSymbolID("var", pkg.Path, name.Name)] = valueTypes
					}
				}
			}
		}
	}
}

// staticMapConcreteValueTypes 判断一个字面量表达式是否为 map[K]I（I 为项目内 interface），
// 并收集全部 key 的具体值类型。要求所有 key 都高置信度解析为同一个具体类型的候选集合：
// 任一 key 无法解析、低置信度或仍是接口都会拒绝整个 map，避免动态 map 误绑定。
func (idx *Index) staticMapConcreteValueTypes(file *project.File, expr ast.Expr) ([]ValueType, bool) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	mapType, ok := lit.Type.(*ast.MapType)
	if !ok {
		return nil, false
	}
	declaredValueType := ValueTypeFromTypeExpr(file, mapType.Value)
	if declaredValueType.TypeName == "" || !idx.isInterfaceType(declaredValueType) {
		return nil, false
	}
	byKey := map[string]ValueType{}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return nil, false
		}
		valueType := idx.concreteValueTypeFromExpr(file, kv.Value)
		if valueType.TypeName == "" || valueType.Confidence != facts.ConfidenceHigh || idx.isInterfaceType(valueType) {
			return nil, false
		}
		byKey[valueTypeKey(valueType)] = valueType
	}
	if len(byKey) == 0 {
		return nil, false
	}
	// 按类型键排序后输出，保证索引结果稳定，避免遍历 map 的非确定性影响下游输出。
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]ValueType, 0, len(keys))
	for _, key := range keys {
		out = append(out, byKey[key])
	}
	return out, true
}

// concreteValueTypeFromExpr 把一个表达式解析为具体 ValueType。
// 优先识别 new(T) 形式的 builtin 构造，再退化到组合字面量与取址表达式。
func (idx *Index) concreteValueTypeFromExpr(file *project.File, expr ast.Expr) ValueType {
	if call, ok := expr.(*ast.CallExpr); ok {
		if valueType, ok := idx.ResolveBuiltinNewType(file, call); ok {
			return valueType
		}
	}
	return valueTypeFromExpr(file, expr)
}

// indexStructFields 索引 struct 类型的字段类型，用于解析多层 selector 链。
// 嵌入字段没有显式名字，按类型名作为键登记，与 ReceiverTypeName 处理一致。
func (idx *Index) indexStructFields(file *project.File, id facts.SymbolID, spec *ast.TypeSpec) {
	structType, ok := spec.Type.(*ast.StructType)
	if !ok {
		return
	}
	fields := map[string]ValueType{}
	for _, field := range structType.Fields.List {
		valueType := ValueTypeFromTypeExpr(file, field.Type)
		if valueType.TypeName == "" {
			continue
		}
		if len(field.Names) == 0 {
			// 嵌入字段：用类型名作键，便于 selector 链直接按类型名穿透。
			fields[valueType.TypeName] = valueType
			continue
		}
		for _, name := range field.Names {
			fields[name.Name] = valueType
		}
	}
	if len(fields) > 0 {
		idx.StructFieldTypes[id] = fields
	}
}

// indexFuncDecl 索引函数或方法声明，并补充首个返回值类型。
// 没有 receiver 视作 package-level function；有 receiver 则用 ReceiverTypeName
// 剥离指针/泛型包装得到基础类型名，再拼出 method 符号 ID。
func (idx *Index) indexFuncDecl(p *project.Project, pkg *project.Package, file *project.File, decl *ast.FuncDecl) {
	if decl.Recv == nil || len(decl.Recv.List) == 0 {
		id := FunctionSymbolID(pkg.Path, decl.Name.Name)
		idx.Symbols[id] = symbolFact(p, file, id, "func", pkg.Path, "", decl.Name.Name, decl.Pos(), decl.End())
		idx.indexCallableReturnType(file, id, decl)
		return
	}
	receiver := ReceiverTypeName(decl.Recv.List[0].Type)
	id := MethodSymbolID(pkg.Path, receiver, decl.Name.Name)
	idx.Symbols[id] = symbolFact(p, file, id, "method", pkg.Path, receiver, decl.Name.Name, decl.Pos(), decl.End())
	idx.indexCallableReturnType(file, id, decl)
}

// indexCallableReturnType 记录 callable 首个返回值的静态类型。
// 当声明返回的是项目内 interface 时，尝试用函数体所有 return 语句收窄到唯一具体类型：
// 这是 BFF 中 constructor `func NewX() I { return &impl{} }` 写法的核心推断。
func (idx *Index) indexCallableReturnType(file *project.File, id facts.SymbolID, decl *ast.FuncDecl) {
	if decl.Type.Results == nil || len(decl.Type.Results.List) == 0 {
		return
	}
	valueType := ValueTypeFromTypeExpr(file, decl.Type.Results.List[0].Type)
	if valueType.TypeName != "" && idx.isInterfaceType(valueType) {
		if concrete, ok := idx.singleConcreteReturnType(file, decl); ok {
			valueType = concrete
		}
	}
	if valueType.TypeName != "" {
		idx.CallableReturnTypes[id] = valueType
	}
}

// singleConcreteReturnType 在函数体内查找所有 return 语句，判断首返回值是否
// 全部高置信度解析为同一个项目内具体类型。一旦出现 return 无值、低置信度、
// 接口类型或多实现，立即放弃收窄，避免把接口 constructor 误绑死。
func (idx *Index) singleConcreteReturnType(file *project.File, decl *ast.FuncDecl) (ValueType, bool) {
	if decl.Body == nil {
		return ValueType{}, false
	}
	var out ValueType
	found := false
	unknown := false
	ast.Inspect(decl.Body, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.FuncLit:
			// 闭包内的 return 不属于当前函数的返回，跳过整棵闭包子树。
			return false
		case *ast.ReturnStmt:
			if len(x.Results) == 0 {
				unknown = true
				return false
			}
			valueType := idx.concreteValueTypeFromExpr(file, x.Results[0])
			if valueType.TypeName == "" || valueType.Confidence != facts.ConfidenceHigh || idx.isInterfaceType(valueType) {
				unknown = true
				return false
			}
			if found && valueTypeKey(out) != valueTypeKey(valueType) {
				// 出现多个不同具体类型，放弃收窄。
				unknown = true
				return false
			}
			out = valueType
			found = true
			return false
		default:
			// 已经判定无法收窄时立即停止下探，提前剪枝。
			return !unknown
		}
	})
	if unknown || !found {
		return ValueType{}, false
	}
	return out, true
}

// valueKind 把声明 token 映射为 var/const 字符串，其他声明返回空串。
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

// valueTypeFromValueSpec 推断一条 var/const 声明中第 index 个名字的静态类型。
// 优先级：显式类型标注 -> new(T) -> 项目内 constructor 首返回值（中等置信度）
// -> 外部 constructor selector 名（中等置信度）-> 组合字面量/取址表达式。
func (idx *Index) valueTypeFromValueSpec(file *project.File, spec *ast.ValueSpec, index int) ValueType {
	if spec.Type != nil {
		return ValueTypeFromTypeExpr(file, spec.Type)
	}
	if len(spec.Values) == 0 {
		return ValueType{}
	}
	valueIndex := index
	if valueIndex >= len(spec.Values) {
		valueIndex = 0
	}
	if call, ok := spec.Values[valueIndex].(*ast.CallExpr); ok {
		if valueType, ok := idx.ResolveBuiltinNewType(file, call); ok {
			return valueType
		}
		if valueType, ok := idx.callableReturnType(file, call.Fun); ok {
			// 项目内 constructor 注入：返回值类型本身可信，但作为 selector 接收者来源
			// 标记为中等置信度，使下游传播节点置信度能反映这一推断层级。
			valueType.Confidence = facts.ConfidenceMedium
			return valueType
		}
		if valueType, ok := idx.externalCallableReceiver(file, call.Fun); ok {
			return valueType
		}
	}
	return valueTypeFromExpr(file, spec.Values[valueIndex])
}

// externalCallableReceiver 处理外部包 constructor 的 selector 形式 pkg.Name()。
// 项目内 constructor 走 callableReturnType 用真实返回值类型；外部 constructor
// 只用于确认依赖边界，因此按 selector 名推测 receiver 类型，并标记中等置信度。
func (idx *Index) externalCallableReceiver(file *project.File, expr ast.Expr) (ValueType, bool) {
	callable, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ValueType{}, false
	}
	pkg, ok := callable.X.(*ast.Ident)
	if !ok {
		return ValueType{}, false
	}
	importPath := file.Imports[pkg.Name]
	if importPath == "" || idx.IsProjectPackage(importPath) {
		// 项目内包不走外部 constructor 推测分支。
		return ValueType{}, false
	}
	return ValueType{
		PackagePath: importPath,
		TypeName:    callable.Sel.Name,
		Confidence:  facts.ConfidenceMedium,
	}, true
}

// valueTypeFromExpr 从初始化表达式（组合字面量或取址）推断静态类型。
// 仅处理 &T{}、T{} 这类可直接得到类型名的形式，其他表达式返回空 ValueType。
func valueTypeFromExpr(file *project.File, expr ast.Expr) ValueType {
	switch x := expr.(type) {
	case *ast.UnaryExpr:
		// 处理 &T{}：剥掉取址运算符后继续判断被操作数。
		return valueTypeFromExpr(file, x.X)
	case *ast.CompositeLit:
		return ValueTypeFromTypeExpr(file, x.Type)
	default:
		return ValueType{}
	}
}

// callableReturnType 解析 call 表达式中被调用 function 的首返回值类型。
// 支持 ident、跨包 selector，以及泛型实例化形式（剥离类型实参后取基础 function）。
func (idx *Index) callableReturnType(file *project.File, expr ast.Expr) (ValueType, bool) {
	var id facts.SymbolID
	switch callable := expr.(type) {
	case *ast.Ident:
		id = FunctionSymbolID(file.Package.Path, callable.Name)
	case *ast.SelectorExpr:
		pkg, ok := callable.X.(*ast.Ident)
		if !ok {
			return ValueType{}, false
		}
		importPath := file.Imports[pkg.Name]
		if importPath == "" {
			return ValueType{}, false
		}
		id = FunctionSymbolID(importPath, callable.Sel.Name)
	case *ast.IndexExpr:
		return idx.callableReturnType(file, callable.X)
	case *ast.IndexListExpr:
		return idx.callableReturnType(file, callable.X)
	default:
		return ValueType{}, false
	}
	valueType, ok := idx.CallableReturnTypes[id]
	return valueType, ok
}

// ResolveBuiltinNewType 仅当 new 是未被遮蔽的内建函数时，把 new(T) 解析为 T。
// 通过两点保证严格性：ident.Obj 必须为 nil（标识符未绑定到项目内声明），
// 且四种声明空间里都不存在同名的 func/var/const/type，防止把项目内的 new 函数
// 误判为内建 new。
func (idx *Index) ResolveBuiltinNewType(file *project.File, call *ast.CallExpr) (ValueType, bool) {
	ident, ok := call.Fun.(*ast.Ident)
	if !ok || ident.Name != "new" || ident.Obj != nil || len(call.Args) != 1 {
		return ValueType{}, false
	}
	for _, kind := range []string{"func", "var", "const", "type"} {
		var id facts.SymbolID
		switch kind {
		case "func":
			id = FunctionSymbolID(file.Package.Path, ident.Name)
		case "type":
			id = TypeSymbolID(file.Package.Path, ident.Name)
		default:
			id = ValueSymbolID(kind, file.Package.Path, ident.Name)
		}
		if _, exists := idx.Symbols[id]; exists {
			// 同名声明存在，说明 new 可能被遮蔽，按非内建处理。
			return ValueType{}, false
		}
	}
	valueType := ValueTypeFromTypeExpr(file, call.Args[0])
	return valueType, valueType.TypeName != ""
}

// ValueTypeFromTypeExpr 把类型表达式归一化为 ValueType。
// 项目内 ident 视为本包类型；selector 形式按 import 解析跨包类型；
// 指针、括号、单/多类型参数泛型实例化一律剥离包装取基础类型名。
func ValueTypeFromTypeExpr(file *project.File, expr ast.Expr) ValueType {
	switch x := expr.(type) {
	case *ast.Ident:
		return ValueType{PackagePath: file.Package.Path, TypeName: x.Name, Confidence: facts.ConfidenceHigh}
	case *ast.SelectorExpr:
		pkg, ok := x.X.(*ast.Ident)
		if !ok {
			return ValueType{}
		}
		importPath := file.Imports[pkg.Name]
		if importPath == "" {
			return ValueType{}
		}
		return ValueType{PackagePath: importPath, TypeName: x.Sel.Name, Confidence: facts.ConfidenceHigh}
	case *ast.StarExpr:
		// 指针类型 *T：剥掉指针取基础类型。
		return ValueTypeFromTypeExpr(file, x.X)
	case *ast.ParenExpr:
		// 括号包裹 (T)：剥掉括号。
		return ValueTypeFromTypeExpr(file, x.X)
	case *ast.IndexExpr:
		// 泛型实例化 T[A]：剥掉类型实参。
		return ValueTypeFromTypeExpr(file, x.X)
	case *ast.IndexListExpr:
		// 多类型参数实例化 T[A, B]：剥掉全部实参。
		return ValueTypeFromTypeExpr(file, x.X)
	default:
		return ValueType{}
	}
}

// ResolveSelectorMethod 解析 selector 方法链，返回方法符号 ID。
// 内部委托给带置信度版本并丢弃置信度信息。
func (idx *Index) ResolveSelectorMethod(file *project.File, parts []string) (facts.SymbolID, bool) {
	resolved, ok := idx.ResolveSelectorMethodWithConfidence(file, parts)
	return resolved.ID, ok
}

// ResolveSelectorMethodWithConfidence 解析形如 pkg.Var.Field.Method 的链，
// 返回最终方法符号 ID 及沿链累积的置信度。链的最后一段是方法名，前面段通过
// ResolveSelectorReceiverType 解析到方法所属 receiver 类型。
func (idx *Index) ResolveSelectorMethodWithConfidence(file *project.File, parts []string) (ResolvedSymbol, bool) {
	valueType, ok := idx.ResolveSelectorReceiverType(file, parts)
	if !ok {
		return ResolvedSymbol{}, false
	}
	methodID := MethodSymbolID(valueType.PackagePath, valueType.TypeName, parts[len(parts)-1])
	_, ok = idx.Symbols[methodID]
	return ResolvedSymbol{ID: methodID, Confidence: valueType.Confidence}, ok
}

// ResolveSelectorReceiverType 返回 selector 链最终方法所属的 receiver 类型。
// 链的首段可能是 import 包名（跨包 selector）或当前包名（同包 selector）；
// 起点变量按 var/const 两种符号空间查询，对 var 还会尝试唯一接口绑定收窄。
// 随后按 selectors 中间段沿 struct 字段链一路走到 receiver 类型。
func (idx *Index) ResolveSelectorReceiverType(file *project.File, parts []string) (ValueType, bool) {
	if file == nil || len(parts) < 2 {
		return ValueType{}, false
	}
	var packagePath, varName string
	var selectors []string
	if importPath := file.Imports[parts[0]]; importPath != "" {
		// 跨包 selector：首段是 import 别名，至少需要 pkg.var.name 三段。
		if len(parts) < 3 {
			return ValueType{}, false
		}
		packagePath = importPath
		varName = parts[1]
		selectors = parts[2:]
	} else {
		// 同包 selector：首段是本包 var/const，方法名从第二段开始。
		packagePath = file.Package.Path
		varName = parts[0]
		selectors = parts[1:]
	}
	if len(selectors) == 0 {
		return ValueType{}, false
	}
	var valueType ValueType
	for _, kind := range []string{"var", "const"} {
		valueID := ValueSymbolID(kind, packagePath, varName)
		if candidate, ok := idx.ValueReceiverTypes[string(valueID)]; ok {
			if kind == "var" {
				// 接口变量仅在严格证据下收窄到唯一具体类型。
				if concrete, ok := idx.resolveUniqueInterfaceBinding(valueID); ok {
					candidate = concrete
				}
			}
			valueType = candidate
			break
		}
	}
	if valueType.TypeName == "" {
		return ValueType{}, false
	}
	// 沿 struct 字段链走完除最后一段（方法名）之外的所有 selector。
	for _, fieldName := range selectors[:len(selectors)-1] {
		typeID := TypeSymbolID(valueType.PackagePath, valueType.TypeName)
		fields := idx.StructFieldTypes[typeID]
		nextType, ok := fields[fieldName]
		if !ok {
			return ValueType{}, false
		}
		// 置信度沿链向下传递：任一环为低/中，整条链相应降级。
		nextType.Confidence = combineConfidence(valueType.Confidence, nextType.Confidence)
		valueType = nextType
	}
	return valueType, true
}

// ResolveValueTypeMethod 从一个已知的 ValueType 起点解析方法。
// 与 ResolveSelectorReceiverType 的区别：起点不是 selector 链中的包级变量，
// 而是调用方已通过 receiver / 显式类型局部变量 / new(T) 等手段确定的 ValueType。
func (idx *Index) ResolveValueTypeMethod(valueType ValueType, selectors []string) (ResolvedSymbol, bool) {
	if valueType.TypeName == "" || len(selectors) == 0 {
		return ResolvedSymbol{}, false
	}
	confidence := valueType.Confidence
	for _, fieldName := range selectors[:len(selectors)-1] {
		typeID := TypeSymbolID(valueType.PackagePath, valueType.TypeName)
		fields := idx.StructFieldTypes[typeID]
		nextType, ok := fields[fieldName]
		if !ok {
			return ResolvedSymbol{}, false
		}
		valueType = nextType
		confidence = combineConfidence(confidence, valueType.Confidence)
	}
	methodID := MethodSymbolID(valueType.PackagePath, valueType.TypeName, selectors[len(selectors)-1])
	_, ok := idx.Symbols[methodID]
	return ResolvedSymbol{ID: methodID, Confidence: confidence}, ok
}

// ResolveMapIndexValueTypes 解析 `m[key].Method(...)` 中 m 的具体值类型集合。
// 仅支持 m 是本包 var 或跨包 var 的两层 selector；其他深度或非 var 起点不处理。
// 返回的候选集合由调用方（reference resolver）继续按方法符号命中筛选。
func (idx *Index) ResolveMapIndexValueTypes(file *project.File, expr *ast.IndexExpr) ([]ValueType, bool) {
	if file == nil || expr == nil {
		return nil, false
	}
	parts := SelectorParts(expr.X)
	if len(parts) == 0 {
		return nil, false
	}
	var id facts.SymbolID
	if len(parts) == 1 {
		id = ValueSymbolID("var", file.Package.Path, parts[0])
	} else if len(parts) == 2 {
		importPath := file.Imports[parts[0]]
		if importPath == "" {
			return nil, false
		}
		id = ValueSymbolID("var", importPath, parts[1])
	} else {
		return nil, false
	}
	valueTypes := idx.MapValueTypes[id]
	if len(valueTypes) == 0 {
		return nil, false
	}
	// 复制切片返回，避免调用方修改索引内部的候选集合。
	return append([]ValueType(nil), valueTypes...), true
}

// PackageValueSymbol 通过 ast.Object 词法对象身份查询包级 var/const 的符号 ID。
// 用于在赋值目标、selector 起点等位置精确区分同名局部变量与包级变量，
// 避免内层 block 的遮蔽变量污染外层包级变量的方法解析。
//
//nolint:staticcheck // Parser-only indexing intentionally uses ast.Object for local package value identity.
func (idx *Index) PackageValueSymbol(object *ast.Object) (facts.SymbolID, bool) {
	if object == nil {
		return "", false
	}
	id, ok := idx.packageValueObjects[object]
	return id, ok
}

// SelectorParts 把 selector 表达式展平为字符串切片。
// 例如 pkg.Var.Field 返回 ["pkg", "Var", "Field"]，非 selector 返回 nil。
// 导出供 route/reference/link 等包复用，避免逐字节重复的平行实现。
func SelectorParts(expr ast.Expr) []string {
	switch x := expr.(type) {
	case *ast.Ident:
		return []string{x.Name}
	case *ast.SelectorExpr:
		return append(SelectorParts(x.X), x.Sel.Name)
	default:
		return nil
	}
}

// combineConfidence 沿 selector 链合并两段置信度。
// 任一端为 low 整体 low；任一端为 medium 整体 medium；都为 high 才是 high；
// 空串视为“未设置”，不影响另一端。
func combineConfidence(left, right facts.Confidence) facts.Confidence {
	if left == facts.ConfidenceLow || right == facts.ConfidenceLow {
		return facts.ConfidenceLow
	}
	if left == facts.ConfidenceMedium || right == facts.ConfidenceMedium {
		return facts.ConfidenceMedium
	}
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	return facts.ConfidenceHigh
}

// symbolFact 构造一条 SymbolFact，并把源码文件绝对路径转为项目相对路径，
// 保证 facts 输出在任意工作目录下都稳定。
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
