package grpc

import (
	"fmt"
	"go/ast"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// ServerImplementationAmbiguityError prevents binding one registration to an
// arbitrary implementation when multiple concrete types remain possible.
type ServerImplementationAmbiguityError struct {
	RegisterFunction string
	Span             facts.SourceSpan
}

func (e *ServerImplementationAmbiguityError) Error() string {
	return fmt.Sprintf("ambiguous gRPC server implementation for %s at %s:%d", e.RegisterFunction, e.Span.File, e.Span.StartLine)
}

// ServerBindingIssue records a known registration whose concrete
// implementation cannot be proven. The registration itself still produces
// provider facts so registration diffs remain analyzable.
type ServerBindingIssue struct {
	RegisterFunction string
	ServerInterface  string
	Span             facts.SourceSpan
}

// ServerRegistrationImportPaths returns packages that are actually used by
// RegisterXxxServer calls in project source.
func ServerRegistrationImportPaths(p *project.Project) []string {
	seen := map[string]bool{}
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			ast.Inspect(file.AST, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !strings.HasPrefix(selector.Sel.Name, "Register") || !strings.HasSuffix(selector.Sel.Name, "Server") {
					return true
				}
				alias, ok := selector.X.(*ast.Ident)
				if ok && file.Imports[alias.Name] != "" {
					seen[file.Imports[alias.Name]] = true
				}
				return true
			})
		}
	}
	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// ExtractServerProviders binds generated RegisterXxxServer calls to concrete
// project methods. It never guesses between multiple implementation types.
func ExtractServerProviders(p *project.Project, idx *astindex.Index, catalog *ServerCatalog) ([]facts.GrpcProviderFact, []ServerBindingIssue, error) {
	var providers []facts.GrpcProviderFact
	var issues []ServerBindingIssue
	var ambiguity *ServerImplementationAmbiguityError
	concreteReturns := concreteCallableReturnTypes(p, idx)
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				registrationSymbol := functionDeclarationSymbol(file, fn)
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok || len(call.Args) < 2 {
						return true
					}
					selector, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					alias, ok := selector.X.(*ast.Ident)
					if !ok {
						return true
					}
					importPath := file.Imports[alias.Name]
					service, ok := catalog.Lookup(ServerRegistrationKey{GoPackage: importPath, RegisterFunction: selector.Sel.Name})
					if !ok {
						return true
					}
					span := serverCallSpan(p.Root, file, call)
					candidates := implementationTypes(file, fn, idx, concreteReturns, call.Args[1])
					candidates = matchingImplementationTypes(idx, candidates, service)
					if len(candidates) > 1 {
						ambiguity = &ServerImplementationAmbiguityError{RegisterFunction: service.RegisterFunction, Span: span}
						return false
					}
					var implementation astindex.ValueType
					if len(candidates) == 1 {
						implementation = candidates[0]
					} else {
						issues = append(issues, ServerBindingIssue{RegisterFunction: service.RegisterFunction, ServerInterface: service.ServerInterface, Span: span})
					}
					for _, method := range service.Methods {
						provider := facts.GrpcProviderFact{
							OperationID:        method.Operation.ID,
							GeneratedGoPackage: service.GoPackage,
							RegisterFunction:   service.RegisterFunction,
							ServerInterface:    service.ServerInterface,
							RegistrationSymbol: registrationSymbol,
							Span:               span,
							Evidence: []facts.EvidenceFact{{
								Kind: "grpc_server_registration",
								Raw:  service.GoPackage + "." + service.RegisterFunction,
								Span: span,
							}},
						}
						if implementation.TypeName != "" {
							provider.ImplementationGoPackage = implementation.PackagePath
							provider.ImplementationType = implementation.TypeName
							provider.ImplementationSymbol = astindex.TypeSymbolID(implementation.PackagePath, implementation.TypeName)
							handler := astindex.MethodSymbolID(implementation.PackagePath, implementation.TypeName, method.GoMethod)
							if _, exists := idx.Symbols[handler]; exists {
								provider.HandlerSymbol = handler
							}
						}
						provider.ID = facts.GrpcProviderID(provider.OperationID, span)
						providers = append(providers, provider)
					}
					return true
				})
				if ambiguity != nil {
					return nil, nil, ambiguity
				}
			}
		}
	}
	providers = dedupeProviders(providers)
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Span.File != issues[j].Span.File {
			return issues[i].Span.File < issues[j].Span.File
		}
		return issues[i].Span.StartLine < issues[j].Span.StartLine
	})
	return providers, issues, nil
}

func implementationTypes(file *project.File, fn *ast.FuncDecl, idx *astindex.Index, concreteReturns map[facts.SymbolID][]astindex.ValueType, expr ast.Expr) []astindex.ValueType {
	var candidates []astindex.ValueType
	var collect func(ast.Expr)
	collect = func(current ast.Expr) {
		switch x := current.(type) {
		case *ast.ParenExpr:
			collect(x.X)
		case *ast.UnaryExpr:
			collect(x.X)
		case *ast.CompositeLit:
			candidates = appendConcreteType(idx, candidates, astindex.ValueTypeFromTypeExpr(file, x.Type))
		case *ast.CallExpr:
			if valueType, ok := idx.ResolveBuiltinNewType(file, x); ok {
				candidates = appendConcreteType(idx, candidates, valueType)
			}
			if valueType, ok := callableReturnType(file, idx, x.Fun); ok {
				candidates = appendConcreteType(idx, candidates, valueType)
			}
			if callable, ok := callableSymbol(file, x.Fun); ok {
				candidates = append(candidates, concreteReturns[callable]...)
			}
			collectGenericTypes(file, idx, x.Fun, &candidates)
			for _, arg := range x.Args {
				collect(arg)
			}
			if selector, ok := x.Fun.(*ast.SelectorExpr); ok {
				collect(selector.X)
			}
		case *ast.SelectorExpr:
			if callable, ok := callableSymbol(file, x); ok {
				candidates = append(candidates, concreteReturns[callable]...)
			}
			if fieldType, ok := receiverFieldType(file, fn, idx, x); ok {
				candidates = appendConcreteType(idx, candidates, fieldType)
			}
			collect(x.X)
		case *ast.Ident:
			if callable, ok := callableSymbol(file, x); ok {
				candidates = append(candidates, concreteReturns[callable]...)
			}
			if valueType, ok := idx.CallableReturnTypes[astindex.FunctionSymbolID(file.Package.Path, x.Name)]; ok {
				candidates = appendConcreteType(idx, candidates, valueType)
			}
		case *ast.IndexExpr:
			collectGenericTypes(file, idx, x, &candidates)
			collect(x.X)
		case *ast.IndexListExpr:
			collectGenericTypes(file, idx, x, &candidates)
			collect(x.X)
		}
	}
	collect(expr)
	return uniqueValueTypes(candidates)
}

// concreteCallableReturnTypes records constructors whose declared result is an
// interface but whose return statement proves one project-local concrete type.
// This is common when DI helpers wrap NewXxxProvider() in a generic container.
func concreteCallableReturnTypes(p *project.Project, idx *astindex.Index) map[facts.SymbolID][]astindex.ValueType {
	out := map[facts.SymbolID][]astindex.ValueType{}
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				id := functionDeclarationSymbol(file, fn)
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					if _, nested := node.(*ast.FuncLit); nested {
						return false
					}
					ret, ok := node.(*ast.ReturnStmt)
					if !ok || len(ret.Results) == 0 {
						return true
					}
					if valueType, ok := explicitConcreteType(file, idx, ret.Results[0]); ok {
						out[id] = append(out[id], valueType)
					}
					return true
				})
			}
		}
	}
	for id, types := range out {
		out[id] = uniqueValueTypes(types)
	}
	return out
}

func explicitConcreteType(file *project.File, idx *astindex.Index, expr ast.Expr) (astindex.ValueType, bool) {
	for {
		switch x := expr.(type) {
		case *ast.ParenExpr:
			expr = x.X
		case *ast.UnaryExpr:
			expr = x.X
		case *ast.CompositeLit:
			valueType := astindex.ValueTypeFromTypeExpr(file, x.Type)
			return concreteProjectType(idx, valueType)
		case *ast.CallExpr:
			valueType, ok := idx.ResolveBuiltinNewType(file, x)
			if !ok {
				return astindex.ValueType{}, false
			}
			return concreteProjectType(idx, valueType)
		default:
			return astindex.ValueType{}, false
		}
	}
}

func concreteProjectType(idx *astindex.Index, valueType astindex.ValueType) (astindex.ValueType, bool) {
	id := astindex.TypeSymbolID(valueType.PackagePath, valueType.TypeName)
	if valueType.PackagePath == "" || valueType.TypeName == "" {
		return astindex.ValueType{}, false
	}
	if _, exists := idx.Symbols[id]; !exists {
		return astindex.ValueType{}, false
	}
	if _, isInterface := idx.InterfaceTypes[id]; isInterface {
		return astindex.ValueType{}, false
	}
	return valueType, true
}

func callableSymbol(file *project.File, expr ast.Expr) (facts.SymbolID, bool) {
	switch x := expr.(type) {
	case *ast.Ident:
		if x.Obj != nil && x.Obj.Kind != ast.Fun {
			return "", false
		}
		return astindex.FunctionSymbolID(file.Package.Path, x.Name), true
	case *ast.SelectorExpr:
		alias, ok := x.X.(*ast.Ident)
		if !ok || (alias.Obj != nil && alias.Obj.Kind != ast.Pkg) || file.Imports[alias.Name] == "" {
			return "", false
		}
		return astindex.FunctionSymbolID(file.Imports[alias.Name], x.Sel.Name), true
	case *ast.IndexExpr:
		return callableSymbol(file, x.X)
	case *ast.IndexListExpr:
		return callableSymbol(file, x.X)
	default:
		return "", false
	}
}

func callableReturnType(file *project.File, idx *astindex.Index, expr ast.Expr) (astindex.ValueType, bool) {
	switch x := expr.(type) {
	case *ast.Ident:
		value, ok := idx.CallableReturnTypes[astindex.FunctionSymbolID(file.Package.Path, x.Name)]
		return value, ok
	case *ast.SelectorExpr:
		alias, ok := x.X.(*ast.Ident)
		if !ok {
			return astindex.ValueType{}, false
		}
		value, found := idx.CallableReturnTypes[astindex.FunctionSymbolID(file.Imports[alias.Name], x.Sel.Name)]
		return value, found
	case *ast.IndexExpr:
		return callableReturnType(file, idx, x.X)
	case *ast.IndexListExpr:
		return callableReturnType(file, idx, x.X)
	}
	return astindex.ValueType{}, false
}

func collectGenericTypes(file *project.File, idx *astindex.Index, expr ast.Expr, candidates *[]astindex.ValueType) {
	var args []ast.Expr
	switch x := expr.(type) {
	case *ast.IndexExpr:
		args = []ast.Expr{x.Index}
	case *ast.IndexListExpr:
		args = x.Indices
	case *ast.SelectorExpr:
		collectGenericTypes(file, idx, x.X, candidates)
	}
	for _, arg := range args {
		*candidates = appendConcreteType(idx, *candidates, astindex.ValueTypeFromTypeExpr(file, arg))
	}
}

func receiverFieldType(file *project.File, fn *ast.FuncDecl, idx *astindex.Index, selector *ast.SelectorExpr) (astindex.ValueType, bool) {
	ident, ok := selector.X.(*ast.Ident)
	if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 || len(fn.Recv.List[0].Names) != 1 || fn.Recv.List[0].Names[0].Name != ident.Name {
		return astindex.ValueType{}, false
	}
	receiver := astindex.ValueTypeFromTypeExpr(file, fn.Recv.List[0].Type)
	fields := idx.StructFieldTypes[astindex.TypeSymbolID(receiver.PackagePath, receiver.TypeName)]
	field, ok := fields[selector.Sel.Name]
	return field, ok
}

func appendConcreteType(idx *astindex.Index, items []astindex.ValueType, item astindex.ValueType) []astindex.ValueType {
	if item.PackagePath == "" || item.TypeName == "" {
		return items
	}
	if _, exists := idx.Symbols[astindex.TypeSymbolID(item.PackagePath, item.TypeName)]; !exists {
		return items
	}
	return append(items, item)
}

func matchingImplementationTypes(idx *astindex.Index, candidates []astindex.ValueType, service ServerServiceEntry) []astindex.ValueType {
	var matched []astindex.ValueType
	for _, candidate := range uniqueValueTypes(candidates) {
		for _, method := range service.Methods {
			if _, exists := idx.Symbols[astindex.MethodSymbolID(candidate.PackagePath, candidate.TypeName, method.GoMethod)]; exists {
				matched = append(matched, candidate)
				break
			}
		}
	}
	return uniqueValueTypes(matched)
}

func uniqueValueTypes(items []astindex.ValueType) []astindex.ValueType {
	seen := map[string]bool{}
	var out []astindex.ValueType
	for _, item := range items {
		key := item.PackagePath + "\x00" + item.TypeName
		if item.PackagePath == "" || item.TypeName == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PackagePath != out[j].PackagePath {
			return out[i].PackagePath < out[j].PackagePath
		}
		return out[i].TypeName < out[j].TypeName
	})
	return out
}

func functionDeclarationSymbol(file *project.File, fn *ast.FuncDecl) facts.SymbolID {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return astindex.FunctionSymbolID(file.Package.Path, fn.Name.Name)
	}
	return astindex.MethodSymbolID(file.Package.Path, astindex.ReceiverTypeName(fn.Recv.List[0].Type), fn.Name.Name)
}

func serverCallSpan(root string, file *project.File, call *ast.CallExpr) facts.SourceSpan {
	start := file.FileSet.Position(call.Pos())
	end := file.FileSet.Position(call.End())
	return facts.SourceSpan{File: relativeProjectFile(root, file.Path), StartLine: start.Line, StartCol: start.Column, EndLine: end.Line, EndCol: end.Column}
}

func relativeProjectFile(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func dedupeProviders(items []facts.GrpcProviderFact) []facts.GrpcProviderFact {
	byID := map[string]facts.GrpcProviderFact{}
	for _, item := range items {
		byID[item.ID] = item
	}
	out := make([]facts.GrpcProviderFact, 0, len(byID))
	for _, item := range byID {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
