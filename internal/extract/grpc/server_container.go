package grpc

import (
	"go/ast"
	"go/token"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// containerProvider is one statically visible, unconditional registration in
// a function body. The analyzer deliberately models only the narrow pattern
// used by known generic containers: package.Provide(factory), followed by
// package.GetBean[Interface]().MustGet() in the same function.
type containerProvider struct {
	containerKey  string
	interfaceType astindex.ValueType
	concreteTypes []astindex.ValueType
	pos           token.Pos
}

// declaredCallableReturnTypes preserves a factory's declared interface return
// type. astindex intentionally narrows a unique interface-returning factory
// to its concrete result for ordinary call resolution, while DI matching needs
// both sides of that proof.
func declaredCallableReturnTypes(p *project.Project) map[facts.SymbolID]astindex.ValueType {
	out := map[facts.SymbolID]astindex.ValueType{}
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Type.Results == nil || len(fn.Type.Results.List) == 0 {
					continue
				}
				returnType := astindex.ValueTypeFromTypeExpr(file, fn.Type.Results.List[0].Type)
				if returnType.TypeName != "" {
					out[functionDeclarationSymbol(file, fn)] = returnType
				}
			}
		}
	}
	return out
}

// collectContainerProviders extracts only top-level expression-statement
// registrations. Restricting the scope excludes conditionals, loops and
// deferred work whose runtime registration cannot be proven at the server
// registration site.
func collectContainerProviders(file *project.File, fn *ast.FuncDecl, strictConcreteReturns map[facts.SymbolID]astindex.ValueType, declaredReturns map[facts.SymbolID]astindex.ValueType) []containerProvider {
	if fn == nil || fn.Body == nil {
		return nil
	}
	var providers []containerProvider
	for _, statement := range fn.Body.List {
		expr, ok := statement.(*ast.ExprStmt)
		if !ok {
			continue
		}
		ast.Inspect(expr.X, func(node ast.Node) bool {
			if _, nested := node.(*ast.FuncLit); nested {
				return false
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			provider, ok := containerProviderFromCall(file, call, strictConcreteReturns, declaredReturns)
			if ok {
				providers = append(providers, provider)
			}
			return true
		})
	}
	return providers
}

func containerProviderFromCall(file *project.File, call *ast.CallExpr, strictConcreteReturns map[facts.SymbolID]astindex.ValueType, declaredReturns map[facts.SymbolID]astindex.ValueType) (containerProvider, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Provide" || len(call.Args) != 1 {
		return containerProvider{}, false
	}
	containerKey, ok := importedReceiverKey(file, selector.X)
	if !ok {
		return containerProvider{}, false
	}
	factory, ok := callableSymbol(file, call.Args[0])
	if !ok {
		return containerProvider{}, false
	}
	interfaceType, ok := declaredReturns[factory]
	if !ok || interfaceType.TypeName == "" {
		return containerProvider{}, false
	}
	concreteType, ok := strictConcreteReturns[factory]
	if !ok {
		return containerProvider{}, false
	}
	return containerProvider{
		containerKey:  containerKey,
		interfaceType: interfaceType,
		concreteTypes: []astindex.ValueType{concreteType},
		pos:           call.Pos(),
	}, true
}

// containerProvidedImplementationTypes resolves a GetBean[T]().MustGet()
// registration argument through preceding unconditional Provide factories.
// The target T must be exactly the generated server interface and every
// factory must declare that same return interface; matching by method names
// alone would be too weak for an IoC-style flow.
func containerProvidedImplementationTypes(file *project.File, providers []containerProvider, service ServerServiceEntry, expr ast.Expr) []astindex.ValueType {
	containerKey, target, ok := getBeanRequest(file, expr)
	if !ok || !sameValueType(target, astindex.ValueType{PackagePath: service.GoPackage, TypeName: service.ServerInterface}) {
		return nil
	}
	var candidates []astindex.ValueType
	requestPos := expr.Pos()
	for _, provider := range providers {
		if provider.pos >= requestPos || provider.containerKey != containerKey || !sameValueType(provider.interfaceType, target) {
			continue
		}
		candidates = append(candidates, provider.concreteTypes...)
	}
	return uniqueValueTypes(candidates)
}

func getBeanRequest(file *project.File, expr ast.Expr) (string, astindex.ValueType, bool) {
	call, ok := unwrapServerExpr(expr).(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return "", astindex.ValueType{}, false
	}
	mustGet, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || mustGet.Sel.Name != "MustGet" {
		return "", astindex.ValueType{}, false
	}
	getBean, ok := unwrapServerExpr(mustGet.X).(*ast.CallExpr)
	if !ok || len(getBean.Args) != 0 {
		return "", astindex.ValueType{}, false
	}
	selector, targetExpr, ok := genericSelector(getBean.Fun, "GetBean")
	if !ok {
		return "", astindex.ValueType{}, false
	}
	containerKey, ok := importedReceiverKey(file, selector.X)
	if !ok {
		return "", astindex.ValueType{}, false
	}
	target := astindex.ValueTypeFromTypeExpr(file, targetExpr)
	if target.TypeName == "" {
		return "", astindex.ValueType{}, false
	}
	return containerKey, target, true
}

func genericSelector(expr ast.Expr, name string) (*ast.SelectorExpr, ast.Expr, bool) {
	switch value := expr.(type) {
	case *ast.IndexExpr:
		selector, ok := value.X.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != name {
			return nil, nil, false
		}
		return selector, value.Index, true
	case *ast.IndexListExpr:
		if len(value.Indices) != 1 {
			return nil, nil, false
		}
		selector, ok := value.X.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != name {
			return nil, nil, false
		}
		return selector, value.Indices[0], true
	default:
		return nil, nil, false
	}
}

func importedReceiverKey(file *project.File, expr ast.Expr) (string, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok || (ident.Obj != nil && ident.Obj.Kind != ast.Pkg) {
		return "", false
	}
	path := file.Imports[ident.Name]
	if path == "" {
		return "", false
	}
	return path, true
}

func unwrapServerExpr(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = paren.X
	}
}

func sameValueType(left, right astindex.ValueType) bool {
	return left.PackagePath == right.PackagePath && left.TypeName == right.TypeName
}
