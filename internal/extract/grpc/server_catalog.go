package grpc

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// ServerRegistrationKey identifies a generated RegisterXxxServer function.
type ServerRegistrationKey struct {
	GoPackage        string
	RegisterFunction string
}

// ServerMethodEntry binds one generated server interface method to its
// canonical protobuf operation.
type ServerMethodEntry struct {
	GoMethod  string
	Operation facts.GrpcOperationFact
}

// ServerServiceEntry describes one generated gRPC server registration API.
type ServerServiceEntry struct {
	GoPackage        string
	RegisterFunction string
	ServerInterface  string
	Methods          []ServerMethodEntry
}

// ServerCatalog is an immutable index of generated server registrations.
type ServerCatalog struct {
	Operations     []facts.GrpcOperationFact
	ByRegistration map[ServerRegistrationKey]ServerServiceEntry
}

// Lookup resolves an imported RegisterXxxServer call.
func (c *ServerCatalog) Lookup(key ServerRegistrationKey) (ServerServiceEntry, bool) {
	if c == nil {
		return ServerServiceEntry{}, false
	}
	entry, ok := c.ByRegistration[key]
	return entry, ok
}

type serverSourceFile struct {
	importPath string
	path       string
	fset       *token.FileSet
	file       *ast.File
	evidence   string
}

// BuildServerCatalog scans both the main project and selected dependency
// packages. This supports repositories that keep generated code in the main
// module as well as repositories that consume locally replaced proto modules.
func BuildServerCatalog(p *project.Project, dependencies []project.DependencyPackage) (*ServerCatalog, error) {
	catalog := &ServerCatalog{ByRegistration: map[ServerRegistrationKey]ServerServiceEntry{}}
	operations := map[string]facts.GrpcOperationFact{}

	var sources []serverSourceFile
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			if !hasGeneratedMarker(file.AST) {
				continue
			}
			rel, err := filepath.Rel(p.Root, file.Path)
			if err != nil {
				rel = file.Path
			}
			sources = append(sources, serverSourceFile{
				importPath: projectGeneratedImportPath(p, file),
				path:       file.Path,
				fset:       file.FileSet,
				file:       file.AST,
				evidence:   filepath.ToSlash(rel),
			})
		}
	}
	for _, pkg := range dependencies {
		for _, name := range pkg.GoFiles {
			path := filepath.Join(pkg.Dir, name)
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				return nil, fmt.Errorf("parse generated gRPC server source %s: %w", pkg.ImportPath, err)
			}
			if !hasGeneratedMarker(file) {
				continue
			}
			sources = append(sources, serverSourceFile{
				importPath: pkg.ImportPath,
				path:       path,
				fset:       fset,
				file:       file,
				evidence:   logicalDependencyPath(pkg.ImportPath, path),
			})
		}
	}

	for _, source := range sources {
		entries, err := parseServerServices(source)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			key := ServerRegistrationKey{GoPackage: entry.GoPackage, RegisterFunction: entry.RegisterFunction}
			if existing, ok := catalog.ByRegistration[key]; ok {
				if !sameServerService(existing, entry) {
					return nil, fmt.Errorf("gRPC server catalog conflict %s.%s", key.GoPackage, key.RegisterFunction)
				}
				continue
			}
			catalog.ByRegistration[key] = entry
			for _, method := range entry.Methods {
				existing := operations[method.Operation.ID]
				if existing.ID == "" {
					existing = method.Operation
				} else if existing.StreamingMode != method.Operation.StreamingMode {
					return nil, fmt.Errorf("gRPC server operation conflict %s", method.Operation.FullMethod)
				}
				for _, evidence := range method.Operation.Evidence {
					existing.Evidence = appendEvidenceOnce(existing.Evidence, evidence)
				}
				operations[existing.ID] = existing
			}
		}
	}
	for _, operation := range operations {
		if operation.ClientBindings == nil {
			operation.ClientBindings = []facts.GrpcClientBinding{}
		}
		catalog.Operations = append(catalog.Operations, operation)
	}
	sort.Slice(catalog.Operations, func(i, j int) bool { return catalog.Operations[i].ID < catalog.Operations[j].ID })
	return catalog, nil
}

// ProjectGeneratedServerImportPaths lists generated server packages that can
// be read directly from the repository, including nested Go modules.
func ProjectGeneratedServerImportPaths(p *project.Project) []string {
	seen := map[string]bool{}
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			if !hasGeneratedMarker(file.AST) {
				continue
			}
			source := serverSourceFile{importPath: projectGeneratedImportPath(p, file), path: file.Path, fset: file.FileSet, file: file.AST}
			entries, err := parseServerServices(source)
			if err == nil && len(entries) > 0 {
				seen[source.importPath] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func projectGeneratedImportPath(p *project.Project, file *project.File) string {
	dir := filepath.Dir(file.Path)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			modulePath, readErr := project.ReadModulePath(dir)
			if readErr == nil {
				rel, relErr := filepath.Rel(dir, filepath.Dir(file.Path))
				if relErr == nil && rel != "." {
					return strings.TrimSuffix(modulePath, "/") + "/" + filepath.ToSlash(rel)
				}
				return modulePath
			}
		}
		if dir == p.Root || filepath.Dir(dir) == dir {
			break
		}
		dir = filepath.Dir(dir)
	}
	return file.Package.Path
}

type serviceDescriptor struct {
	interfaceName string
	serviceName   string
	methods       []descriptorMethod
	evidence      facts.EvidenceFact
}

type descriptorMethod struct {
	name string
	mode facts.GrpcStreamingMode
}

func parseServerServices(source serverSourceFile) ([]ServerServiceEntry, error) {
	constants := map[string]string{}
	collectStringConstants(source.file, constants)
	interfaceMethods := collectServerInterfaceMethods(source.file)
	descriptors := map[string]serviceDescriptor{}
	for _, decl := range source.file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, raw := range gen.Specs {
			spec, ok := raw.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range spec.Names {
				if !strings.HasSuffix(name.Name, "_ServiceDesc") || i >= len(spec.Values) {
					continue
				}
				desc, ok := parseServiceDescriptor(spec.Values[i], constants)
				if !ok {
					continue
				}
				position := source.fset.Position(name.Pos())
				desc.evidence = facts.EvidenceFact{
					Kind: "generated_grpc_service_descriptor",
					Raw:  desc.serviceName,
					Span: facts.SourceSpan{File: source.evidence, StartLine: position.Line, StartCol: position.Column, EndLine: position.Line, EndCol: position.Column + len(name.Name)},
				}
				descriptors[desc.interfaceName] = desc
			}
		}
	}

	var out []ServerServiceEntry
	for _, decl := range source.file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Register") || !strings.HasSuffix(fn.Name.Name, "Server") {
			continue
		}
		interfaceName := secondParameterType(fn.Type.Params)
		desc, ok := descriptors[interfaceName]
		if !ok || len(desc.methods) == 0 {
			continue
		}
		entry := ServerServiceEntry{GoPackage: source.importPath, RegisterFunction: fn.Name.Name, ServerInterface: interfaceName}
		for _, method := range desc.methods {
			operation, err := operationFromFullMethod("/"+desc.serviceName+"/"+method.name, method.mode)
			if err != nil {
				continue
			}
			operation.ClientBindings = []facts.GrpcClientBinding{}
			operation.Evidence = []facts.EvidenceFact{desc.evidence}
			entry.Methods = append(entry.Methods, ServerMethodEntry{GoMethod: serverGoMethod(method.name, interfaceMethods[interfaceName]), Operation: operation})
		}
		sort.Slice(entry.Methods, func(i, j int) bool {
			return entry.Methods[i].Operation.FullMethod < entry.Methods[j].Operation.FullMethod
		})
		if len(entry.Methods) > 0 {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RegisterFunction < out[j].RegisterFunction })
	return out, nil
}

func collectServerInterfaceMethods(file *ast.File) map[string][]string {
	out := map[string][]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, raw := range gen.Specs {
			spec, ok := raw.(*ast.TypeSpec)
			if !ok {
				continue
			}
			iface, ok := spec.Type.(*ast.InterfaceType)
			if !ok {
				continue
			}
			for _, field := range iface.Methods.List {
				if _, ok := field.Type.(*ast.FuncType); !ok {
					continue
				}
				for _, name := range field.Names {
					out[spec.Name.Name] = append(out[spec.Name.Name], name.Name)
				}
			}
		}
	}
	return out
}

func serverGoMethod(rpcMethod string, interfaceMethods []string) string {
	for _, method := range interfaceMethods {
		if method == rpcMethod {
			return method
		}
	}
	matched := ""
	for _, method := range interfaceMethods {
		if !strings.EqualFold(method, rpcMethod) {
			continue
		}
		if matched != "" {
			return rpcMethod
		}
		matched = method
	}
	if matched != "" {
		return matched
	}
	return rpcMethod
}

func parseServiceDescriptor(expr ast.Expr, constants map[string]string) (serviceDescriptor, bool) {
	lit := unwrapCompositeLiteral(expr)
	if lit == nil {
		return serviceDescriptor{}, false
	}
	var desc serviceDescriptor
	for _, raw := range lit.Elts {
		field, ok := raw.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		switch identName(field.Key) {
		case "ServiceName":
			desc.serviceName, _ = resolveString(field.Value, constants)
		case "HandlerType":
			desc.interfaceName = handlerInterfaceName(field.Value)
		case "Methods":
			desc.methods = append(desc.methods, descriptorMethods(field.Value, false)...)
		case "Streams":
			desc.methods = append(desc.methods, descriptorMethods(field.Value, true)...)
		}
	}
	return desc, desc.interfaceName != "" && desc.serviceName != ""
}

func descriptorMethods(expr ast.Expr, streaming bool) []descriptorMethod {
	lit := unwrapCompositeLiteral(expr)
	if lit == nil {
		return nil
	}
	var out []descriptorMethod
	for _, raw := range lit.Elts {
		methodLit := unwrapCompositeLiteral(raw)
		if methodLit == nil {
			continue
		}
		method := descriptorMethod{mode: facts.GrpcStreamingUnary}
		clientStreams, serverStreams := false, false
		for _, rawField := range methodLit.Elts {
			field, ok := rawField.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			switch identName(field.Key) {
			case "MethodName", "StreamName":
				method.name, _ = stringLiteral(field.Value)
			case "ClientStreams":
				clientStreams, _ = boolLiteral(field.Value)
			case "ServerStreams":
				serverStreams, _ = boolLiteral(field.Value)
			}
		}
		if method.name == "" {
			continue
		}
		if streaming {
			switch {
			case clientStreams && serverStreams:
				method.mode = facts.GrpcStreamingBidirectional
			case clientStreams:
				method.mode = facts.GrpcStreamingClient
			case serverStreams:
				method.mode = facts.GrpcStreamingServer
			default:
				method.mode = facts.GrpcStreamingServer
			}
		}
		out = append(out, method)
	}
	return out
}

func unwrapCompositeLiteral(expr ast.Expr) *ast.CompositeLit {
	switch x := expr.(type) {
	case *ast.CompositeLit:
		return x
	case *ast.UnaryExpr:
		return unwrapCompositeLiteral(x.X)
	case *ast.ParenExpr:
		return unwrapCompositeLiteral(x.X)
	}
	return nil
}

func handlerInterfaceName(expr ast.Expr) string {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return ""
	}
	return baseTypeName(call.Fun)
}

func secondParameterType(params *ast.FieldList) string {
	if params == nil || len(params.List) < 2 {
		return ""
	}
	return baseTypeName(params.List[1].Type)
}

func sameServerService(left, right ServerServiceEntry) bool {
	if left.ServerInterface != right.ServerInterface || len(left.Methods) != len(right.Methods) {
		return false
	}
	for i := range left.Methods {
		if left.Methods[i].GoMethod != right.Methods[i].GoMethod || left.Methods[i].Operation.FullMethod != right.Methods[i].Operation.FullMethod {
			return false
		}
	}
	return true
}
