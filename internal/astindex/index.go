package astindex

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

type Index struct {
	Project          *project.Project
	Symbols          map[facts.SymbolID]facts.SymbolFact
	VarReceiverTypes map[string]ValueType
	StructFieldTypes map[facts.SymbolID]map[string]ValueType
}

type ValueType struct {
	PackagePath string
	TypeName    string
	Confidence  facts.Confidence
}

type ResolvedSymbol struct {
	ID         facts.SymbolID
	Confidence facts.Confidence
}

func Build(p *project.Project) (*Index, error) {
	idx := &Index{
		Project:          p,
		Symbols:          map[facts.SymbolID]facts.SymbolFact{},
		VarReceiverTypes: map[string]ValueType{},
		StructFieldTypes: map[facts.SymbolID]map[string]ValueType{},
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
	return idx, nil
}

func (idx *Index) indexGenDecl(p *project.Project, pkg *project.Package, file *project.File, decl *ast.GenDecl) {
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			id := TypeSymbolID(pkg.Path, s.Name.Name)
			idx.Symbols[id] = symbolFact(p, file, id, "type", pkg.Path, "", s.Name.Name, s.Pos(), s.End())
			idx.indexStructFields(file, id, s)
		case *ast.ValueSpec:
			kind := valueKind(decl.Tok)
			if kind == "" {
				continue
			}
			for i, name := range s.Names {
				id := ValueSymbolID(kind, pkg.Path, name.Name)
				idx.Symbols[id] = symbolFact(p, file, id, kind, pkg.Path, "", name.Name, s.Pos(), s.End())
				if kind == "var" {
					if valueType := valueTypeFromValueSpec(file, s, i); valueType.TypeName != "" {
						idx.VarReceiverTypes[string(id)] = valueType
					}
				}
			}
		}
	}
}

func (idx *Index) indexStructFields(file *project.File, id facts.SymbolID, spec *ast.TypeSpec) {
	structType, ok := spec.Type.(*ast.StructType)
	if !ok {
		return
	}
	fields := map[string]ValueType{}
	for _, field := range structType.Fields.List {
		valueType := valueTypeFromTypeExpr(file, field.Type)
		if valueType.TypeName == "" {
			continue
		}
		if len(field.Names) == 0 {
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

func (idx *Index) indexFuncDecl(p *project.Project, pkg *project.Package, file *project.File, decl *ast.FuncDecl) {
	if decl.Recv == nil || len(decl.Recv.List) == 0 {
		id := FunctionSymbolID(pkg.Path, decl.Name.Name)
		idx.Symbols[id] = symbolFact(p, file, id, "func", pkg.Path, "", decl.Name.Name, decl.Pos(), decl.End())
		return
	}
	receiver := receiverTypeName(decl.Recv.List[0].Type)
	id := MethodSymbolID(pkg.Path, receiver, decl.Name.Name)
	idx.Symbols[id] = symbolFact(p, file, id, "method", pkg.Path, receiver, decl.Name.Name, decl.Pos(), decl.End())
}

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

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.IndexExpr:
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	default:
		return ""
	}
}

func valueTypeFromValueSpec(file *project.File, spec *ast.ValueSpec, index int) ValueType {
	if spec.Type != nil {
		return valueTypeFromTypeExpr(file, spec.Type)
	}
	if len(spec.Values) == 0 {
		return ValueType{}
	}
	valueIndex := index
	if valueIndex >= len(spec.Values) {
		valueIndex = 0
	}
	return valueTypeFromExpr(file, spec.Values[valueIndex])
}

func valueTypeFromExpr(file *project.File, expr ast.Expr) ValueType {
	switch x := expr.(type) {
	case *ast.UnaryExpr:
		return valueTypeFromExpr(file, x.X)
	case *ast.CompositeLit:
		return valueTypeFromTypeExpr(file, x.Type)
	case *ast.CallExpr:
		valueType := valueTypeFromTypeExpr(file, x.Fun)
		name := valueType.TypeName
		if strings.HasPrefix(name, "New") && len(name) > len("New") {
			valueType.TypeName = strings.TrimPrefix(name, "New")
		}
		valueType.Confidence = facts.ConfidenceMedium
		return valueType
	default:
		return ValueType{}
	}
}

func valueTypeFromTypeExpr(file *project.File, expr ast.Expr) ValueType {
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
		return valueTypeFromTypeExpr(file, x.X)
	case *ast.ParenExpr:
		return valueTypeFromTypeExpr(file, x.X)
	case *ast.IndexExpr:
		return valueTypeFromTypeExpr(file, x.X)
	case *ast.IndexListExpr:
		return valueTypeFromTypeExpr(file, x.X)
	default:
		return ValueType{}
	}
}

func (idx *Index) ResolveSelectorMethod(file *project.File, parts []string) (facts.SymbolID, bool) {
	resolved, ok := idx.ResolveSelectorMethodWithConfidence(file, parts)
	return resolved.ID, ok
}

func (idx *Index) ResolveSelectorMethodWithConfidence(file *project.File, parts []string) (ResolvedSymbol, bool) {
	if file == nil || len(parts) < 2 {
		return ResolvedSymbol{}, false
	}
	var packagePath, varName string
	var selectors []string
	if importPath := file.Imports[parts[0]]; importPath != "" {
		if len(parts) < 3 {
			return ResolvedSymbol{}, false
		}
		packagePath = importPath
		varName = parts[1]
		selectors = parts[2:]
	} else {
		packagePath = file.Package.Path
		varName = parts[0]
		selectors = parts[1:]
	}
	varID := ValueSymbolID("var", packagePath, varName)
	valueType, ok := idx.VarReceiverTypes[string(varID)]
	if !ok || valueType.TypeName == "" || len(selectors) == 0 {
		return ResolvedSymbol{}, false
	}
	confidence := valueType.Confidence
	for _, fieldName := range selectors[:len(selectors)-1] {
		typeID := TypeSymbolID(valueType.PackagePath, valueType.TypeName)
		fields := idx.StructFieldTypes[typeID]
		valueType, ok = fields[fieldName]
		if !ok {
			return ResolvedSymbol{}, false
		}
		confidence = combineConfidence(confidence, valueType.Confidence)
	}
	methodID := MethodSymbolID(valueType.PackagePath, valueType.TypeName, selectors[len(selectors)-1])
	_, ok = idx.Symbols[methodID]
	return ResolvedSymbol{ID: methodID, Confidence: confidence}, ok
}

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
