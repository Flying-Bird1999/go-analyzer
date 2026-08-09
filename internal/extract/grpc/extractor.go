// extractor.go 从项目源码中直接抽取 gRPC client 调用，不读取任何依赖包或生成代码。
//
// 识别规则是 protoc-gen-go-grpc 的命名契约（真实项目验证：366 个调用点，0 例外）：
// generated 的 client 接口永远命名为 <Service>Client，且总是在调用方所在包之外的
// 另一个包里声明。调用点形如 `x.M(...)`，若 x 的静态类型满足这条契约，就把
// TypeName 去掉 Client 后缀得到 Service，PackagePath 就是生成包的 import 路径，
// M 就是 GoMethod——三者拼成 canonical identity，不需要外部证据。
//
// 单靠命名后缀会把手写的 Redis/HTTP client 封装（同样以 Client 结尾、来自外部包）
// 一并误判成 gRPC 调用——真实项目验证过这类误判，因此额外要求生成包 import 路径以
// 公司内网域名 gopkg.inshopline.com/ 开头，且不属于被分析项目自己的 module（后者
// 兜底属于自己 module 但恰好落在该域名下的手写包装类型，例如 sc1-server 内部的
// ConversationOnlineClient）。两条都是从调用方自己的 import 路径/go.mod 就能证明
// 的信息，不需要读取外部包内容。
package grpc

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// clientTypeSuffix 是 protoc-gen-go-grpc 对生成 client 接口的固定命名后缀。
const clientTypeSuffix = "Client"

// generatedPackageDomain 是公司内网 proto 生成包统一使用的 module 域名。真实项目
// 验证：sl-sc1-admin-bff / sl-sc1-bff-service / sl-sc2-admin-bff / sc1-server /
// sc2-server 五个仓库里，凡是真正的 gRPC 生成包（含 7 个跨团队服务：ai/chatbot、
// sc/background、armor（两个）、member、product、billing）无一例外都在这个域名下；
// 反之 Redis/HTTP client 封装（github.com/go-redis/redis、
// gopkg.inshopline.com/commons/httpclientx/v2 等）以及项目自己手写的同名类型
// （sc1-admin-bff/remote/oa 等）都不在其下。
const generatedPackageDomain = "gopkg.inshopline.com/"

// CallAmbiguityError 表示 receiver 有多个可证明的 gRPC client 候选。
type CallAmbiguityError struct {
	Caller facts.SymbolID
	Span   facts.SourceSpan
}

func (e *CallAmbiguityError) Error() string {
	return fmt.Sprintf("ambiguous generated gRPC call in %s at %s:%d", e.Caller, e.Span.File, e.Span.StartLine)
}

// Extract 遍历项目 non-test source，直接从调用点推出 gRPC operation 与调用事实。
func Extract(p *project.Project, idx *astindex.Index) ([]facts.GrpcOperationFact, []facts.GrpcCallFact, error) {
	operations := map[string]facts.GrpcOperationFact{}
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
					// 防御性歧义处理：receiver 解析出多个候选类型且其中有满足 gRPC client
					// 命名契约的，报告 CallAmbiguityError 而非静默挑一个猜。
					//
					// 注意：当前 functionScope.resolve 在单一标识符上最多返回 1 个
					// ValueType（interface 多实现被 resolveUniqueInterfaceBinding 拒绝，
					// map 索引分发无 IndexExpr 分支），故本分支在现有架构下不可达，保留
					// 用于未来 resolve 能力扩展时的防御。详见 TestCallAmbiguityErrorFormatting。
					if len(types) > 1 {
						matched := 0
						for _, t := range types {
							if _, ok := clientService(t, pkg.Path, p.ModulePath); ok {
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
					service, ok := clientService(types[0], pkg.Path, p.ModulePath)
					if !ok {
						return true
					}
					span := relativeSpan(p.Root, file, call.Pos(), call.End())
					identity := facts.GrpcIdentity(types[0].PackagePath, service, selector.Sel.Name)
					operationID := facts.GrpcOperationID(identity)
					evidence := facts.EvidenceFact{Kind: "grpc_call_expression", Raw: selector.Sel.Name, Span: span}
					operation := operations[operationID]
					if operation.ID == "" {
						operation = facts.GrpcOperationFact{ID: operationID, Identity: identity, GoPackage: types[0].PackagePath, Service: service, GoMethod: selector.Sel.Name}
					}
					operation.Evidence = appendEvidenceOnce(operation.Evidence, evidence)
					operations[operationID] = operation
					calls = append(calls, facts.GrpcCallFact{
						ID:           fmt.Sprintf("grpc_call:%s:%s:%d:%d", caller, operationID, span.StartLine, span.StartCol),
						CallerSymbol: caller, OperationID: operationID, Span: span,
						Evidence: []facts.EvidenceFact{evidence},
					})
					return true
				})
				if extractErr != nil {
					return nil, nil, extractErr
				}
			}
		}
	}
	return sortedOperations(operations), sortedCalls(calls), nil
}

// clientService 判断 t 是否满足 gRPC client 命名契约，满足则返回 Service 名。
// 四个条件全部要求同时成立：
//  1. 类型来自调用方所在包之外的另一个包——generated client 接口从不与消费它的
//     业务代码同包声明；
//  2. 类型名以 Client 结尾；
//  3. 类型所在包的 import 路径以公司内网域名 gopkg.inshopline.com/ 开头——真实
//     生成包统一挂在这个域名下，Redis/HTTP client 封装等手写类型不在其下；
//  4. 类型所在包不属于被分析项目自己的 module——兜底排除"项目自己手写的包装类型
//     恰好落在 gopkg.inshopline.com 域名下"这种情况（如 sc1-server 内部的
//     ConversationOnlineClient）。
func clientService(t astindex.ValueType, callerPackagePath, projectModulePath string) (string, bool) {
	if t.PackagePath == "" || t.PackagePath == callerPackagePath || !strings.HasSuffix(t.TypeName, clientTypeSuffix) {
		return "", false
	}
	if !strings.HasPrefix(t.PackagePath, generatedPackageDomain) {
		return "", false
	}
	if projectModulePath != "" && (t.PackagePath == projectModulePath || strings.HasPrefix(t.PackagePath, projectModulePath+"/")) {
		return "", false
	}
	service := strings.TrimSuffix(t.TypeName, clientTypeSuffix)
	if service == "" {
		return "", false
	}
	return service, true
}

func sortedOperations(operations map[string]facts.GrpcOperationFact) []facts.GrpcOperationFact {
	out := make([]facts.GrpcOperationFact, 0, len(operations))
	for _, operation := range operations {
		sort.Slice(operation.Evidence, func(i, j int) bool { return evidenceKey(operation.Evidence[i]) < evidenceKey(operation.Evidence[j]) })
		out = append(out, operation)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func sortedCalls(calls []facts.GrpcCallFact) []facts.GrpcCallFact {
	sort.Slice(calls, func(i, j int) bool { return calls[i].ID < calls[j].ID })
	return dedupeCalls(calls)
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
		if len(parents) == 0 {
			// X 解析不出值类型时，它可能根本不是值而是 import 别名
			// （如 live.ActivityUserClient），此时 Sel 是被导入包的包级变量。
			// ident.Obj == nil 排除被局部声明遮蔽的同名标识符。
			if ident, ok := value.X.(*ast.Ident); ok && ident.Obj == nil {
				if typ, ok := s.idx.ResolvePackageQualifiedValueType(s.file, ident.Name, value.Sel.Name); ok {
					return []astindex.ValueType{typ}
				}
			}
			return nil
		}
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
