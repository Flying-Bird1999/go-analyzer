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

// parserObject is used only for lexical shadowing on assignment targets.
type parserObject = ast.Object //nolint:staticcheck // go/parser still populates this relation without type checking.

type Index struct {
	Project             *project.Project
	Symbols             map[facts.SymbolID]facts.SymbolFact
	ValueReceiverTypes  map[string]ValueType
	CallableReturnTypes map[facts.SymbolID]ValueType
	StructFieldTypes    map[facts.SymbolID]map[string]ValueType
	InterfaceTypes      map[facts.SymbolID]struct{}
	InterfaceBindings   map[facts.SymbolID]*InterfaceBinding
	MapValueTypes       map[facts.SymbolID][]ValueType
	packageValueObjects map[*parserObject]facts.SymbolID
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

func (idx *Index) indexGenDecl(p *project.Project, pkg *project.Package, file *project.File, decl *ast.GenDecl) {
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			id := TypeSymbolID(pkg.Path, s.Name.Name)
			idx.Symbols[id] = symbolFact(p, file, id, "type", pkg.Path, "", s.Name.Name, s.Pos(), s.End())
			if _, ok := s.Type.(*ast.InterfaceType); ok {
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
					idx.packageValueObjects[name.Obj] = id
				}
			}
		}
	}
}

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

func (idx *Index) staticMapConcreteValueTypes(file *project.File, expr ast.Expr) ([]ValueType, bool) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	mapType, ok := lit.Type.(*ast.MapType)
	if !ok {
		return nil, false
	}
	declaredValueType := valueTypeFromTypeExpr(file, mapType.Value)
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

func (idx *Index) concreteValueTypeFromExpr(file *project.File, expr ast.Expr) ValueType {
	if call, ok := expr.(*ast.CallExpr); ok {
		if valueType, ok := idx.ResolveBuiltinNewType(file, call); ok {
			return valueType
		}
	}
	return valueTypeFromExpr(file, expr)
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
		idx.indexCallableReturnType(file, id, decl)
		return
	}
	receiver := receiverTypeName(decl.Recv.List[0].Type)
	id := MethodSymbolID(pkg.Path, receiver, decl.Name.Name)
	idx.Symbols[id] = symbolFact(p, file, id, "method", pkg.Path, receiver, decl.Name.Name, decl.Pos(), decl.End())
	idx.indexCallableReturnType(file, id, decl)
}

func (idx *Index) indexCallableReturnType(file *project.File, id facts.SymbolID, decl *ast.FuncDecl) {
	if decl.Type.Results == nil || len(decl.Type.Results.List) == 0 {
		return
	}
	valueType := valueTypeFromTypeExpr(file, decl.Type.Results.List[0].Type)
	if valueType.TypeName != "" {
		idx.CallableReturnTypes[id] = valueType
	}
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

func (idx *Index) valueTypeFromValueSpec(file *project.File, spec *ast.ValueSpec, index int) ValueType {
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
	if call, ok := spec.Values[valueIndex].(*ast.CallExpr); ok {
		if valueType, ok := idx.ResolveBuiltinNewType(file, call); ok {
			return valueType
		}
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

// ResolveBuiltinNewType resolves new(T) only when new is not shadowed.
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
			return ValueType{}, false
		}
	}
	valueType := valueTypeFromTypeExpr(file, call.Args[0])
	return valueType, valueType.TypeName != ""
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
	valueType, ok := idx.ResolveSelectorReceiverType(file, parts)
	if !ok {
		return ResolvedSymbol{}, false
	}
	methodID := MethodSymbolID(valueType.PackagePath, valueType.TypeName, parts[len(parts)-1])
	_, ok = idx.Symbols[methodID]
	return ResolvedSymbol{ID: methodID, Confidence: valueType.Confidence}, ok
}

// ResolveSelectorReceiverType returns the type that owns the final method in a selector chain.
func (idx *Index) ResolveSelectorReceiverType(file *project.File, parts []string) (ValueType, bool) {
	if file == nil || len(parts) < 2 {
		return ValueType{}, false
	}
	var packagePath, varName string
	var selectors []string
	if importPath := file.Imports[parts[0]]; importPath != "" {
		if len(parts) < 3 {
			return ValueType{}, false
		}
		packagePath = importPath
		varName = parts[1]
		selectors = parts[2:]
	} else {
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
	for _, fieldName := range selectors[:len(selectors)-1] {
		typeID := TypeSymbolID(valueType.PackagePath, valueType.TypeName)
		fields := idx.StructFieldTypes[typeID]
		nextType, ok := fields[fieldName]
		if !ok {
			return ValueType{}, false
		}
		nextType.Confidence = combineConfidence(valueType.Confidence, nextType.Confidence)
		valueType = nextType
	}
	return valueType, true
}

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

func (idx *Index) ResolveMapIndexValueTypes(file *project.File, expr *ast.IndexExpr) ([]ValueType, bool) {
	if file == nil || expr == nil {
		return nil, false
	}
	parts := selectorParts(expr.X)
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
	return append([]ValueType(nil), valueTypes...), true
}

func selectorParts(expr ast.Expr) []string {
	switch x := expr.(type) {
	case *ast.Ident:
		return []string{x.Name}
	case *ast.SelectorExpr:
		return append(selectorParts(x.X), x.Sel.Name)
	default:
		return nil
	}
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
