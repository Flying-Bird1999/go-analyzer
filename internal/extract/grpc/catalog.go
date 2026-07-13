// catalog.go 从当前 dependency graph 的 generated gRPC client source 构建 operation catalog。
package grpc

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// BindingKey 唯一标识一个 generated Go client method。
type BindingKey struct {
	GoPackage  string
	ClientType string
	GoMethod   string
}

// CatalogEntry 是 binding 对应的 operation 和 generated transport 证据。
type CatalogEntry struct {
	Operation facts.GrpcOperationFact
	Binding   facts.GrpcClientBinding
	Evidence  facts.EvidenceFact
}

// Catalog 是 immutable generated client binding 索引。
type Catalog struct {
	Operations []facts.GrpcOperationFact
	ByBinding  map[BindingKey]CatalogEntry
}

// Lookup 按 generated Go receiver binding 查 operation。
func (c *Catalog) Lookup(key BindingKey) (CatalogEntry, bool) {
	if c == nil {
		return CatalogEntry{}, false
	}
	entry, ok := c.ByBinding[key]
	return entry, ok
}

// BuildCatalog 解析 selected dependency packages。任何 binding 冲突都会失败，避免推测。
func BuildCatalog(packages []project.DependencyPackage) (*Catalog, error) {
	catalog := &Catalog{ByBinding: map[BindingKey]CatalogEntry{}}
	operations := map[string]facts.GrpcOperationFact{}
	for _, pkg := range packages {
		entries, err := buildPackageCatalog(pkg)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			key := BindingKey{GoPackage: entry.Binding.GoPackage, ClientType: entry.Binding.ClientType, GoMethod: entry.Binding.GoMethod}
			if existing, ok := catalog.ByBinding[key]; ok && existing.Operation.FullMethod != entry.Operation.FullMethod {
				return nil, fmt.Errorf("gRPC catalog binding conflict %s.%s.%s: %s vs %s", key.GoPackage, key.ClientType, key.GoMethod, existing.Operation.FullMethod, entry.Operation.FullMethod)
			}
			catalog.ByBinding[key] = entry
			operation := operations[entry.Operation.ID]
			if operation.ID == "" {
				operation = entry.Operation
			} else {
				if operation.StreamingMode != entry.Operation.StreamingMode {
					return nil, fmt.Errorf("gRPC catalog operation conflict %s: %s vs %s", entry.Operation.FullMethod, operation.StreamingMode, entry.Operation.StreamingMode)
				}
				operation.ClientBindings = appendBindingOnce(operation.ClientBindings, entry.Binding)
				operation.Evidence = appendEvidenceOnce(operation.Evidence, entry.Evidence)
			}
			operations[entry.Operation.ID] = operation
		}
	}
	for _, operation := range operations {
		sort.Slice(operation.ClientBindings, func(i, j int) bool {
			a, b := operation.ClientBindings[i], operation.ClientBindings[j]
			if a.GoPackage != b.GoPackage {
				return a.GoPackage < b.GoPackage
			}
			if a.ClientType != b.ClientType {
				return a.ClientType < b.ClientType
			}
			return a.GoMethod < b.GoMethod
		})
		sort.Slice(operation.Evidence, func(i, j int) bool { return evidenceKey(operation.Evidence[i]) < evidenceKey(operation.Evidence[j]) })
		catalog.Operations = append(catalog.Operations, operation)
	}
	sort.Slice(catalog.Operations, func(i, j int) bool { return catalog.Operations[i].ID < catalog.Operations[j].ID })
	return catalog, nil
}

type generatedFile struct {
	path string
	fset *token.FileSet
	ast  *ast.File
}

func buildPackageCatalog(pkg project.DependencyPackage) ([]CatalogEntry, error) {
	var files []generatedFile
	for _, name := range pkg.GoFiles {
		path := filepath.Join(pkg.Dir, name)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse generated gRPC source %s: %w", pkg.ImportPath, err)
		}
		if hasGeneratedMarker(file) {
			files = append(files, generatedFile{path: path, fset: fset, ast: file})
		}
	}
	if len(files) == 0 {
		return nil, nil
	}
	constants := map[string]string{}
	concreteToInterface := map[string]string{}
	streamModes := map[string][]facts.GrpcStreamingMode{}
	for _, file := range files {
		collectStringConstants(file.ast, constants)
		collectClientConstructors(file.ast, concreteToInterface)
		collectStreamModes(file.ast, streamModes)
	}
	var out []CatalogEntry
	for _, file := range files {
		for _, decl := range file.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Body == nil {
				continue
			}
			concrete := baseTypeName(fn.Recv.List[0].Type)
			clientType := concreteToInterface[concrete]
			if concrete == "" || clientType == "" {
				continue
			}
			fullMethod, mode, found := generatedTransport(fn.Body, constants, streamModes)
			if !found {
				continue
			}
			operation, err := operationFromFullMethod(fullMethod, mode)
			if err != nil {
				// Some generated SDKs use Invoke/NewStream for transport internals but do
				// not expose a canonical protobuf service/method identity. They do not
				// satisfy this analyzer's generated gRPC operation contract.
				continue
			}
			binding := facts.GrpcClientBinding{GoPackage: pkg.ImportPath, ClientType: clientType, GoMethod: fn.Name.Name}
			position := file.fset.Position(fn.Pos())
			evidence := facts.EvidenceFact{
				Kind:       "generated_grpc_transport",
				Raw:        fullMethod,
				Span:       facts.SourceSpan{File: logicalDependencyPath(pkg.ImportPath, file.path), StartLine: position.Line, StartCol: position.Column, EndLine: position.Line, EndCol: position.Column + len(fn.Name.Name)},
				Confidence: facts.ConfidenceHigh,
			}
			operation.ClientBindings = []facts.GrpcClientBinding{binding}
			operation.Evidence = []facts.EvidenceFact{evidence}
			out = append(out, CatalogEntry{Operation: operation, Binding: binding, Evidence: evidence})
		}
	}
	return out, nil
}

func hasGeneratedMarker(file *ast.File) bool {
	for _, group := range file.Comments {
		if strings.Contains(group.Text(), "Code generated") && strings.Contains(group.Text(), "DO NOT EDIT") {
			return true
		}
	}
	return false
}

func collectStringConstants(file *ast.File, constants map[string]string) {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, raw := range gen.Specs {
			spec, ok := raw.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range spec.Names {
				if i >= len(spec.Values) {
					continue
				}
				if value, ok := stringLiteral(spec.Values[i]); ok {
					constants[name.Name] = value
				}
			}
		}
	}
}

func collectClientConstructors(file *ast.File, concreteToInterface map[string]string) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "New") || fn.Type.Results == nil || len(fn.Type.Results.List) != 1 || fn.Body == nil {
			continue
		}
		clientType := baseTypeName(fn.Type.Results.List[0].Type)
		if clientType == "" || !strings.HasSuffix(clientType, "Client") {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			result, ok := node.(*ast.ReturnStmt)
			if !ok || len(result.Results) != 1 {
				return true
			}
			if concrete := concreteTypeFromExpr(result.Results[0]); concrete != "" {
				concreteToInterface[concrete] = clientType
			}
			return true
		})
	}
}

func collectStreamModes(file *ast.File, modes map[string][]facts.GrpcStreamingMode) {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, raw := range gen.Specs {
			spec, ok := raw.(*ast.ValueSpec)
			if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 {
				continue
			}
			lit, ok := spec.Values[0].(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, element := range lit.Elts {
				field, ok := element.(*ast.KeyValueExpr)
				if !ok || identName(field.Key) != "Streams" {
					continue
				}
				streams, ok := field.Value.(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, rawStream := range streams.Elts {
					stream, ok := rawStream.(*ast.CompositeLit)
					if !ok {
						continue
					}
					client, server := false, false
					for _, rawProperty := range stream.Elts {
						property, ok := rawProperty.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						value, _ := boolLiteral(property.Value)
						switch identName(property.Key) {
						case "ClientStreams":
							client = value
						case "ServerStreams":
							server = value
						}
					}
					mode := facts.GrpcStreamingUnary
					if client && server {
						mode = facts.GrpcStreamingBidirectional
					} else if client {
						mode = facts.GrpcStreamingClient
					} else if server {
						mode = facts.GrpcStreamingServer
					}
					modes[spec.Names[0].Name] = append(modes[spec.Names[0].Name], mode)
				}
			}
		}
	}
}

func generatedTransport(body *ast.BlockStmt, constants map[string]string, streamModes map[string][]facts.GrpcStreamingMode) (string, facts.GrpcStreamingMode, bool) {
	var fullMethod string
	mode := facts.GrpcStreamingUnary
	ast.Inspect(body, func(node ast.Node) bool {
		if fullMethod != "" {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch selector.Sel.Name {
		case "Invoke":
			if len(call.Args) > 1 {
				fullMethod, _ = resolveString(call.Args[1], constants)
			}
		case "NewStream":
			if len(call.Args) > 2 {
				fullMethod, _ = resolveString(call.Args[2], constants)
				if descriptor, index, ok := streamDescriptor(call.Args[1]); ok && index < len(streamModes[descriptor]) {
					mode = streamModes[descriptor][index]
				}
			}
		}
		return true
	})
	return fullMethod, mode, fullMethod != ""
}

func operationFromFullMethod(fullMethod string, mode facts.GrpcStreamingMode) (facts.GrpcOperationFact, error) {
	if !strings.HasPrefix(fullMethod, "/") {
		return facts.GrpcOperationFact{}, fmt.Errorf("full method must start with /")
	}
	parts := strings.Split(strings.TrimPrefix(fullMethod, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return facts.GrpcOperationFact{}, fmt.Errorf("invalid full method %q", fullMethod)
	}
	serviceParts := strings.Split(parts[0], ".")
	if len(serviceParts) < 2 {
		return facts.GrpcOperationFact{}, fmt.Errorf("service is missing protobuf package")
	}
	return facts.GrpcOperationFact{ID: facts.GrpcOperationID(fullMethod), FullMethod: fullMethod, ProtoPackage: strings.Join(serviceParts[:len(serviceParts)-1], "."), Service: serviceParts[len(serviceParts)-1], Method: parts[1], StreamingMode: mode}, nil
}

func resolveString(expr ast.Expr, constants map[string]string) (string, bool) {
	if value, ok := stringLiteral(expr); ok {
		return value, true
	}
	if ident, ok := expr.(*ast.Ident); ok {
		value, ok := constants[ident.Name]
		return value, ok
	}
	return "", false
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	return value, err == nil
}
func boolLiteral(expr ast.Expr) (bool, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false, false
	}
	return ident.Name == "true", ident.Name == "true" || ident.Name == "false"
}
func identName(expr ast.Expr) string {
	ident, _ := expr.(*ast.Ident)
	if ident == nil {
		return ""
	}
	return ident.Name
}
func baseTypeName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.StarExpr:
		return baseTypeName(x.X)
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return baseTypeName(x.X)
	case *ast.IndexListExpr:
		return baseTypeName(x.X)
	}
	return ""
}
func concreteTypeFromExpr(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.UnaryExpr:
		return concreteTypeFromExpr(x.X)
	case *ast.CompositeLit:
		return baseTypeName(x.Type)
	}
	return ""
}
func streamDescriptor(expr ast.Expr) (string, int, bool) {
	if unary, ok := expr.(*ast.UnaryExpr); ok {
		expr = unary.X
	}
	index, ok := expr.(*ast.IndexExpr)
	if !ok {
		return "", 0, false
	}
	position, ok := index.Index.(*ast.BasicLit)
	if !ok || position.Kind != token.INT {
		return "", 0, false
	}
	value, err := strconv.Atoi(position.Value)
	if err != nil {
		return "", 0, false
	}
	selector, ok := index.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Streams" {
		return "", 0, false
	}
	descriptor, ok := selector.X.(*ast.Ident)
	return descriptor.Name, value, ok
}
func logicalDependencyPath(importPath, path string) string {
	return "dependency/" + importPath + "/" + filepath.Base(path)
}
func appendBindingOnce(items []facts.GrpcClientBinding, item facts.GrpcClientBinding) []facts.GrpcClientBinding {
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}
func appendEvidenceOnce(items []facts.EvidenceFact, item facts.EvidenceFact) []facts.EvidenceFact {
	for _, existing := range items {
		if evidenceKey(existing) == evidenceKey(item) {
			return items
		}
	}
	return append(items, item)
}
func evidenceKey(item facts.EvidenceFact) string {
	return item.Kind + "\x00" + item.Raw + "\x00" + item.Span.File + "\x00" + strconv.Itoa(item.Span.StartLine) + "\x00" + strconv.Itoa(item.Span.StartCol)
}
