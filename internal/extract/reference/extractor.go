// extractor.go 实现 reference 包的入口：遍历项目 AST，调用三类提取器写出 call/type/value 引用边。
//
// Package reference 在 buildFacts 阶段提取 Go 源码中的符号引用，生成三类依赖边：
// call（调用）、type（类型引用）、value（值引用）。每条边的方向为 FromSymbol 依赖 ToSymbol。
// selector 解析、接口绑定诊断以及 value/method 候选解析由 resolver 承载；
// 类型与目标符号的定位依赖 astindex 提供的符号表与解析能力。
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

// Extract 遍历项目所有包/文件的顶层声明，分发到函数声明与通用声明（type/var/const）的引用提取器。
// 它是 RunFacts 与 RunImpact 在 buildFacts 阶段都会执行的统一入口。
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

// extractFuncReferences 提取单个函数/方法声明的引用边：FromSymbol 为该函数/方法。
// 依次处理接收者、类型参数、参数、返回值的类型引用，再进入函数体提取 value 与 call 边。
func extractFuncReferences(p *project.Project, file *project.File, idx *astindex.Index, store *facts.Store, pkgPath string, fn *ast.FuncDecl) {
	from := functionSymbol(pkgPath, fn)
	// 预先收集函数体上下文：局部变量推断类型、忽略位置和调用函数位置。
	bodyContext := collectFunctionBodyContext(file, idx, fn)
	if fn.Recv != nil {
		// 方法的接收者类型本身算作 type 引用。
		for _, field := range fn.Recv.List {
			addTypeReferences(p, file, idx, store, from, field.Type)
		}
	}
	if fn.Type.TypeParams != nil {
		// 泛型类型参数也构成 type 引用。
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
		// 外部声明或仅声明无体的函数没有函数体可分析。
		return
	}
	extractFunctionBodyReferences(p, file, idx, store, from, fn, bodyContext)
}

// extractFunctionBodyReferences 用一次函数体遍历同时提取 call/type/value 引用。
func extractFunctionBodyReferences(p *project.Project, file *project.File, idx *astindex.Index, store *facts.Store, from facts.SymbolID, fn *ast.FuncDecl, ctx functionBodyContext) {
	if fn.Body == nil {
		return
	}
	resolver := newResolver(file, idx, scopedValueTypes{})
	var visit func(node ast.Node) bool
	visit = func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.CallExpr:
			// 先把泛型调用的显式类型实参当作 type 引用处理。
			for _, typeArgument := range genericTypeArguments(x.Fun) {
				addTypeReferences(p, file, idx, store, from, typeArgument)
			}
			callee := unwrapGenericCallee(x.Fun)
			// 剥去泛型实参后，若被调表达式整体能解析为类型，则视为类型转换而非调用。
			if len(collectTypeIDs(file, idx, callee)) > 0 {
				addTypeReferences(p, file, idx, store, from, callee)
			} else {
				addCallReference(p, file, idx, store, from, ctx.scopedTypes, x)
			}
		case *ast.CompositeLit:
			// 组合字面量的类型部分构成 type 引用。
			addTypeReferences(p, file, idx, store, from, x.Type)
		case *ast.SelectorExpr:
			if ctx.ignored[x.Pos()] {
				return false
			}
			var targets []facts.SymbolID
			if ctx.callFuns[x.Pos()] {
				targets = resolver.ResolveReceiverValueIDs(x)
			} else {
				targets = resolver.ResolveValueIDs(x)
			}
			addValueReferenceFacts(p, file, store, from, x, targets)
			// 选择器整体解析完毕，不再下钻以避免重复解析根 Ident；但接收者
			// 表达式内若含调用（如链式 Helper(g).GET(...)），必须单独遍历该
			// 子树，否则接收者位置的调用引用会被整体剪枝而漏掉。
			if containsCallExpr(x.X) {
				ast.Inspect(x.X, visit)
			}
			return false
		case *ast.Ident:
			// 跳过被忽略位置、调用函数位置以及局部变量。
			if ctx.ignored[x.Pos()] || ctx.callFuns[x.Pos()] || isLocalIdentifier(idx, x) {
				return true
			}
			addValueReferenceFacts(p, file, store, from, x, resolver.ResolveValueIDs(x))
		}
		return true
	}
	ast.Inspect(fn.Body, visit)
}

// extractGenDeclTypeReferences 处理通用声明：type 声明的 FromSymbol 为该类型，
// var/const 声明的 FromSymbol 为对应 value 符号，并递归提取初始化表达式中的引用。
func extractGenDeclTypeReferences(p *project.Project, file *project.File, idx *astindex.Index, store *facts.Store, pkgPath string, decl *ast.GenDecl) {
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			from := astindex.TypeSymbolID(pkgPath, s.Name.Name)
			addTypeReferences(p, file, idx, store, from, s.Type)
		case *ast.ValueSpec:
			kind := valueDeclarationKind(decl.Tok)
			if kind == "" {
				// 非 var/const（如 import）的 ValueSpec 不处理。
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

// extractInitializerReferences 提取包级 var/const 初始化表达式中的引用边。
// 与函数体不同，初始化表达式没有局部作用域类型推断，因此 resolver 使用空的 scopedValueTypes。
func extractInitializerReferences(p *project.Project, file *project.File, idx *astindex.Index, store *facts.Store, from facts.SymbolID, expr ast.Expr) {
	// ignored 标记不应作为 value 引用的位置（如组合字面量类型、键值对的键）。
	ignored := ignoredValuePositions(expr)
	// callFuns 标记作为调用函数的选择器位置，需走接收者解析路径。
	callFuns := callFunPositions(expr)
	resolver := newResolver(file, idx, scopedValueTypes{})
	var visit func(node ast.Node) bool
	visit = func(node ast.Node) bool {
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
			// 选择器已整体解析，避免再回退到根 Ident 单独产生重复 value 引用；
			// 但接收者表达式内含调用（链式 Helper(g).Method(...)）时必须单独
			// 遍历该子树，否则接收者调用引用会被整体剪枝而漏掉。
			if containsCallExpr(x.X) {
				ast.Inspect(x.X, visit)
			}
			return false
		case *ast.Ident:
			if ignored[x.Pos()] || callFuns[x.Pos()] {
				return true
			}
			addValueReferenceFacts(p, file, store, from, x, resolver.ResolveValueIDs(x))
		}
		return true
	}
	ast.Inspect(expr, visit)
}

// addCallReference 将一次调用解析为目标符号候选，并为每个候选写一条 call 引用边。
// 当解析失败且属于项目内部调用时，记录接口绑定相关诊断而不是写出边。
func addCallReference(p *project.Project, file *project.File, idx *astindex.Index, store *facts.Store, from facts.SymbolID, scopedTypes scopedValueTypes, call *ast.CallExpr) {
	resolver := newResolver(file, idx, scopedTypes)
	resolved, raw, ok := resolver.ResolveCall(call)
	if !ok || len(resolved) == 0 {
		callee := unwrapGenericCallee(call.Fun)
		// 仅当确属项目内部符号且能给出接口绑定/未解析诊断时才上报。
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
		// 跳过自引用和空目标，避免产生无意义的环或空边。
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

// valueDeclarationKind 将声明 token 映射为 facts 符号 ID 所用的 value 种类字符串。
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

// functionSymbol 计算函数/方法声明的 FromSymbol：函数返回函数符号，方法返回带接收者类型的方法符号。
func functionSymbol(pkgPath string, fn *ast.FuncDecl) facts.SymbolID {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return astindex.FunctionSymbolID(pkgPath, fn.Name.Name)
	}
	return astindex.MethodSymbolID(pkgPath, astindex.ReceiverTypeName(fn.Recv.List[0].Type), fn.Name.Name)
}

// referenceID 由边类型、起止符号与源码位置组合出稳定 ID，
// 使同一引用的语义身份加上源码位置即可去重。
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

// spanFor 将 token.Pos 转换为相对项目根目录的源码跨度。
func spanFor(p *project.Project, file *project.File, start, end token.Pos) facts.SourceSpan {
	span := astindex.SourceSpanFor(file.FileSet, start, end)
	if rel, err := filepath.Rel(p.Root, span.File); err == nil {
		span.File = filepath.ToSlash(rel)
	}
	return span
}
