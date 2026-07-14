// Package job extracts statically named XXL-Job registrations.
package job

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

var jobValueTypes = map[string]bool{"JobListener": true, "TaskFunc": true}

// Extract records map assignments in functions whose parameters or results
// prove the map value is an XXL-Job handler type.
func Extract(p *project.Project, idx *astindex.Index, store *facts.Store) error {
	constants := stringConstants(p)
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || !isJobRegistrationFunction(file, fn) {
					continue
				}
				registration := functionSymbol(file, fn)
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					if _, nested := node.(*ast.FuncLit); nested {
						return false
					}
					assign, ok := node.(*ast.AssignStmt)
					if !ok {
						return true
					}
					for i, lhs := range assign.Lhs {
						if i >= len(assign.Rhs) {
							continue
						}
						index, ok := lhs.(*ast.IndexExpr)
						if !ok {
							continue
						}
						name, ok := resolveString(file, index.Index, constants)
						if !ok || strings.TrimSpace(name) == "" {
							continue
						}
						handler, confidence, ok := resolveHandler(file, idx, fn, assign.Rhs[i])
						if !ok {
							continue
						}
						span := sourceSpan(p.Root, file, assign)
						store.JobRegistrations = append(store.JobRegistrations, facts.JobRegistrationFact{
							ID: facts.JobRegistrationID(name, span), Name: name, HandlerSymbol: handler,
							RegistrationSymbol: registration, Span: span, Confidence: confidence,
							Evidence: []facts.EvidenceFact{{Kind: "job_registration", Raw: expression(assign), Span: span, Confidence: confidence}},
						})
					}
					return false
				})
			}
		}
	}
	return nil
}

func isJobRegistrationFunction(file *project.File, fn *ast.FuncDecl) bool {
	for _, list := range []*ast.FieldList{fn.Type.Params, fn.Type.Results} {
		if list == nil {
			continue
		}
		for _, field := range list.List {
			mapType, ok := field.Type.(*ast.MapType)
			if !ok || !isStringType(mapType.Key) {
				continue
			}
			valueType := astindex.ValueTypeFromTypeExpr(file, mapType.Value)
			if jobValueTypes[valueType.TypeName] && isJobPackage(valueType.PackagePath) {
				return true
			}
		}
	}
	return false
}

func isStringType(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "string"
}

func isJobPackage(path string) bool {
	path = strings.ToLower(path)
	return strings.Contains(path, "jobx") || strings.Contains(path, "xxljob")
}

func resolveHandler(file *project.File, idx *astindex.Index, fn *ast.FuncDecl, expr ast.Expr) (facts.SymbolID, facts.Confidence, bool) {
	if ident, ok := expr.(*ast.Ident); ok {
		id := astindex.FunctionSymbolID(file.Package.Path, ident.Name)
		_, exists := idx.Symbols[id]
		return id, facts.ConfidenceHigh, exists
	}
	parts := astindex.SelectorParts(expr)
	if len(parts) == 0 {
		return "", "", false
	}
	if importPath := file.Imports[parts[0]]; importPath != "" && len(parts) == 2 {
		id := astindex.FunctionSymbolID(importPath, parts[1])
		if _, ok := idx.Symbols[id]; ok {
			return id, facts.ConfidenceHigh, true
		}
	}
	if resolved, ok := idx.ResolveSelectorMethodWithConfidence(file, parts); ok {
		return resolved.ID, resolved.Confidence, true
	}
	if fn.Recv != nil && len(fn.Recv.List) > 0 && len(fn.Recv.List[0].Names) > 0 && parts[0] == fn.Recv.List[0].Names[0].Name {
		receiver := astindex.ValueTypeFromTypeExpr(file, fn.Recv.List[0].Type)
		remaining := append([]string(nil), parts[1:]...)
		if resolved, ok := idx.ResolveValueTypeMethod(receiver, append(remaining, "Execute")); ok {
			return resolved.ID, resolved.Confidence, true
		}
	}
	if valueType, ok := idx.ResolveSelectorReceiverType(file, append(parts, "Execute")); ok {
		if resolved, ok := idx.ResolveValueTypeMethod(valueType, []string{"Execute"}); ok {
			return resolved.ID, resolved.Confidence, true
		}
	}
	return "", "", false
}

func functionSymbol(file *project.File, fn *ast.FuncDecl) facts.SymbolID {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return astindex.FunctionSymbolID(file.Package.Path, fn.Name.Name)
	}
	return astindex.MethodSymbolID(file.Package.Path, astindex.ReceiverTypeName(fn.Recv.List[0].Type), fn.Name.Name)
}

func stringConstants(p *project.Project) map[facts.SymbolID]string {
	out := map[facts.SymbolID]string{}
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.CONST {
					continue
				}
				for _, spec := range gen.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range value.Names {
						if i >= len(value.Values) {
							continue
						}
						if text, ok := stringLiteral(value.Values[i]); ok {
							out[astindex.ValueSymbolID("const", pkg.Path, name.Name)] = text
						}
					}
				}
			}
		}
	}
	return out
}

func resolveString(file *project.File, expr ast.Expr, constants map[facts.SymbolID]string) (string, bool) {
	if value, ok := stringLiteral(expr); ok {
		return value, true
	}
	switch x := expr.(type) {
	case *ast.Ident:
		value, ok := constants[astindex.ValueSymbolID("const", file.Package.Path, x.Name)]
		return value, ok
	case *ast.SelectorExpr:
		root, ok := x.X.(*ast.Ident)
		if !ok || file.Imports[root.Name] == "" {
			return "", false
		}
		value, ok := constants[astindex.ValueSymbolID("const", file.Imports[root.Name], x.Sel.Name)]
		return value, ok
	default:
		return "", false
	}
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	return value, err == nil
}

func sourceSpan(root string, file *project.File, node ast.Node) facts.SourceSpan {
	start, end := file.FileSet.Position(node.Pos()), file.FileSet.Position(node.End())
	rel, err := filepath.Rel(root, file.Path)
	if err != nil {
		rel = file.Path
	}
	return facts.SourceSpan{File: filepath.ToSlash(rel), StartLine: start.Line, StartCol: start.Column, EndLine: end.Line, EndCol: end.Column}
}

func expression(node ast.Node) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, token.NewFileSet(), node)
	return buf.String()
}
