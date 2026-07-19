// extractor.go 实现 route 提取的入口与主流程：遍历项目中所有路由函数，
// 从 lego RouterGroup 注册语法提取路由组、路由、中间件、handler/路由组包装器，
// 并通过参数传递与返回值建立跨函数的组流动（group flow）与前缀传播。
//
// Package route 从 lego RouterGroup 的注册语法（g.Group / g.GET / g.Use 以及项目内
// Add*/Create*/New*/Build* 等 group helper）中提取路由组、路由、中间件、handler 包装器、
// 路由组包装器，并通过路由函数之间的参数传递和直接返回值建立跨函数的 group flow，
// 同时记录每条事件在源码中的语句序号以支持按声明顺序的影响范围分析。
package route

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// Extract 是 route 提取入口：扫描全部函数，提取路由组/路由/中间件事实，
// 随后建立跨函数组流动并应用调用上下文传播的前缀。
func Extract(p *project.Project, _ *astindex.Index, store *facts.Store) error {
	funcs := routeFunctions(p)
	stringConsts := routeStringConstants(p)
	var callContexts []routeCallContext
	var returnCallContexts []routeReturnCallContext
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				collectFunc(p, pkg, file, fn, store, funcs, stringConsts, &callContexts, &returnCallContexts)
			}
		}
	}
	addRouteGroupFlows(store, funcs, callContexts, returnCallContexts)
	applyRouteCallPrefixes(store, callContexts)
	return nil
}

// routeStringConstants 收集每个包内可解析为字符串的字面量常量，
// 用于在路由路径中使用 const 名称或常量拼接时还原真实路径。
func routeStringConstants(p *project.Project) map[string]map[string]string {
	exprs := map[string]map[string]ast.Expr{}
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				genDecl, ok := decl.(*ast.GenDecl)
				if !ok || genDecl.Tok != token.CONST {
					continue
				}
				var previousValues []ast.Expr
				for _, rawSpec := range genDecl.Specs {
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
					for i, name := range spec.Names {
						if len(values) == 0 {
							continue
						}
						valueIndex := i
						if valueIndex >= len(values) {
							valueIndex = len(values) - 1
						}
						if exprs[pkg.Path] == nil {
							exprs[pkg.Path] = map[string]ast.Expr{}
						}
						exprs[pkg.Path][name.Name] = values[valueIndex]
					}
				}
			}
		}
	}
	out := map[string]map[string]string{}
	for pkgPath, constants := range exprs {
		for name, expr := range constants {
			value, ok := routeConstString(pkgPath, exprs, expr, map[string]bool{})
			if !ok {
				continue
			}
			if out[pkgPath] == nil {
				out[pkgPath] = map[string]string{}
			}
			out[pkgPath][name] = value
		}
	}
	return out
}

// routeConstString 把一个常量表达式解析为字符串值，支持字面量、标识符引用（含 iota 风格的上一行继承）
// 与字符串拼接，seen 用于防止递归引用造成死循环。
func routeConstString(pkgPath string, exprs map[string]map[string]ast.Expr, expr ast.Expr, seen map[string]bool) (string, bool) {
	if value, ok := stringLiteral(expr); ok {
		return value, true
	}
	switch x := expr.(type) {
	case *ast.Ident:
		key := pkgPath + "\x00" + x.Name
		if seen[key] {
			return "", false
		}
		next, ok := exprs[pkgPath][x.Name]
		if !ok {
			return "", false
		}
		nextSeen := make(map[string]bool, len(seen)+1)
		for item, present := range seen {
			nextSeen[item] = present
		}
		nextSeen[key] = true
		return routeConstString(pkgPath, exprs, next, nextSeen)
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		left, leftOK := routeConstString(pkgPath, exprs, x.X, seen)
		right, rightOK := routeConstString(pkgPath, exprs, x.Y, seen)
		if !leftOK || !rightOK {
			return "", false
		}
		return left + right, true
	case *ast.ParenExpr:
		return routeConstString(pkgPath, exprs, x.X, seen)
	default:
		return "", false
	}
}

// routeFunction 记录一个函数作为路由函数的元信息：是否返回路由组，以及直接返回了哪些组变量。
type routeFunction struct {
	fn                *ast.FuncDecl // 函数声明
	returnsGroup      bool          // 是否返回路由组类型
	returnedGroupVars []string      // 函数体中直接 return 的组变量名（去重排序）
}

// routeCallContext 记录一次对路由函数的调用：调用者、被调用者及实参到形参的组传播信息。
type routeCallContext struct {
	caller facts.SymbolID               // 调用方路由函数符号
	callee facts.SymbolID               // 被调用路由函数符号
	params map[string]routeParamContext // 形参名到组上下文传播信息的映射
}

// routeParamContext 描述某个组实参传入时的前缀、调用方根组变量及组 ID。
type routeParamContext struct {
	prefix        string // 该实参在调用方累积的前缀
	callerRootVar string // 对应的调用方根组变量名
	groupID       string // 实参对应的组 ID（用于建立 group flow 边）
}

// routeReturnCallContext 记录"返回值直接来自路由组变量"形成的组流动：
// 被调用函数返回的组流入调用方某个组变量。
type routeReturnCallContext struct {
	callee        facts.SymbolID // 被调用路由函数
	callerGroupID string         // 调用方接收返回值的组 ID
}

// routeFunctions 收集项目中所有函数及其路由函数元信息，建立符号到 routeFunction 的映射。
func routeFunctions(p *project.Project) map[facts.SymbolID]routeFunction {
	out := map[facts.SymbolID]routeFunction{}
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				out[routeFuncSymbolID(pkg.Path, fn)] = routeFunction{
					fn:                fn,
					returnsGroup:      returnsRouterGroup(fn),
					returnedGroupVars: returnedGroupVars(fn),
				}
			}
		}
	}
	return out
}

// returnsRouterGroup 判断函数是否返回路由组：首个返回类型为 RouterGroup，
// 或与首个参数（路由组指针）类型一致（即把传入的组返回出来）。
func returnsRouterGroup(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil || len(fn.Type.Results.List) == 0 {
		return false
	}
	resultType := fn.Type.Results.List[0].Type
	if isRouterGroupType(resultType) {
		return true
	}
	return fn.Type.Params != nil &&
		len(fn.Type.Params.List) > 0 &&
		strings.HasSuffix(astindex.ReceiverTypeName(resultType), "Group") &&
		exprString(resultType) == exprString(fn.Type.Params.List[0].Type)
}

// returnedGroupVars 收集函数体（不进入闭包）中 return 语句直接返回的标识符名，
// 用于识别"把组变量直接返回"形成的 group flow。结果去重并排序。
func returnedGroupVars(fn *ast.FuncDecl) []string {
	seen := map[string]bool{}
	var out []string
	if fn.Body == nil {
		return out
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		ret, ok := node.(*ast.ReturnStmt)
		if !ok || len(ret.Results) == 0 {
			return true
		}
		ident, ok := ret.Results[0].(*ast.Ident)
		if ok && !seen[ident.Name] {
			seen[ident.Name] = true
			out = append(out, ident.Name)
		}
		return false
	})
	sort.Strings(out)
	return out
}

// rootGroups 为路由函数的根组参数构建初始组上下文：仅类型为 lego 路由宿主
// （RouterGroup 或 Engine）的参数才成为 root 组。
//
// 历史实现把首个参数无条件当作 root 组（用于补偿 isRouterGroupType 只认字面名），
// 这会把 ctx context.Context、gRPC client 等非路由首参也当成 root 组：一旦函数体内
// 出现 `x := c.AddXxx(ctx, ...)` 且方法名命中 route group wrapper 命名规则，就会为普通
// 业务函数伪造 route_group 事实（如 remote/grpc/*::AddProductKeyword，parent_group_var=c），
// 污染公开 route_groups 契约数组。改为要求 root 组参数必须是路由宿主类型即可消除这类伪造，
// 同时因真实路由函数首参均为 *lego.RouterGroup / *lego.Engine（isRouterRootParamType 识别），
// 不影响合法路由/中间件抽取。
func rootGroups(routeFunc facts.SymbolID, fn *ast.FuncDecl) map[string]groupContext {
	out := map[string]groupContext{}
	if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return out
	}
	for _, field := range fn.Type.Params.List {
		if !isRouterRootParamType(field.Type) {
			continue
		}
		for _, name := range field.Names {
			out[name.Name] = groupContext{
				id:      rootGroupID(routeFunc, name.Name),
				varName: name.Name,
				prefix:  "",
				rootVar: name.Name,
			}
		}
	}
	return out
}

// isRouterRootParamType 判断参数类型是否可作为根路由宿主：类型名以 "Group" 结尾
// （RouterGroup、Group 等 lego 路由组类型）或为 "Engine"（lego 根引擎，可注册全局
// middleware/子 group）。穿透指针与泛型包装。
// 该启发式覆盖真实项目的 *lego.RouterGroup / *lego.Engine 与测试中的 *Group，同时排除
// context.Context、gRPC client、请求体等非路由首参（它们既不以 Group 结尾也不是 Engine），
// 从而避免把普通业务首参当成 root 组、进而为 `x := c.AddXxx(...)` 之类调用伪造 route_group。
func isRouterRootParamType(expr ast.Expr) bool {
	switch x := expr.(type) {
	case *ast.Ident:
		return isRouterRootTypeName(x.Name)
	case *ast.SelectorExpr:
		return isRouterRootTypeName(x.Sel.Name)
	case *ast.StarExpr:
		return isRouterRootParamType(x.X)
	case *ast.IndexExpr:
		return isRouterRootParamType(x.X)
	case *ast.IndexListExpr:
		return isRouterRootParamType(x.X)
	default:
		return false
	}
}

// isRouterRootTypeName 判断裸类型名是否像路由宿主类型。
func isRouterRootTypeName(name string) bool {
	return name == "Engine" || strings.HasSuffix(name, "Group")
}

// isRouterGroupType 判断类型表达式是否为 lego 的 RouterGroup（穿透指针、泛型索引）。
func isRouterGroupType(expr ast.Expr) bool {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name == "RouterGroup"
	case *ast.SelectorExpr:
		return x.Sel.Name == "RouterGroup"
	case *ast.StarExpr:
		return isRouterGroupType(x.X)
	case *ast.IndexExpr:
		return isRouterGroupType(x.X)
	case *ast.IndexListExpr:
		return isRouterGroupType(x.X)
	default:
		return false
	}
}

// collectFunc 处理单个函数：初始化根组上下文后按语句序号遍历函数体，提取路由事件。
func collectFunc(
	p *project.Project,
	pkg *project.Package,
	file *project.File,
	fn *ast.FuncDecl,
	store *facts.Store,
	funcs map[facts.SymbolID]routeFunction,
	stringConsts map[string]map[string]string,
	callContexts *[]routeCallContext,
	returnCallContexts *[]routeReturnCallContext,
) {
	routeFunc := routeFuncSymbolID(pkg.Path, fn)
	groups := rootGroups(routeFunc, fn)
	cursor := &routeEventCursor{}
	for _, stmt := range fn.Body.List {
		collectStmt(p, file, routeFunc, store, groups, stmt, cursor, funcs, stringConsts, callContexts, returnCallContexts)
	}
}

// routeEventCursor 是路由事件（组/路由/中间件）的语句序号计数器，
// 用于在分支/循环中仍能给出单调递增的全函数序号。
type routeEventCursor struct {
	next int
}

// Next 自增并返回下一个事件序号。
func (c *routeEventCursor) Next() int {
	c.next++
	return c.next
}

// collectStmt 按语句类型分发提取逻辑，并为各控制流分支复制独立的组上下文，
// 使分支内定义的组不会泄漏到兄弟分支。语句序号由共享游标维护以保证全局单调。
func collectStmt(
	p *project.Project,
	file *project.File,
	routeFunc facts.SymbolID,
	store *facts.Store,
	groups map[string]groupContext,
	stmt ast.Stmt,
	cursor *routeEventCursor,
	funcs map[facts.SymbolID]routeFunction,
	stringConsts map[string]map[string]string,
	callContexts *[]routeCallContext,
	returnCallContexts *[]routeReturnCallContext,
) {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		for i, lhs := range s.Lhs {
			name, ok := lhs.(*ast.Ident)
			if !ok || i >= len(s.Rhs) {
				continue
			}
			// 右侧是路由组调用（Group/工厂/包装器）：登记新组并记录其组调用中间件。
			if parent, prefix, prefixRaw, ok := groupCall(file, funcs, stringConsts, groups, s.Rhs[i]); ok {
				statementIndex := cursor.Next()
				groupID := routeGroupID(routeFunc, name.Name, statementIndex)
				groups[name.Name] = groupContext{id: groupID, varName: name.Name, prefix: prefix, prefixRaw: prefixRaw, rootVar: parent.rootVar}
				store.RouteGroups = append(store.RouteGroups, facts.RouteGroupFact{
					ID:             groupID,
					GroupVar:       name.Name,
					ParentGroupID:  parent.id,
					ParentGroupVar: parent.varName,
					Prefix:         prefix,
					PrefixRaw:      prefixRaw,
					RouteFunc:      routeFunc,
					StatementIndex: statementIndex,
					Span:           spanFor(p, file, s.Pos(), s.End()),
				})
				recordRouteReturnCallContext(file, groupID, s.Rhs[i], funcs, returnCallContexts)
				// Group 调用除路径外的额外参数视为该组的中间件绑定。
				for _, raw := range groupMiddlewareArgs(s.Rhs[i]) {
					middlewareIndex := cursor.Next()
					store.Middleware = append(store.Middleware, facts.MiddlewareBindingFact{
						ID:             middlewareID(routeFunc, name.Name, middlewareIndex) + ":" + strconv.Itoa(len(store.Middleware)),
						GroupID:        groupID,
						GroupVar:       name.Name,
						MiddlewareRaw:  raw,
						RouteFunc:      routeFunc,
						StatementIndex: middlewareIndex,
						Span:           spanFor(p, file, s.Pos(), s.End()),
					})
				}
			}
		}
	case *ast.ExprStmt:
		call, ok := s.X.(*ast.CallExpr)
		if !ok {
			return
		}
		collectCall(p, file, routeFunc, store, groups, call, cursor, funcs, callContexts)
	case *ast.BlockStmt:
		for _, child := range s.List {
			collectStmt(p, file, routeFunc, store, groups, child, cursor, funcs, stringConsts, callContexts, returnCallContexts)
		}
	case *ast.IfStmt:
		branchGroups := copyGroups(groups)
		if s.Init != nil {
			collectStmt(p, file, routeFunc, store, branchGroups, s.Init, cursor, funcs, stringConsts, callContexts, returnCallContexts)
		}
		collectStmt(p, file, routeFunc, store, copyGroups(branchGroups), s.Body, cursor, funcs, stringConsts, callContexts, returnCallContexts)
		if s.Else != nil {
			collectStmt(p, file, routeFunc, store, copyGroups(branchGroups), s.Else, cursor, funcs, stringConsts, callContexts, returnCallContexts)
		}
	case *ast.ForStmt:
		collectStmt(p, file, routeFunc, store, copyGroups(groups), s.Body, cursor, funcs, stringConsts, callContexts, returnCallContexts)
	case *ast.RangeStmt:
		collectStmt(p, file, routeFunc, store, copyGroups(groups), s.Body, cursor, funcs, stringConsts, callContexts, returnCallContexts)
	case *ast.SwitchStmt:
		switchGroups := copyGroups(groups)
		if s.Init != nil {
			collectStmt(p, file, routeFunc, store, switchGroups, s.Init, cursor, funcs, stringConsts, callContexts, returnCallContexts)
		}
		for _, rawClause := range s.Body.List {
			clause, ok := rawClause.(*ast.CaseClause)
			if !ok {
				continue
			}
			clauseGroups := copyGroups(switchGroups)
			for _, child := range clause.Body {
				collectStmt(p, file, routeFunc, store, clauseGroups, child, cursor, funcs, stringConsts, callContexts, returnCallContexts)
			}
		}
	case *ast.TypeSwitchStmt:
		switchGroups := copyGroups(groups)
		if s.Init != nil {
			collectStmt(p, file, routeFunc, store, switchGroups, s.Init, cursor, funcs, stringConsts, callContexts, returnCallContexts)
		}
		if s.Assign != nil {
			collectStmt(p, file, routeFunc, store, switchGroups, s.Assign, cursor, funcs, stringConsts, callContexts, returnCallContexts)
		}
		for _, rawClause := range s.Body.List {
			clause, ok := rawClause.(*ast.CaseClause)
			if !ok {
				continue
			}
			clauseGroups := copyGroups(switchGroups)
			for _, child := range clause.Body {
				collectStmt(p, file, routeFunc, store, clauseGroups, child, cursor, funcs, stringConsts, callContexts, returnCallContexts)
			}
		}
	case *ast.SelectStmt:
		for _, rawClause := range s.Body.List {
			clause, ok := rawClause.(*ast.CommClause)
			if !ok {
				continue
			}
			clauseGroups := copyGroups(groups)
			if clause.Comm != nil {
				collectStmt(p, file, routeFunc, store, clauseGroups, clause.Comm, cursor, funcs, stringConsts, callContexts, returnCallContexts)
			}
			for _, child := range clause.Body {
				collectStmt(p, file, routeFunc, store, clauseGroups, child, cursor, funcs, stringConsts, callContexts, returnCallContexts)
			}
		}
	case *ast.LabeledStmt:
		collectStmt(p, file, routeFunc, store, groups, s.Stmt, cursor, funcs, stringConsts, callContexts, returnCallContexts)
	case *ast.DeferStmt:
		collectCall(p, file, routeFunc, store, groups, s.Call, cursor, funcs, callContexts)
	case *ast.GoStmt:
		collectCall(p, file, routeFunc, store, groups, s.Call, cursor, funcs, callContexts)
	}
}

// collectCall 处理一次调用表达式，按优先级判定为中间件绑定或路由注册，
// 否则记录为路由函数调用上下文以供前缀传播。
func collectCall(
	p *project.Project,
	file *project.File,
	routeFunc facts.SymbolID,
	store *facts.Store,
	groups map[string]groupContext,
	call *ast.CallExpr,
	cursor *routeEventCursor,
	funcs map[facts.SymbolID]routeFunction,
	callContexts *[]routeCallContext,
) {
	nextIndex := cursor.next + 1
	if binding, ok := middlewareCall(p, file, routeFunc, funcs, groups, call, nextIndex); ok {
		cursor.Next()
		store.Middleware = append(store.Middleware, binding)
		return
	}
	nextIndex = cursor.next + 1
	if route, ok := routeCall(p, file, routeFunc, store, funcs, groups, call, nextIndex); ok {
		cursor.Next()
		store.Routes = append(store.Routes, route)
		return
	}
	recordRouteFunctionCallContext(file, routeFunc, funcs, groups, call, callContexts)
}

// routeFuncSymbolID 计算函数声明的符号 ID，区分普通函数与方法（带接收者）。
func routeFuncSymbolID(pkgPath string, fn *ast.FuncDecl) facts.SymbolID {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return astindex.FunctionSymbolID(pkgPath, fn.Name.Name)
	}
	return astindex.MethodSymbolID(pkgPath, astindex.ReceiverTypeName(fn.Recv.List[0].Type), fn.Name.Name)
}

// copyGroups 浅拷贝组上下文表，供控制流分支隔离使用。
func copyGroups(groups map[string]groupContext) map[string]groupContext {
	out := make(map[string]groupContext, len(groups))
	for name, group := range groups {
		out[name] = group
	}
	return out
}

// recordRouteFunctionCallContext 记录一次对项目内路由函数的调用：
// 把每个解析为组的实参映射到被调用函数的形参名，用于后续前缀传播与 group flow 建立。
func recordRouteFunctionCallContext(file *project.File, routeFunc facts.SymbolID, funcs map[facts.SymbolID]routeFunction, groups map[string]groupContext, call *ast.CallExpr, callContexts *[]routeCallContext) {
	callee, ok := resolveRouteFunctionCall(file, call.Fun)
	if !ok {
		return
	}
	target, ok := funcs[callee]
	if !ok {
		return
	}
	paramNames := routeParamNamesByArgument(target.fn)
	params := map[string]routeParamContext{}
	for i, arg := range call.Args {
		if i >= len(paramNames) {
			continue
		}
		group, _, ok := groupForExpr(file, funcs, groups, arg)
		if !ok {
			continue
		}
		for _, name := range paramNames[i] {
			params[name] = routeParamContext{
				prefix:        group.prefix,
				callerRootVar: group.rootVar,
				groupID:       group.id,
			}
		}
	}
	if len(params) == 0 {
		return
	}
	*callContexts = append(*callContexts, routeCallContext{
		caller: routeFunc,
		callee: callee,
		params: params,
	})
}

// recordRouteReturnCallContext 处理右侧是路由函数调用的赋值：
// 若被调用函数返回路由组且存在直接返回的组变量，则记录一条返回值 group flow 上下文。
func recordRouteReturnCallContext(
	file *project.File,
	callerGroupID string,
	expr ast.Expr,
	funcs map[facts.SymbolID]routeFunction,
	contexts *[]routeReturnCallContext,
) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return
	}
	callee, ok := resolveRouteFunctionCall(file, call.Fun)
	if !ok || !funcs[callee].returnsGroup || len(funcs[callee].returnedGroupVars) == 0 {
		return
	}
	*contexts = append(*contexts, routeReturnCallContext{
		callee:        callee,
		callerGroupID: callerGroupID,
	})
}

// addRouteGroupFlows 基于调用上下文与返回上下文建立组流动边：
// 实参→被调用函数 root 组，以及被调用函数返回的组→调用方接收组。
// 结果去重并按 ID 排序后写入 store。
func addRouteGroupFlows(
	store *facts.Store,
	funcs map[facts.SymbolID]routeFunction,
	callContexts []routeCallContext,
	returnContexts []routeReturnCallContext,
) {
	seen := map[string]bool{}
	add := func(parent, child string) {
		if parent == "" || child == "" || parent == child {
			return
		}
		id := "route_group_flow:" + parent + ":" + child
		if seen[id] {
			return
		}
		seen[id] = true
		store.RouteGroupFlows = append(store.RouteGroupFlows, facts.RouteGroupFlowFact{
			ID:            id,
			ParentGroupID: parent,
			ChildGroupID:  child,
		})
	}
	for _, context := range callContexts {
		for param, paramContext := range context.params {
			add(paramContext.groupID, rootGroupID(context.callee, param))
		}
	}
	groups := map[facts.SymbolID]map[string][]string{}
	for _, group := range store.RouteGroups {
		if groups[group.RouteFunc] == nil {
			groups[group.RouteFunc] = map[string][]string{}
		}
		groups[group.RouteFunc][group.GroupVar] = append(groups[group.RouteFunc][group.GroupVar], group.ID)
	}
	for _, context := range returnContexts {
		for _, returnedVar := range funcs[context.callee].returnedGroupVars {
			returnedGroups := groups[context.callee][returnedVar]
			if len(returnedGroups) == 0 {
				returnedGroups = []string{rootGroupID(context.callee, returnedVar)}
			}
			for _, returnedGroupID := range returnedGroups {
				add(returnedGroupID, context.callerGroupID)
			}
		}
	}
	sort.Slice(store.RouteGroupFlows, func(i, j int) bool {
		return store.RouteGroupFlows[i].ID < store.RouteGroupFlows[j].ID
	})
}

// resolveRouteFunctionCall 把调用表达式解析为被调用函数的符号 ID，
// 支持同包标识符与跨包选择器（通过文件导入表查包路径）。
func resolveRouteFunctionCall(file *project.File, expr ast.Expr) (facts.SymbolID, bool) {
	switch x := expr.(type) {
	case *ast.Ident:
		return astindex.FunctionSymbolID(file.Package.Path, x.Name), true
	case *ast.SelectorExpr:
		root, ok := x.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		importPath := file.Imports[root.Name]
		if importPath == "" {
			return "", false
		}
		return astindex.FunctionSymbolID(importPath, x.Sel.Name), true
	default:
		return "", false
	}
}

// routeParamNamesByArgument 返回按实参位置对齐的形参名切片：
// Go 中多名称共享一个类型字段会展开为多个位置，无名称字段对应 nil。
func routeParamNamesByArgument(fn *ast.FuncDecl) [][]string {
	if fn.Type.Params == nil {
		return nil
	}
	var out [][]string
	for _, field := range fn.Type.Params.List {
		if len(field.Names) == 0 {
			out = append(out, nil)
			continue
		}
		for _, name := range field.Names {
			out = append(out, []string{name.Name})
		}
	}
	return out
}

// applyRouteCallPrefixes 利用调用上下文传播得到的唯一前缀，回填路由函数内
// 未能本地解析（依赖传入组）的路由完整路径。
func applyRouteCallPrefixes(store *facts.Store, callContexts []routeCallContext) {
	prefixes := uniqueRouteCallPrefixes(callContexts)
	if len(prefixes) == 0 {
		return
	}
	groupsByID := map[string]facts.RouteGroupFact{}
	for _, group := range store.RouteGroups {
		groupsByID[group.ID] = group
	}
	for i := range store.Routes {
		route := &store.Routes[i]
		rootVar := routeRootGroupVar(*route, groupsByID)
		prefix := prefixes[route.RouteFunc][rootVar]
		if prefix == "" {
			continue
		}
		path := route.ResolvedPath
		if path == "" && route.PathRaw == "" {
			path = route.LocalPath
		}
		if path == "" {
			continue
		}
		route.ResolvedPath = joinContextPrefix(prefix, path)
	}
}

// uniqueRouteCallPrefixes 通过不动点迭代传播每个 root 组的累积前缀：
// 只有当一个 root 组在所有调用处收敛到唯一前缀时才回填，避免歧义。
// 同时跳过那些本身也是别路由函数实参的"中间"组（hasIncoming），防止把中间层当作终点播种。
func uniqueRouteCallPrefixes(callContexts []routeCallContext) map[facts.SymbolID]map[string]string {
	hasIncoming := routeCallIncomingParams(callContexts)
	prefixSets := map[facts.SymbolID]map[string]map[string]struct{}{}
	changed := true
	maxIterations := len(callContexts) + 1
	for iteration := 0; changed && iteration < maxIterations; iteration++ {
		changed = false
		for _, context := range callContexts {
			for param, paramContext := range context.params {
				callerPrefixes := prefixSets[context.caller][paramContext.callerRootVar]
				if len(callerPrefixes) == 0 {
					if hasIncoming[context.caller][paramContext.callerRootVar] {
						continue
					}
					if addRoutePrefix(prefixSets, context.callee, param, paramContext.prefix) {
						changed = true
					}
					continue
				}
				for callerPrefix := range callerPrefixes {
					prefix := joinContextPrefix(callerPrefix, paramContext.prefix)
					if addRoutePrefix(prefixSets, context.callee, param, prefix) {
						changed = true
					}
				}
			}
		}
	}
	out := map[facts.SymbolID]map[string]string{}
	for callee, params := range prefixSets {
		for param, prefixes := range params {
			if len(prefixes) != 1 {
				continue
			}
			for prefix := range prefixes {
				if out[callee] == nil {
					out[callee] = map[string]string{}
				}
				out[callee][param] = prefix
			}
		}
	}
	return out
}

// routeCallIncomingParams 统计每个路由函数的哪些参数是由调用方传入的组（即作为路由函数实参出现）。
func routeCallIncomingParams(callContexts []routeCallContext) map[facts.SymbolID]map[string]bool {
	out := map[facts.SymbolID]map[string]bool{}
	for _, context := range callContexts {
		for param := range context.params {
			if out[context.callee] == nil {
				out[context.callee] = map[string]bool{}
			}
			out[context.callee][param] = true
		}
	}
	return out
}

// addRoutePrefix 向某路由函数某参数的前缀集合中添加一个前缀，返回是否为新加入。
func addRoutePrefix(prefixSets map[facts.SymbolID]map[string]map[string]struct{}, routeFunc facts.SymbolID, param string, prefix string) bool {
	if prefixSets[routeFunc] == nil {
		prefixSets[routeFunc] = map[string]map[string]struct{}{}
	}
	if prefixSets[routeFunc][param] == nil {
		prefixSets[routeFunc][param] = map[string]struct{}{}
	}
	if _, ok := prefixSets[routeFunc][param][prefix]; ok {
		return false
	}
	prefixSets[routeFunc][param][prefix] = struct{}{}
	return true
}

// routeRootGroupVar 沿父组链回溯，找到路由所属的 root 组变量名，
// 用于在 applyRouteCallPrefixes 中查表得到调用方传播来的前缀。
func routeRootGroupVar(route facts.RouteRegistrationFact, groupsByID map[string]facts.RouteGroupFact) string {
	if route.GroupID == "" {
		return route.GroupVar
	}
	currentID := route.GroupID
	for {
		group, ok := groupsByID[currentID]
		if !ok {
			return route.GroupVar
		}
		if group.ParentGroupID == "" {
			return group.GroupVar
		}
		if _, ok := groupsByID[group.ParentGroupID]; !ok {
			return group.ParentGroupVar
		}
		currentID = group.ParentGroupID
	}
}

// joinContextPrefix 用调用方前缀拼接子路径；若 path 已经以该前缀开头则原样返回，避免重复。
func joinContextPrefix(prefix, path string) string {
	if prefix == "" {
		return path
	}
	if path == prefix || strings.HasPrefix(path, strings.TrimRight(prefix, "/")+"/") {
		return path
	}
	return joinPath(prefix, path)
}

// groupMiddlewareArgs 从 g.Group(path, mw1, mw2) 调用中取出路径之后的所有参数原始文本，
// 视为该组的中间件绑定。
func groupMiddlewareArgs(expr ast.Expr) []string {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) <= 1 {
		return nil
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Group" {
		return nil
	}
	out := make([]string, 0, len(call.Args)-1)
	for _, arg := range call.Args[1:] {
		out = append(out, exprString(arg))
	}
	return out
}

// groupCall 判定一个右侧表达式是否产生新的路由组，并返回其父组、累积前缀与是否成功：
// 标识符返回其组；g.Group(path) 拼接父前缀；工厂/包装器调用按命名规则递归处理。
// 对于项目内的工厂/包装器调用，会校验返回类型确为路由组，避免把普通业务函数误判为组。
func groupCall(
	file *project.File,
	funcs map[facts.SymbolID]routeFunction,
	stringConsts map[string]map[string]string,
	groups map[string]groupContext,
	expr ast.Expr,
) (parent groupContext, prefix, prefixRaw string, ok bool) {
	if ident, ok := expr.(*ast.Ident); ok {
		parent, ok := groups[ident.Name]
		if !ok {
			return groupContext{}, "", "", false
		}
		return parent, parent.prefix, parent.prefixRaw, true
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return groupContext{}, "", "", false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if ok && selector.Sel.Name == "Group" {
		switch base := selector.X.(type) {
		case *ast.Ident:
			parent, ok = groups[base.Name]
			if !ok {
				return groupContext{}, "", "", false
			}
		case *ast.CallExpr:
			// 链式 g.Group(a).Group(b)：接收者本身又是一次 Group（或工厂/包装器）调用。
			// 递归求内层调用的父上下文与累积前缀，折叠成一个合成父上下文再拼接本层路径。
			// 中间层不单独物化为 group 事实，但最终 resolved 前缀完整（修复 router.go 中
			// adminWithoutAuthGroup := g.Group(WEB_BFF_PREFIX).Group("") 前缀丢失）。
			innerParent, innerPrefix, innerPrefixRaw, ok := groupCall(file, funcs, stringConsts, groups, base)
			if !ok {
				return groupContext{}, "", "", false
			}
			parent = groupContext{
				id:        innerParent.id,
				varName:   innerParent.varName,
				prefix:    innerPrefix,
				prefixRaw: innerPrefixRaw,
				rootVar:   innerParent.rootVar,
			}
		default:
			return groupContext{}, "", "", false
		}
		local, resolved := routeStringArg(file, stringConsts, call.Args[0])
		if resolved && parent.prefixRaw == "" {
			return parent, joinPath(parent.prefix, local), "", true
		}
		localRaw := exprString(call.Args[0])
		if resolved {
			localRaw = strconv.Quote(local)
		}
		return parent, "", joinRoutePathExpression(parent.prefix, parent.prefixRaw, localRaw), true
	}
	name := shortCallName(call)
	if isRouteGroupFactory(name) || isRouteGroupWrapper(name) {
		if unresolvedSelectorRouteFunction(file, funcs, call.Fun) {
			return groupContext{}, "", "", false
		}
		if callee, resolved := resolveRouteFunctionCall(file, call.Fun); resolved {
			if target, projectFunction := funcs[callee]; projectFunction && !target.returnsGroup {
				return groupContext{}, "", "", false
			}
		}
	}
	if isRouteGroupFactory(name) {
		parent, prefix, prefixRaw, ok := groupCall(file, funcs, stringConsts, groups, call.Args[0])
		if !ok {
			return groupContext{}, "", "", false
		}
		if len(call.Args) > 1 {
			if local, resolved := routeStringArg(file, stringConsts, call.Args[1]); resolved {
				if prefixRaw == "" {
					prefix = joinPath(prefix, local)
				} else {
					prefixRaw = joinRoutePathExpression("", prefixRaw, strconv.Quote(local))
				}
			} else {
				prefixRaw = joinRoutePathExpression(prefix, prefixRaw, exprString(call.Args[1]))
				prefix = ""
			}
		}
		return parent, prefix, prefixRaw, true
	}
	if !isRouteGroupWrapper(name) {
		return groupContext{}, "", "", false
	}
	return groupCall(file, funcs, stringConsts, groups, call.Args[0])
}

func joinRoutePathExpression(staticPrefix, rawPrefix, localRaw string) string {
	parts := make([]string, 0, 3)
	if rawPrefix != "" {
		parts = append(parts, rawPrefix)
	} else if staticPrefix != "" && staticPrefix != "/" {
		parts = append(parts, strconv.Quote(staticPrefix))
	}
	if localRaw != "" {
		parts = append(parts, localRaw)
	}
	return strings.Join(parts, " + ")
}

// routeStringArg 解析路径参数为字符串：支持字面量、包内常量名、字符串拼接与括号包裹。
func routeStringArg(file *project.File, stringConsts map[string]map[string]string, expr ast.Expr) (string, bool) {
	if value, ok := stringLiteral(expr); ok {
		return value, true
	}
	switch x := expr.(type) {
	case *ast.Ident:
		value, ok := stringConsts[file.Package.Path][x.Name]
		return value, ok
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		left, leftOK := routeStringArg(file, stringConsts, x.X)
		right, rightOK := routeStringArg(file, stringConsts, x.Y)
		if !leftOK || !rightOK {
			return "", false
		}
		return left + right, true
	case *ast.ParenExpr:
		return routeStringArg(file, stringConsts, x.X)
	default:
		return "", false
	}
}

// routeCall 把一个 HTTP 方法调用构造为 RouteRegistrationFact：
// 解析方法/路径/handler，拼接组前缀得到解析路径，合并组与 handler 包装器，
// 并在路径动态或 handler 无法精确解析时发出诊断。
func routeCall(p *project.Project, file *project.File, routeFunc facts.SymbolID, store *facts.Store, funcs map[facts.SymbolID]routeFunction, groups map[string]groupContext, call *ast.CallExpr, statementIndex int) (facts.RouteRegistrationFact, bool) {
	parsed, ok := ParseRouteCall(call)
	if !ok {
		return facts.RouteRegistrationFact{}, false
	}
	selector := call.Fun.(*ast.SelectorExpr)
	group, groupWrappers, ok := groupForExpr(file, funcs, groups, selector.X)
	if !ok {
		return facts.RouteRegistrationFact{}, false
	}
	wrappers := append(groupWrappers, parsed.HandlerWrappers...)
	resolved := ""
	if parsed.PathRaw == "" && group.prefixRaw == "" {
		resolved = joinPath(group.prefix, parsed.LocalPath)
	}
	pathRaw := parsed.PathRaw
	if group.prefixRaw != "" {
		localRaw := parsed.PathRaw
		if localRaw == "" {
			localRaw = strconv.Quote(parsed.LocalPath)
		}
		pathRaw = joinRoutePathExpression("", group.prefixRaw, localRaw)
	}
	route := facts.RouteRegistrationFact{
		ID:             routeID(routeFunc, parsed.Method, parsed.LocalPath, statementIndex),
		Method:         parsed.Method,
		LocalPath:      parsed.LocalPath,
		PathRaw:        pathRaw,
		ResolvedPath:   resolved,
		GroupID:        group.id,
		GroupVar:       group.varName,
		HandlerRaw:     parsed.HandlerRaw,
		Wrappers:       wrappers,
		RouteFunc:      routeFunc,
		StatementIndex: statementIndex,
		File:           filepath.ToSlash(mustRel(p.Root, file.Path)),
		Span:           spanFor(p, file, call.Pos(), call.End()),
	}
	route.Evidence = []facts.EvidenceFact{{
		Kind:       "route_call",
		Raw:        exprString(call),
		Span:       route.Span,
		Confidence: facts.ConfidenceHigh,
	}}
	if pathRaw != "" {
		diagnostics.AddFact(store, diagnostics.Diagnostic{
			Code:           diagnostics.CodeRouteDynamicPath,
			Severity:       diagnostics.SeverityWarning,
			Message:        "dynamic route path cannot be resolved",
			Span:           route.Span,
			RelatedFactIDs: []string{route.ID},
		})
	}
	if isUnresolvedHandlerExpression(call.Args[1]) {
		diagnostics.AddFact(store, diagnostics.Diagnostic{
			Code:           diagnostics.CodeRouteUnresolvedHandler,
			Severity:       diagnostics.SeverityWarning,
			Message:        "route handler expression cannot be resolved precisely",
			Span:           route.Span,
			RelatedFactIDs: []string{route.ID},
		})
	}
	if hasGuessedWrapper(parsed.HandlerWrappers) {
		// wrapper 调用名不在已知白名单中，提取器退化为"取最后一个长得像 handler 的
		// 实参"这一结构兜底猜测；若该 wrapper 实际语义并非原样转发（记录/审计后返回
		// 另一闭包、按条件交换实参等），猜出的 handler 可能与真实注册的不符。
		// 与 isUnresolvedHandlerExpression 不同：这里"成功"产出了一个表达式，
		// 因此不会触发 CodeRouteUnresolvedHandler，必须用独立诊断标记其证据强度。
		diagnostics.AddFact(store, diagnostics.Diagnostic{
			Code:           diagnostics.CodeRouteWrapperGuessed,
			Severity:       diagnostics.SeverityWarning,
			Message:        "handler wrapper is not in the known whitelist; innermost handler was inferred structurally and may be incorrect if the wrapper does not forward as-is",
			Span:           route.Span,
			RelatedFactIDs: []string{route.ID},
		})
	}
	return route, true
}

// hasGuessedWrapper 判断 wrapper 列表中是否存在任意一个经结构兜底猜测得出的 wrapper。
func hasGuessedWrapper(wrappers []facts.WrapperFact) bool {
	for _, wrapper := range wrappers {
		if wrapper.Guessed {
			return true
		}
	}
	return false
}

// isUnresolvedHandlerExpression 判断 handler 表达式是否无法精确解析：
// 标识符/选择器可解析；纯调用但拆不出任何包装器视为不可解析；其他表达式同样视为不可解析。
func isUnresolvedHandlerExpression(expr ast.Expr) bool {
	switch x := expr.(type) {
	case *ast.Ident, *ast.SelectorExpr:
		return false
	case *ast.CallExpr:
		_, wrappers := unwrapHandler(x)
		return len(wrappers) == 0
	default:
		return true
	}
}

// middlewareCall 把 g.Use(mw...) 调用构造为 MiddlewareBindingFact，
// 多个实参以逗号拼接为一条中间件记录。
func middlewareCall(p *project.Project, file *project.File, routeFunc facts.SymbolID, funcs map[facts.SymbolID]routeFunction, groups map[string]groupContext, call *ast.CallExpr, statementIndex int) (facts.MiddlewareBindingFact, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Use" || len(call.Args) == 0 {
		return facts.MiddlewareBindingFact{}, false
	}
	group, _, ok := groupForExpr(file, funcs, groups, selector.X)
	if !ok {
		return facts.MiddlewareBindingFact{}, false
	}
	raws := make([]string, 0, len(call.Args))
	for _, arg := range call.Args {
		raws = append(raws, exprString(arg))
	}
	raw := strings.Join(raws, ", ")
	return facts.MiddlewareBindingFact{
		ID:             middlewareID(routeFunc, group.varName, statementIndex),
		GroupID:        group.id,
		GroupVar:       group.varName,
		MiddlewareRaw:  raw,
		RouteFunc:      routeFunc,
		StatementIndex: statementIndex,
		Span:           spanFor(p, file, call.Pos(), call.End()),
	}, true
}

// rootGroupID 构造路由函数某参数对应的 root 组 ID。
func rootGroupID(routeFunc facts.SymbolID, name string) string {
	return "route_group:" + string(routeFunc) + ":" + name + ":root"
}

// routeGroupID 构造一个普通路由组（带语句序号）的 ID。
func routeGroupID(routeFunc facts.SymbolID, name string, statementIndex int) string {
	return "route_group:" + string(routeFunc) + ":" + name + ":" + strconv.Itoa(statementIndex)
}

// routeID 构造一条路由注册事实的 ID。
func routeID(routeFunc facts.SymbolID, method, localPath string, statementIndex int) string {
	return "route:" + string(routeFunc) + ":" + method + ":" + localPath + ":" + strconv.Itoa(statementIndex)
}

// middlewareID 构造一条中间件绑定事实的 ID。
func middlewareID(routeFunc facts.SymbolID, groupVar string, statementIndex int) string {
	return "middleware:" + string(routeFunc) + ":" + groupVar + ":" + strconv.Itoa(statementIndex)
}

// spanFor 计算给定起止位置的源码 span，并把文件路径转为相对项目根的路径。
func spanFor(p *project.Project, file *project.File, start, end token.Pos) facts.SourceSpan {
	span := astindex.SourceSpanFor(file.FileSet, start, end)
	if rel, err := filepath.Rel(p.Root, span.File); err == nil {
		span.File = filepath.ToSlash(rel)
	}
	return span
}

// mustRel 把绝对路径转为相对项目根的路径，转换失败时原样返回。
func mustRel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
