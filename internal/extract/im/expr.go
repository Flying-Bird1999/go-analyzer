package im

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

type constDecl struct {
	file     *project.File
	expr     ast.Expr
	typeName string
	iota     int64
}

type evaluator struct {
	project       *project.Project
	index         *astindex.Index
	consts        map[facts.SymbolID]constDecl
	stringTables  map[facts.SymbolID][]string
	stringMethods map[string]facts.SymbolID
}

func newEvaluator(p *project.Project, idx *astindex.Index) *evaluator {
	e := &evaluator{
		project:       p,
		index:         idx,
		consts:        map[facts.SymbolID]constDecl{},
		stringTables:  map[facts.SymbolID][]string{},
		stringMethods: map[string]facts.SymbolID{},
	}
	e.indexDeclarations()
	return e
}

func (e *evaluator) indexDeclarations() {
	for _, pkg := range e.project.Packages {
		for _, file := range pkg.Files {
			for _, rawDecl := range file.AST.Decls {
				switch decl := rawDecl.(type) {
				case *ast.GenDecl:
					e.indexGenDecl(file, decl)
				case *ast.FuncDecl:
					e.indexStringMethod(file, decl)
				}
			}
		}
	}
}

func (e *evaluator) indexGenDecl(file *project.File, decl *ast.GenDecl) {
	if decl.Tok == token.CONST {
		var previousValues []ast.Expr
		var previousType ast.Expr
		for i, rawSpec := range decl.Specs {
			spec, ok := rawSpec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			values := spec.Values
			if len(values) == 0 {
				values = previousValues
			} else {
				previousValues = values
			}
			typeExpr := spec.Type
			if typeExpr == nil {
				typeExpr = previousType
			} else {
				previousType = typeExpr
			}
			for valueIndex, name := range spec.Names {
				if len(values) == 0 {
					continue
				}
				index := valueIndex
				if index >= len(values) {
					index = len(values) - 1
				}
				e.consts[astindex.ValueSymbolID("const", file.Package.Path, name.Name)] = constDecl{
					file:     file,
					expr:     values[index],
					typeName: localTypeName(typeExpr),
					iota:     int64(i),
				}
			}
		}
		return
	}
	if decl.Tok != token.VAR {
		return
	}
	for _, rawSpec := range decl.Specs {
		spec, ok := rawSpec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range spec.Names {
			if len(spec.Values) == 0 {
				continue
			}
			valueIndex := i
			if valueIndex >= len(spec.Values) {
				valueIndex = len(spec.Values) - 1
			}
			table, ok := staticStringTable(spec.Values[valueIndex])
			if !ok {
				continue
			}
			e.stringTables[astindex.ValueSymbolID("var", file.Package.Path, name.Name)] = table
		}
	}
}

func (e *evaluator) indexStringMethod(file *project.File, fn *ast.FuncDecl) {
	if fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Name.Name != "String" || fn.Body == nil {
		return
	}
	receiverType := astindex.ReceiverTypeName(fn.Recv.List[0].Type)
	if receiverType == "" {
		return
	}
	for _, stmt := range fn.Body.List {
		ret, ok := stmt.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			continue
		}
		indexExpr, ok := ret.Results[0].(*ast.IndexExpr)
		if !ok {
			continue
		}
		table, ok := indexExpr.X.(*ast.Ident)
		if !ok {
			continue
		}
		tableID := astindex.ValueSymbolID("var", file.Package.Path, table.Name)
		if _, ok := e.stringTables[tableID]; !ok {
			continue
		}
		e.stringMethods[typeKey(file.Package.Path, receiverType)] = tableID
	}
}

func (e *evaluator) eventValue(file *project.File, expr ast.Expr) (string, bool) {
	return e.eventValueSeen(file, expr, map[facts.SymbolID]bool{})
}

func (e *evaluator) eventValueSeen(file *project.File, expr ast.Expr, seen map[facts.SymbolID]bool) (string, bool) {
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return "", false
		}
		out, err := strconv.Unquote(value.Value)
		return out, err == nil
	case *ast.Ident:
		id := astindex.ValueSymbolID("const", file.Package.Path, value.Name)
		decl, ok := e.consts[id]
		if !ok || seen[id] {
			return "", false
		}
		nextSeen := copySeen(seen)
		nextSeen[id] = true
		return e.eventValueSeen(decl.file, decl.expr, nextSeen)
	case *ast.SelectorExpr:
		pkg, ok := value.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		importPath := file.Imports[pkg.Name]
		if importPath == "" {
			return "", false
		}
		id := astindex.ValueSymbolID("const", importPath, value.Sel.Name)
		decl, ok := e.consts[id]
		if !ok || seen[id] {
			return "", false
		}
		nextSeen := copySeen(seen)
		nextSeen[id] = true
		return e.eventValueSeen(decl.file, decl.expr, nextSeen)
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", false
		}
		left, leftOK := e.eventValueSeen(file, value.X, seen)
		right, rightOK := e.eventValueSeen(file, value.Y, seen)
		if !leftOK || !rightOK {
			return "", false
		}
		return left + right, true
	case *ast.CallExpr:
		if len(value.Args) == 1 {
			if ident, ok := value.Fun.(*ast.Ident); ok && (ident.Name == "string" || e.localNamedType(file, ident.Name)) {
				return e.eventValueSeen(file, value.Args[0], seen)
			}
		}
		selector, ok := value.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "String" || len(value.Args) != 0 {
			return "", false
		}
		return e.enumStringValue(file, selector.X)
	case *ast.ParenExpr:
		return e.eventValueSeen(file, value.X, seen)
	default:
		return "", false
	}
}

func (e *evaluator) enumStringValue(file *project.File, expr ast.Expr) (string, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	id := astindex.ValueSymbolID("const", file.Package.Path, ident.Name)
	decl, ok := e.consts[id]
	if !ok || decl.typeName == "" {
		return "", false
	}
	index, ok := e.integerValue(decl.file, decl.expr, decl.iota, map[facts.SymbolID]bool{id: true})
	if !ok || index < 0 {
		return "", false
	}
	tableID, ok := e.stringMethods[typeKey(decl.file.Package.Path, decl.typeName)]
	if !ok {
		return "", false
	}
	table := e.stringTables[tableID]
	if index >= int64(len(table)) {
		return "", false
	}
	return table[index], true
}

func (e *evaluator) integerValue(file *project.File, expr ast.Expr, iotaValue int64, seen map[facts.SymbolID]bool) (int64, bool) {
	switch value := expr.(type) {
	case *ast.Ident:
		if value.Name == "iota" {
			return iotaValue, true
		}
		id := astindex.ValueSymbolID("const", file.Package.Path, value.Name)
		decl, ok := e.consts[id]
		if !ok || seen[id] {
			return 0, false
		}
		nextSeen := copySeen(seen)
		nextSeen[id] = true
		return e.integerValue(decl.file, decl.expr, decl.iota, nextSeen)
	case *ast.BasicLit:
		if value.Kind != token.INT {
			return 0, false
		}
		out, err := strconv.ParseInt(value.Value, 0, 64)
		return out, err == nil
	case *ast.ParenExpr:
		return e.integerValue(file, value.X, iotaValue, seen)
	default:
		return 0, false
	}
}

func (e *evaluator) expressionTypeIDs(file *project.File, fn *ast.FuncDecl, expr ast.Expr) []facts.SymbolID {
	types := e.expressionTypes(file, fn, expr)
	ids := make([]facts.SymbolID, 0, len(types))
	seen := map[facts.SymbolID]bool{}
	for _, valueType := range types {
		if valueType.TypeName == "" {
			continue
		}
		id := astindex.TypeSymbolID(valueType.PackagePath, valueType.TypeName)
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (e *evaluator) expressionTypes(file *project.File, fn *ast.FuncDecl, expr ast.Expr) []astindex.ValueType {
	switch value := expr.(type) {
	case *ast.Ident:
		if valueType, ok := functionValueType(file, fn, value.Name); ok {
			return []astindex.ValueType{valueType}
		}
	case *ast.SelectorExpr:
		parents := e.expressionTypes(file, fn, value.X)
		var out []astindex.ValueType
		for _, parent := range parents {
			fields := e.index.StructFieldTypes[astindex.TypeSymbolID(parent.PackagePath, parent.TypeName)]
			if field, ok := fields[value.Sel.Name]; ok {
				out = append(out, field)
			}
		}
		return out
	case *ast.StarExpr:
		return e.expressionTypes(file, fn, value.X)
	case *ast.UnaryExpr:
		return e.expressionTypes(file, fn, value.X)
	case *ast.ParenExpr:
		return e.expressionTypes(file, fn, value.X)
	case *ast.CompositeLit:
		if valueType, ok := typeExprValueType(file, value.Type); ok {
			return []astindex.ValueType{valueType}
		}
	case *ast.CallExpr:
		if len(value.Args) == 1 {
			if valueType, ok := typeExprValueType(file, value.Fun); ok {
				return []astindex.ValueType{valueType}
			}
		}
	}
	return nil
}

func functionValueType(file *project.File, fn *ast.FuncDecl, name string) (astindex.ValueType, bool) {
	if fn == nil {
		return astindex.ValueType{}, false
	}
	for _, fields := range []*ast.FieldList{fn.Recv, fn.Type.Params, fn.Type.Results} {
		if fields == nil {
			continue
		}
		for _, field := range fields.List {
			for _, fieldName := range field.Names {
				if fieldName.Name == name {
					return typeExprValueType(file, field.Type)
				}
			}
		}
	}
	return astindex.ValueType{}, false
}

func typeExprValueType(file *project.File, expr ast.Expr) (astindex.ValueType, bool) {
	switch value := expr.(type) {
	case *ast.Ident:
		return astindex.ValueType{PackagePath: file.Package.Path, TypeName: value.Name, Confidence: facts.ConfidenceHigh}, true
	case *ast.SelectorExpr:
		pkg, ok := value.X.(*ast.Ident)
		if !ok {
			return astindex.ValueType{}, false
		}
		importPath := file.Imports[pkg.Name]
		if importPath == "" {
			return astindex.ValueType{}, false
		}
		return astindex.ValueType{PackagePath: importPath, TypeName: value.Sel.Name, Confidence: facts.ConfidenceHigh}, true
	case *ast.StarExpr:
		return typeExprValueType(file, value.X)
	case *ast.ParenExpr:
		return typeExprValueType(file, value.X)
	case *ast.IndexExpr:
		return typeExprValueType(file, value.X)
	case *ast.IndexListExpr:
		return typeExprValueType(file, value.X)
	default:
		return astindex.ValueType{}, false
	}
}

func staticStringTable(expr ast.Expr) ([]string, bool) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(lit.Elts))
	for _, elt := range lit.Elts {
		value, ok := elt.(*ast.BasicLit)
		if !ok || value.Kind != token.STRING {
			return nil, false
		}
		item, err := strconv.Unquote(value.Value)
		if err != nil {
			return nil, false
		}
		out = append(out, item)
	}
	return out, true
}

func localTypeName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return localTypeName(value.X)
	default:
		return ""
	}
}

func (e *evaluator) localNamedType(file *project.File, name string) bool {
	_, ok := e.index.Symbols[astindex.TypeSymbolID(file.Package.Path, name)]
	return ok
}

func typeKey(packagePath, typeName string) string {
	return filepath.ToSlash(packagePath) + "::" + typeName
}

func copySeen(in map[facts.SymbolID]bool) map[facts.SymbolID]bool {
	out := make(map[facts.SymbolID]bool, len(in)+1)
	for key, value := range in {
		out[key] = value
	}
	return out
}
