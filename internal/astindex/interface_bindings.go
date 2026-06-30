package astindex

import (
	"go/ast"
	"go/token"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

type InterfaceBinding struct {
	DeclaredType      ValueType
	ConcreteTypes     map[string]ValueType
	HasUnknownBinding bool
}

func (idx *Index) indexInterfaceBindings() {
	for id, symbol := range idx.Symbols {
		if symbol.Kind != "var" {
			continue
		}
		declaredType, ok := idx.ValueReceiverTypes[string(id)]
		if !ok || !idx.isInterfaceType(declaredType) {
			continue
		}
		idx.InterfaceBindings[id] = &InterfaceBinding{
			DeclaredType:  declaredType,
			ConcreteTypes: map[string]ValueType{},
		}
	}
	for _, pkg := range idx.Project.Packages {
		for _, file := range pkg.Files {
			idx.indexFileInterfaceAssignments(file)
		}
	}
}

func (idx *Index) indexFileInterfaceAssignments(file *project.File) {
	for _, decl := range file.AST.Decls {
		switch node := decl.(type) {
		case *ast.GenDecl:
			if node.Tok == token.VAR {
				idx.indexPackageInitializers(file, node)
			}
		case *ast.FuncDecl:
			if node.Body != nil {
				idx.indexAssignmentBlock(file, node.Body)
			}
		}
	}
}

func (idx *Index) indexPackageInitializers(file *project.File, decl *ast.GenDecl) {
	for _, rawSpec := range decl.Specs {
		spec, ok := rawSpec.(*ast.ValueSpec)
		if !ok || len(spec.Values) == 0 {
			continue
		}
		for i, name := range spec.Names {
			id := ValueSymbolID("var", file.Package.Path, name.Name)
			if _, ok := idx.InterfaceBindings[id]; !ok {
				continue
			}
			valueIndex := i
			if valueIndex >= len(spec.Values) {
				valueIndex = len(spec.Values) - 1
			}
			idx.addInterfaceAssignment(file, id, spec.Values[valueIndex])
		}
	}
}

func (idx *Index) indexAssignmentBlock(file *project.File, block *ast.BlockStmt) {
	ast.Inspect(block, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || assign.Tok != token.ASSIGN || len(assign.Rhs) == 0 {
			return true
		}
		for i, lhs := range assign.Lhs {
			id, ok := idx.packageVariableAssignmentTarget(file, lhs)
			if !ok {
				continue
			}
			if _, ok := idx.InterfaceBindings[id]; !ok {
				continue
			}
			valueIndex := i
			if valueIndex >= len(assign.Rhs) {
				valueIndex = len(assign.Rhs) - 1
			}
			idx.addInterfaceAssignment(file, id, assign.Rhs[valueIndex])
		}
		return true
	})
}

func (idx *Index) packageVariableAssignmentTarget(file *project.File, expr ast.Expr) (facts.SymbolID, bool) {
	switch target := expr.(type) {
	case *ast.Ident:
		if target.Obj != nil {
			id, ok := idx.packageValueObjects[target.Obj]
			return id, ok
		}
		id := ValueSymbolID("var", file.Package.Path, target.Name)
		_, ok := idx.Symbols[id]
		return id, ok
	case *ast.SelectorExpr:
		root, ok := target.X.(*ast.Ident)
		if !ok || (root.Obj != nil && root.Obj.Kind != ast.Pkg) {
			return "", false
		}
		importPath := file.Imports[root.Name]
		if importPath == "" {
			return "", false
		}
		id := ValueSymbolID("var", importPath, target.Sel.Name)
		_, ok = idx.Symbols[id]
		return id, ok
	default:
		return "", false
	}
}

func (idx *Index) addInterfaceAssignment(file *project.File, id facts.SymbolID, expr ast.Expr) {
	ident, isIdent := expr.(*ast.Ident)
	if isIdent && ident.Name == "nil" {
		return
	}
	binding := idx.InterfaceBindings[id]
	var valueType ValueType
	if call, ok := expr.(*ast.CallExpr); ok {
		valueType, _ = idx.ResolveBuiltinNewType(file, call)
	}
	if valueType.TypeName == "" {
		valueType = valueTypeFromExpr(file, expr)
	}
	if valueType.TypeName == "" || valueType.Confidence != facts.ConfidenceHigh || idx.isInterfaceType(valueType) {
		binding.HasUnknownBinding = true
		return
	}
	binding.ConcreteTypes[valueTypeKey(valueType)] = valueType
}

func (idx *Index) resolveUniqueInterfaceBinding(id facts.SymbolID) (ValueType, bool) {
	binding := idx.InterfaceBindings[id]
	if binding == nil || binding.HasUnknownBinding || len(binding.ConcreteTypes) != 1 {
		return ValueType{}, false
	}
	for _, valueType := range binding.ConcreteTypes {
		if valueType.Confidence == facts.ConfidenceHigh {
			return valueType, true
		}
	}
	return ValueType{}, false
}

func (idx *Index) isInterfaceType(valueType ValueType) bool {
	_, ok := idx.InterfaceTypes[TypeSymbolID(valueType.PackagePath, valueType.TypeName)]
	return ok
}

func valueTypeKey(valueType ValueType) string {
	return valueType.PackagePath + "\x00" + valueType.TypeName
}
