package grpc

import (
	"go/ast"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// ServerBindingIssueKind distinguishes why a registration's concrete
// implementation could not be bound, so callers can raise a precise diagnostic.
type ServerBindingIssueKind string

const (
	// ServerBindingUnresolved: no concrete implementation type could be proven.
	ServerBindingUnresolved ServerBindingIssueKind = "unresolved"
	// ServerBindingAmbiguous: more than one concrete implementation type remains
	// possible; the analyzer refuses to guess between them.
	ServerBindingAmbiguous ServerBindingIssueKind = "ambiguous"
)

// ServerBindingIssue records a known registration whose concrete
// implementation cannot be proven (unresolved) or is not provably unique
// (ambiguous). Either way is a diagnostic, not an error: unrelated
// registrations in the same project are never affected by one problematic
// registration. Unlike before, such a registration now produces zero
// provider facts — without a resolved implementation type there is no
// repo-local evidence of which methods it actually serves, and this
// analyzer no longer reads a generated method list independent of that.
type ServerBindingIssue struct {
	Kind             ServerBindingIssueKind
	RegisterFunction string
	ServerInterface  string
	Span             facts.SourceSpan
}

// serverService is one RegisterXxxServer call's derived identity. Every
// field comes from the call site's own AST — the import path resolved at
// the call, and a string transform of the register function's own name —
// never from reading the generated package's source.
type serverService struct {
	GoPackage        string
	RegisterFunction string
	ServerInterface  string
	Service          string
}

// deriveServerService recognizes the protoc-gen-go-grpc registration
// contract: RegisterXxxServer always wraps an XxxServer interface for a
// service named Xxx. Both transforms are pure string trims on the call's own
// selector name, requiring no lookup into the target package.
func deriveServerService(importPath, registerFunction string) (serverService, bool) {
	const prefix, suffix = "Register", "Server"
	if importPath == "" || !strings.HasPrefix(registerFunction, prefix) || !strings.HasSuffix(registerFunction, suffix) {
		return serverService{}, false
	}
	serverInterface := strings.TrimPrefix(registerFunction, prefix)
	service := strings.TrimSuffix(serverInterface, suffix)
	if serverInterface == "" || service == "" {
		return serverService{}, false
	}
	return serverService{GoPackage: importPath, RegisterFunction: registerFunction, ServerInterface: serverInterface, Service: service}, true
}

// ExtractServerProviders binds generated RegisterXxxServer calls to concrete
// project methods. It never guesses between multiple implementation types:
// a registration whose implementation is unresolved or ambiguous produces no
// provider facts (there is no independent method list to fall back on) plus
// a ServerBindingIssue explaining why, so one problematic registration never
// affects any other registration in the same project.
func ExtractServerProviders(p *project.Project, idx *astindex.Index) ([]facts.GrpcOperationFact, []facts.GrpcProviderFact, []ServerBindingIssue) {
	operations := map[string]facts.GrpcOperationFact{}
	var providers []facts.GrpcProviderFact
	var issues []ServerBindingIssue
	concreteReturns := concreteCallableReturnTypes(p, idx)
	strictConcreteReturns := strictConcreteCallableReturnTypes(p, idx)
	declaredReturns := declaredCallableReturnTypes(p)
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				registrationSymbol := functionDeclarationSymbol(file, fn)
				containerProviders := collectContainerProviders(file, fn, strictConcreteReturns, declaredReturns)
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
					service, ok := deriveServerService(file.Imports[alias.Name], selector.Sel.Name)
					if !ok {
						return true
					}
					span := serverCallSpan(p.Root, file, call)
					candidates := implementationTypes(file, fn, idx, concreteReturns, call.Args[1])
					candidates = append(candidates, containerProvidedImplementationTypes(file, containerProviders, service.GoPackage, service.ServerInterface, call.Args[1])...)
					candidates = matchingImplementationTypes(p, candidates)
					var implementation astindex.ValueType
					var methods []string
					switch len(candidates) {
					case 1:
						implementation = candidates[0]
						methods = discoverProvidedMethods(p, implementation)
					case 0:
						issues = append(issues, ServerBindingIssue{Kind: ServerBindingUnresolved, RegisterFunction: service.RegisterFunction, ServerInterface: service.ServerInterface, Span: span})
					default:
						issues = append(issues, ServerBindingIssue{Kind: ServerBindingAmbiguous, RegisterFunction: service.RegisterFunction, ServerInterface: service.ServerInterface, Span: span})
					}
					registrationEvidence := facts.EvidenceFact{Kind: "grpc_server_registration", Raw: service.GoPackage + "." + service.RegisterFunction, Span: span}
					for _, goMethod := range methods {
						identity := facts.GrpcIdentity(service.GoPackage, service.Service, goMethod)
						operationID := facts.GrpcOperationID(identity)
						operation := operations[operationID]
						if operation.ID == "" {
							operation = facts.GrpcOperationFact{ID: operationID, Identity: identity, GoPackage: service.GoPackage, Service: service.Service, GoMethod: goMethod}
						}
						operation.Evidence = appendEvidenceOnce(operation.Evidence, registrationEvidence)
						operations[operationID] = operation

						provider := facts.GrpcProviderFact{
							OperationID: operationID, GeneratedGoPackage: service.GoPackage, RegisterFunction: service.RegisterFunction,
							ServerInterface: service.ServerInterface, RegistrationSymbol: registrationSymbol, Span: span,
							Evidence:                []facts.EvidenceFact{registrationEvidence},
							ImplementationGoPackage: implementation.PackagePath, ImplementationType: implementation.TypeName,
						}
						if implementation.TypeName != "" {
							provider.ImplementationSymbol = astindex.TypeSymbolID(implementation.PackagePath, implementation.TypeName)
							handler := astindex.MethodSymbolID(implementation.PackagePath, implementation.TypeName, goMethod)
							if _, exists := idx.Symbols[handler]; exists {
								provider.HandlerSymbol = handler
							}
						}
						provider.ID = facts.GrpcProviderID(provider.OperationID, span)
						providers = append(providers, provider)
					}
					return true
				})
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
	return sortedOperations(operations), providers, issues
}

// matchingImplementationTypes narrows candidate concrete types to those that
// expose at least one method matching the gRPC unary handler shape. This
// replaces matching against a pre-known method list (no longer available
// without reading generated code) with a structural check on the candidate
// itself — still a proof, just a shape-based one instead of a name-based one.
func matchingImplementationTypes(p *project.Project, candidates []astindex.ValueType) []astindex.ValueType {
	var matched []astindex.ValueType
	for _, candidate := range uniqueValueTypes(candidates) {
		if len(discoverProvidedMethods(p, candidate)) > 0 {
			matched = append(matched, candidate)
		}
	}
	return uniqueValueTypes(matched)
}

// discoverProvidedMethods lists candidate's own exported methods whose
// signature matches a unary gRPC handler: func(ctx context.Context, req T)
// (resp T, error). This is a structural proof from the analyzed repo's own
// method declarations, not a lookup against a known interface — so it can
// only see unary methods actually implemented in this repo. Streaming
// methods are out of scope: telling client/server/bidirectional streaming
// apart reliably requires the generated ServiceDesc this analyzer no longer
// reads, and real-world usage across the analyzed projects is 100% unary.
func discoverProvidedMethods(p *project.Project, implementation astindex.ValueType) []string {
	pkg := p.Packages[implementation.PackagePath]
	if pkg == nil || implementation.TypeName == "" {
		return nil
	}
	var methods []string
	for _, file := range pkg.Files {
		for _, decl := range file.AST.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Body == nil || !fn.Name.IsExported() {
				continue
			}
			if astindex.ReceiverTypeName(fn.Recv.List[0].Type) != implementation.TypeName {
				continue
			}
			if isUnaryGrpcHandlerShape(file, fn.Type) {
				methods = append(methods, fn.Name.Name)
			}
		}
	}
	sort.Strings(methods)
	return methods
}

func isUnaryGrpcHandlerShape(file *project.File, signature *ast.FuncType) bool {
	params := flattenFieldTypes(signature.Params)
	if len(params) != 2 || !isContextContextType(file, params[0]) {
		return false
	}
	results := flattenFieldTypes(signature.Results)
	return len(results) == 2 && isBuiltinErrorType(results[1])
}

func flattenFieldTypes(list *ast.FieldList) []ast.Expr {
	if list == nil {
		return nil
	}
	var out []ast.Expr
	for _, field := range list.List {
		if len(field.Names) == 0 {
			out = append(out, field.Type)
			continue
		}
		for range field.Names {
			out = append(out, field.Type)
		}
	}
	return out
}

func isContextContextType(file *project.File, expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Context" {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && file.Imports[ident.Name] == "context"
}

func isBuiltinErrorType(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "error"
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

// strictConcreteCallableReturnTypes recognizes only factories for which every
// return path has the same explicit project-local concrete type. This is
// deliberately stricter than concreteCallableReturnTypes: a direct
// registration expression can still surface multiple candidates as ambiguous,
// while a container lookup has no call-site evidence to select among branches.
func strictConcreteCallableReturnTypes(p *project.Project, idx *astindex.Index) map[facts.SymbolID]astindex.ValueType {
	out := map[facts.SymbolID]astindex.ValueType{}
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				var concrete astindex.ValueType
				found := false
				unknown := false
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					switch node := node.(type) {
					case *ast.FuncLit:
						return false
					case *ast.ReturnStmt:
						if len(node.Results) == 0 {
							unknown = true
							return false
						}
						valueType, ok := explicitConcreteType(file, idx, node.Results[0])
						if !ok || (found && valueType != concrete) {
							unknown = true
							return false
						}
						concrete = valueType
						found = true
						return false
					default:
						return !unknown
					}
				})
				if !unknown && found {
					out[functionDeclarationSymbol(file, fn)] = concrete
				}
			}
		}
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
