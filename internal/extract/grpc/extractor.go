// extractor.go 从项目源码中抽取已精确匹配 generated client binding 的 gRPC 调用。
package grpc

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// CallAmbiguityError 表示 receiver 有多个可证明的 generated operation 候选。
type CallAmbiguityError struct {
	Caller facts.SymbolID
	Span   facts.SourceSpan
}

func (e *CallAmbiguityError) Error() string {
	return fmt.Sprintf("ambiguous generated gRPC call in %s at %s:%d", e.Caller, e.Span.File, e.Span.StartLine)
}

// Extract 遍历项目 non-test source，返回只由唯一 receiver binding 证明的调用事实。
func Extract(p *project.Project, idx *astindex.Index, catalog *Catalog) ([]facts.GrpcCallFact, error) {
	if catalog == nil || len(catalog.ByBinding) == 0 {
		return []facts.GrpcCallFact{}, nil
	}
	var calls []facts.GrpcCallFact
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				caller := functionSymbol(file, fn)
				if caller == "" {
					continue
				}
				scope := buildScope(file, idx, fn)
				var extractErr error
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					if extractErr != nil {
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
					types := scope.resolve(selector.X, call.Pos())
					// 防御性歧义处理：当 receiver 解析出多个候选类型且其中有 catalog 命中时，
					// 报告 CallAmbiguityError 而非静默丢弃。
					//
					// 注意：当前 functionScope.resolve（见下方 resolve/valueTypes 实现）在
					// 单一标识符上最多返回 1 个 ValueType（interface 多实现被
					// resolveUniqueInterfaceBinding 拒绝，map 索引分发无 IndexExpr 分支），
					// 故本分支在现有架构下不可达。保留它是为未来 resolve 能力扩展
					// （如 map 值接口分发、多返回值 constructor）做防御；届时仅需保证
					// 歧义被 surface 而非静默丢失。详见 TestCallAmbiguityErrorFormatting。
					if len(types) > 1 {
						matched := 0
						for _, t := range types {
							k := BindingKey{GoPackage: t.PackagePath, ClientType: t.TypeName, GoMethod: selector.Sel.Name}
							if _, ok := catalog.Lookup(k); ok {
								matched++
							}
						}
						if matched > 0 {
							span := relativeSpan(p.Root, file, call.Pos(), call.End())
							extractErr = &CallAmbiguityError{Caller: caller, Span: span}
							return false
						}
						return true
					}
					if len(types) == 0 {
						return true
					}
					key := BindingKey{GoPackage: types[0].PackagePath, ClientType: types[0].TypeName, GoMethod: selector.Sel.Name}
					entry, ok := catalog.Lookup(key)
					if !ok {
						return true
					}
					span := relativeSpan(p.Root, file, call.Pos(), call.End())
					calls = append(calls, facts.GrpcCallFact{
						ID:           fmt.Sprintf("grpc_call:%s:%s:%d:%d", caller, entry.Operation.ID, span.StartLine, span.StartCol),
						CallerSymbol: caller, OperationID: entry.Operation.ID, ClientBinding: entry.Binding, Span: span,
						Evidence: []facts.EvidenceFact{{Kind: "grpc_call_expression", Raw: selector.Sel.Name, Span: span, Confidence: facts.ConfidenceHigh}, entry.Evidence},
					})
					return true
				})
				if extractErr != nil {
					return nil, extractErr
				}
			}
		}
	}
	sort.Slice(calls, func(i, j int) bool { return calls[i].ID < calls[j].ID })
	return dedupeCalls(calls), nil
}

type functionScope struct {
	file   *project.File
	idx    *astindex.Index
	locals map[*ast.Object][]astindex.ValueType
	names  map[string][]scopedType
}

type scopedType struct {
	pos   token.Pos
	types []astindex.ValueType
}

func buildScope(file *project.File, idx *astindex.Index, fn *ast.FuncDecl) *functionScope {
	scope := &functionScope{file: file, idx: idx, locals: map[*ast.Object][]astindex.ValueType{}, names: map[string][]scopedType{}}
	addFields := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			typeValue := astindex.ValueTypeFromTypeExpr(file, field.Type)
			for _, name := range field.Names {
				scope.add(name, name.Pos(), oneType(typeValue))
			}
		}
	}
	addFields(fn.Recv)
	addFields(fn.Type.Params)
	addFields(fn.Type.Results)
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch stmt := node.(type) {
		case *ast.AssignStmt:
			if stmt.Tok != token.DEFINE {
				return true
			}
			for i, left := range stmt.Lhs {
				name, ok := left.(*ast.Ident)
				if !ok || len(stmt.Rhs) == 0 {
					continue
				}
				value := stmt.Rhs[minIndex(i, len(stmt.Rhs)-1)]
				scope.add(name, name.Pos(), scope.valueTypes(value))
			}
		case *ast.DeclStmt:
			decl, ok := stmt.Decl.(*ast.GenDecl)
			if !ok || decl.Tok != token.VAR {
				return true
			}
			for _, raw := range decl.Specs {
				spec, ok := raw.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range spec.Names {
					types := oneType(astindex.ValueTypeFromTypeExpr(file, spec.Type))
					if len(types) == 0 && len(spec.Values) > 0 {
						types = scope.valueTypes(spec.Values[minIndex(i, len(spec.Values)-1)])
					}
					scope.add(name, name.Pos(), types)
				}
			}
		}
		return true
	})
	return scope
}

func (s *functionScope) add(name *ast.Ident, pos token.Pos, types []astindex.ValueType) {
	if name == nil || name.Name == "" || name.Name == "_" || len(types) == 0 {
		return
	}
	if name.Obj != nil {
		s.locals[name.Obj] = types
	}
	s.names[name.Name] = append(s.names[name.Name], scopedType{pos: pos, types: types})
}

func (s *functionScope) resolve(expr ast.Expr, pos token.Pos) []astindex.ValueType {
	switch value := expr.(type) {
	case *ast.ParenExpr:
		return s.resolve(value.X, pos)
	case *ast.Ident:
		if value.Obj != nil {
			if types, ok := s.locals[value.Obj]; ok {
				return types
			}
		}
		if entries := s.names[value.Name]; len(entries) > 0 {
			var best scopedType
			for _, entry := range entries {
				if entry.pos <= pos && (best.pos == token.NoPos || entry.pos > best.pos) {
					best = entry
				}
			}
			if len(best.types) > 0 {
				return best.types
			}
		}
		if id := astindex.ValueSymbolID("var", s.file.Package.Path, value.Name); s.idx.ValueReceiverTypes[string(id)].TypeName != "" {
			return []astindex.ValueType{s.idx.ValueReceiverTypes[string(id)]}
		}
	case *ast.SelectorExpr:
		parents := s.resolve(value.X, pos)
		var out []astindex.ValueType
		for _, parent := range parents {
			fields := s.idx.StructFieldTypes[astindex.TypeSymbolID(parent.PackagePath, parent.TypeName)]
			if field := fields[value.Sel.Name]; field.TypeName != "" {
				out = append(out, field)
			}
		}
		return uniqueTypes(out)
	case *ast.CallExpr:
		return s.valueTypes(value)
	}
	return nil
}

func (s *functionScope) valueTypes(expr ast.Expr) []astindex.ValueType {
	switch value := expr.(type) {
	case *ast.UnaryExpr:
		return s.valueTypes(value.X)
	case *ast.CompositeLit:
		return oneType(astindex.ValueTypeFromTypeExpr(s.file, value.Type))
	case *ast.CallExpr:
		if typ, ok := s.idx.ResolveBuiltinNewType(s.file, value); ok {
			return []astindex.ValueType{typ}
		}
		if typ := genericTypeArgument(s.file, value.Fun); typ.TypeName != "" {
			return []astindex.ValueType{typ}
		}
		if id := s.callableID(value.Fun); id != "" {
			if typ := s.idx.CallableReturnTypes[id]; typ.TypeName != "" {
				return []astindex.ValueType{typ}
			}
		}
	}
	return nil
}

func genericTypeArgument(file *project.File, fun ast.Expr) astindex.ValueType {
	switch value := fun.(type) {
	case *ast.IndexExpr:
		return astindex.ValueTypeFromTypeExpr(file, value.Index)
	case *ast.IndexListExpr:
		if len(value.Indices) == 1 {
			return astindex.ValueTypeFromTypeExpr(file, value.Indices[0])
		}
	}
	return astindex.ValueType{}
}

func (s *functionScope) callableID(fun ast.Expr) facts.SymbolID {
	switch value := unwrapCallee(fun).(type) {
	case *ast.Ident:
		return astindex.FunctionSymbolID(s.file.Package.Path, value.Name)
	case *ast.SelectorExpr:
		if imported, ok := value.X.(*ast.Ident); ok && s.file.Imports[imported.Name] != "" {
			return astindex.FunctionSymbolID(s.file.Imports[imported.Name], value.Sel.Name)
		}
		if receiver := s.resolve(value.X, value.Pos()); len(receiver) == 1 {
			return astindex.MethodSymbolID(receiver[0].PackagePath, receiver[0].TypeName, value.Sel.Name)
		}
	}
	return ""
}

func unwrapCallee(expr ast.Expr) ast.Expr {
	switch value := expr.(type) {
	case *ast.IndexExpr:
		return unwrapCallee(value.X)
	case *ast.IndexListExpr:
		return unwrapCallee(value.X)
	case *ast.ParenExpr:
		return unwrapCallee(value.X)
	}
	return expr
}
func functionSymbol(file *project.File, fn *ast.FuncDecl) facts.SymbolID {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return astindex.FunctionSymbolID(file.Package.Path, fn.Name.Name)
	}
	receiver := astindex.ValueTypeFromTypeExpr(file, fn.Recv.List[0].Type)
	if receiver.TypeName == "" {
		return ""
	}
	return astindex.MethodSymbolID(file.Package.Path, receiver.TypeName, fn.Name.Name)
}
func relativeSpan(root string, file *project.File, start, end token.Pos) facts.SourceSpan {
	span := astindex.SourceSpanFor(file.FileSet, start, end)
	rel, err := filepath.Rel(root, span.File)
	if err == nil {
		span.File = filepath.ToSlash(rel)
	}
	return span
}
func oneType(typ astindex.ValueType) []astindex.ValueType {
	if typ.TypeName == "" {
		return nil
	}
	return []astindex.ValueType{typ}
}
func uniqueTypes(types []astindex.ValueType) []astindex.ValueType {
	seen := map[string]bool{}
	var out []astindex.ValueType
	for _, typ := range types {
		key := typ.PackagePath + "\x00" + typ.TypeName
		if typ.TypeName != "" && !seen[key] {
			seen[key] = true
			out = append(out, typ)
		}
	}
	return out
}
func minIndex(value, max int) int {
	if value > max {
		return max
	}
	return value
}
func dedupeCalls(calls []facts.GrpcCallFact) []facts.GrpcCallFact {
	out := calls[:0]
	seen := map[string]bool{}
	for _, call := range calls {
		if !seen[call.ID] {
			seen[call.ID] = true
			out = append(out, call)
		}
	}
	return out
}
