// Package dubbo extracts Dubbo-Go provider registrations.
package dubbo

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

type serviceConfig struct {
	interfaceName     string
	version           string
	versionExpression string
	methods           []methodConfig
	span              facts.SourceSpan
	end               token.Pos
	raw               string
}

type methodConfig struct {
	name string
	span facts.SourceSpan
}

// Extract requires ServiceConfig and SetProviderService evidence in the same
// export function before emitting method-level provider facts.
func Extract(p *project.Project, idx *astindex.Index, store *facts.Store) error {
	mappers := methodMappers(p)
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				configs := collectServiceConfigs(p.Root, file, fn)
				if len(configs) == 0 {
					continue
				}
				registration := functionSymbol(file, fn)
				for _, config := range configs {
					providerExpr, ok := providerServiceExpressionAfter(fn, config.end)
					if !ok {
						continue
					}
					providerType, ok := resolveProviderType(file, idx, fn, providerExpr)
					if !ok {
						continue
					}
					mapper := mappers[typeKey(providerType.PackagePath, providerType.TypeName)]
					for _, method := range config.methods {
						goMethod, ok := mapper[method.name]
						if !ok {
							goMethod, ok = uniqueGoMethod(idx, providerType, method.name)
						}
						if !ok {
							continue
						}
						handler := astindex.MethodSymbolID(providerType.PackagePath, providerType.TypeName, goMethod)
						if _, ok := idx.Symbols[handler]; !ok {
							continue
						}
						store.DubboProviders = append(store.DubboProviders, facts.DubboProviderFact{
							ID: facts.DubboProviderID(config.interfaceName, method.name, method.span), Interface: config.interfaceName,
							Version: config.version, VersionExpression: config.versionExpression, Method: method.name, GoMethod: goMethod,
							ImplementationType: providerType.TypeName, HandlerSymbol: handler, RegistrationSymbol: registration,
							Span: method.span, ServiceSpan: config.span, Confidence: providerType.Confidence,
							Evidence: []facts.EvidenceFact{{Kind: "dubbo_service_config", Raw: config.raw, Span: config.span, Confidence: facts.ConfidenceHigh}},
						})
					}
				}
			}
		}
	}
	return nil
}

func collectServiceConfigs(root string, file *project.File, fn *ast.FuncDecl) []serviceConfig {
	var out []serviceConfig
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		lit, ok := node.(*ast.CompositeLit)
		if !ok || !strings.HasSuffix(typeExpression(lit.Type), "ServiceConfig") {
			return true
		}
		config := serviceConfig{span: sourceSpan(root, file, lit), end: lit.End(), raw: expression(lit)}
		for _, element := range lit.Elts {
			kv, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			switch key.Name {
			case "Interface":
				config.interfaceName, _ = stringLiteral(kv.Value)
			case "Version":
				if config.version, ok = stringLiteral(kv.Value); !ok {
					config.versionExpression = expression(kv.Value)
				}
			case "Methods":
				config.methods = methodNames(root, file, kv.Value)
			}
		}
		if config.interfaceName != "" && len(config.methods) > 0 {
			out = append(out, config)
		}
		return false
	})
	return out
}

func methodNames(root string, file *project.File, expr ast.Expr) []methodConfig {
	seen := map[string]bool{}
	var out []methodConfig
	ast.Inspect(expr, func(node ast.Node) bool {
		lit, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		found := false
		for _, element := range lit.Elts {
			kv, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "Name" {
				continue
			}
			name, ok := stringLiteral(kv.Value)
			if ok && !seen[name] {
				seen[name] = true
				out = append(out, methodConfig{name: name, span: sourceSpan(root, file, lit)})
			}
			found = true
			break
		}
		return !found
	})
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// providerServiceExpressionAfter binds a ServiceConfig to the first following
// SetProviderService call in the same export function. Exports can contain
// unrelated registrations before a config, so choosing the first call in the
// function would bind the config to the wrong implementation.
func providerServiceExpressionAfter(fn *ast.FuncDecl, after token.Pos) (ast.Expr, bool) {
	var out ast.Expr
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if out != nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 || !strings.HasSuffix(expression(call.Fun), ".SetProviderService") {
			return true
		}
		if call.Pos() <= after {
			return true
		}
		out = call.Args[0]
		return false
	})
	return out, out != nil
}

func resolveProviderType(file *project.File, idx *astindex.Index, fn *ast.FuncDecl, expr ast.Expr) (astindex.ValueType, bool) {
	if valueType, ok := directValueType(file, expr); ok {
		return valueType, true
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return astindex.ValueType{}, false
	}
	if valueType := idx.ValueReceiverTypes[string(astindex.ValueSymbolID("var", file.Package.Path, ident.Name))]; valueType.TypeName != "" {
		return valueType, true
	}
	var found astindex.ValueType
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			name, ok := lhs.(*ast.Ident)
			if !ok || name.Name != ident.Name || i >= len(assign.Rhs) {
				continue
			}
			if valueType, ok := directValueType(file, assign.Rhs[i]); ok {
				found = valueType
				return false
			}
		}
		return true
	})
	return found, found.TypeName != ""
}

func directValueType(file *project.File, expr ast.Expr) (astindex.ValueType, bool) {
	switch x := expr.(type) {
	case *ast.UnaryExpr:
		return directValueType(file, x.X)
	case *ast.CompositeLit:
		valueType := astindex.ValueTypeFromTypeExpr(file, x.Type)
		return valueType, valueType.TypeName != ""
	case *ast.CallExpr:
		if ident, ok := x.Fun.(*ast.Ident); ok && ident.Name == "new" && len(x.Args) == 1 {
			valueType := astindex.ValueTypeFromTypeExpr(file, x.Args[0])
			return valueType, valueType.TypeName != ""
		}
	}
	return astindex.ValueType{}, false
}

func methodMappers(p *project.Project) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name != "MethodMapper" || fn.Body == nil || fn.Recv == nil || len(fn.Recv.List) == 0 {
					continue
				}
				key := typeKey(pkg.Path, astindex.ReceiverTypeName(fn.Recv.List[0].Type))
				mapping := map[string]string{}
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					lit, ok := node.(*ast.CompositeLit)
					if !ok || !strings.HasPrefix(typeExpression(lit.Type), "map[") {
						return true
					}
					for _, element := range lit.Elts {
						kv, ok := element.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						goMethod, goOK := stringLiteral(kv.Key)
						protocolMethod, protocolOK := stringLiteral(kv.Value)
						if goOK && protocolOK {
							mapping[protocolMethod] = goMethod
						}
					}
					return len(mapping) == 0
				})
				out[key] = mapping
			}
		}
	}
	return out
}

func uniqueGoMethod(idx *astindex.Index, valueType astindex.ValueType, protocolMethod string) (string, bool) {
	var match string
	for _, symbol := range idx.Symbols {
		if symbol.Kind != "method" || symbol.PackagePath != valueType.PackagePath || symbol.Receiver != valueType.TypeName || !strings.EqualFold(symbol.Name, protocolMethod) {
			continue
		}
		if match != "" && match != symbol.Name {
			return "", false
		}
		match = symbol.Name
	}
	return match, match != ""
}

func functionSymbol(file *project.File, fn *ast.FuncDecl) facts.SymbolID {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return astindex.FunctionSymbolID(file.Package.Path, fn.Name.Name)
	}
	return astindex.MethodSymbolID(file.Package.Path, astindex.ReceiverTypeName(fn.Recv.List[0].Type), fn.Name.Name)
}

func sourceSpan(root string, file *project.File, node ast.Node) facts.SourceSpan {
	start, end := file.FileSet.Position(node.Pos()), file.FileSet.Position(node.End())
	rel, err := filepath.Rel(root, file.Path)
	if err != nil {
		rel = file.Path
	}
	return facts.SourceSpan{File: filepath.ToSlash(rel), StartLine: start.Line, StartCol: start.Column, EndLine: end.Line, EndCol: end.Column}
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	return value, err == nil
}

func typeExpression(expr ast.Expr) string { return expression(expr) }

func expression(node any) string {
	astNode, ok := node.(ast.Node)
	if !ok || astNode == nil {
		return ""
	}
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, token.NewFileSet(), astNode)
	return buf.String()
}

func typeKey(packagePath, typeName string) string { return packagePath + "\x00" + typeName }

func sortStrings(items []string) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j] < items[j-1]; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
