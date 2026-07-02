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

type routeFunction struct {
	fn                *ast.FuncDecl
	returnsGroup      bool
	returnedGroupVars []string
}

type routeCallContext struct {
	caller facts.SymbolID
	callee facts.SymbolID
	params map[string]routeParamContext
}

type routeParamContext struct {
	prefix        string
	callerRootVar string
	groupID       string
}

type routeReturnCallContext struct {
	callee        facts.SymbolID
	callerGroupID string
}

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

func rootGroups(routeFunc facts.SymbolID, fn *ast.FuncDecl) map[string]groupContext {
	out := map[string]groupContext{}
	if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return out
	}
	for fieldIndex, field := range fn.Type.Params.List {
		if fieldIndex > 0 && !isRouterGroupType(field.Type) {
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

type routeEventCursor struct {
	next int
}

func (c *routeEventCursor) Next() int {
	c.next++
	return c.next
}

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
			if parent, prefix, ok := groupCall(file, funcs, stringConsts, groups, s.Rhs[i]); ok {
				statementIndex := cursor.Next()
				groupID := routeGroupID(routeFunc, name.Name, statementIndex)
				groups[name.Name] = groupContext{id: groupID, varName: name.Name, prefix: prefix, rootVar: parent.rootVar}
				store.RouteGroups = append(store.RouteGroups, facts.RouteGroupFact{
					ID:             groupID,
					GroupVar:       name.Name,
					ParentGroupID:  parent.id,
					ParentGroupVar: parent.varName,
					Prefix:         prefix,
					RouteFunc:      routeFunc,
					StatementIndex: statementIndex,
					Span:           spanFor(p, file, s.Pos(), s.End()),
				})
				recordRouteReturnCallContext(file, groupID, s.Rhs[i], funcs, returnCallContexts)
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

func routeFuncSymbolID(pkgPath string, fn *ast.FuncDecl) facts.SymbolID {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return astindex.FunctionSymbolID(pkgPath, fn.Name.Name)
	}
	return astindex.MethodSymbolID(pkgPath, astindex.ReceiverTypeName(fn.Recv.List[0].Type), fn.Name.Name)
}

func copyGroups(groups map[string]groupContext) map[string]groupContext {
	out := make(map[string]groupContext, len(groups))
	for name, group := range groups {
		out[name] = group
	}
	return out
}

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

func joinContextPrefix(prefix, path string) string {
	if prefix == "" {
		return path
	}
	if path == prefix || strings.HasPrefix(path, strings.TrimRight(prefix, "/")+"/") {
		return path
	}
	return joinPath(prefix, path)
}

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

func groupCall(
	file *project.File,
	funcs map[facts.SymbolID]routeFunction,
	stringConsts map[string]map[string]string,
	groups map[string]groupContext,
	expr ast.Expr,
) (parent groupContext, prefix string, ok bool) {
	if ident, ok := expr.(*ast.Ident); ok {
		parent, ok := groups[ident.Name]
		if !ok {
			return groupContext{}, "", false
		}
		return parent, parent.prefix, true
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return groupContext{}, "", false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if ok && selector.Sel.Name == "Group" {
		parentIdent, ok := selector.X.(*ast.Ident)
		if !ok {
			return groupContext{}, "", false
		}
		parent, ok = groups[parentIdent.Name]
		if !ok {
			return groupContext{}, "", false
		}
		local, ok := routeStringArg(file, stringConsts, call.Args[0])
		if !ok {
			local = exprString(call.Args[0])
		}
		return parent, joinPath(parent.prefix, local), true
	}
	name := shortCallName(call)
	if isRouteGroupFactory(name) || isRouteGroupWrapper(name) {
		if unresolvedSelectorRouteFunction(file, funcs, call.Fun) {
			return groupContext{}, "", false
		}
		if callee, resolved := resolveRouteFunctionCall(file, call.Fun); resolved {
			if target, projectFunction := funcs[callee]; projectFunction && !target.returnsGroup {
				return groupContext{}, "", false
			}
		}
	}
	if isRouteGroupFactory(name) {
		parent, prefix, ok := groupCall(file, funcs, stringConsts, groups, call.Args[0])
		if !ok {
			return groupContext{}, "", false
		}
		if len(call.Args) > 1 {
			if local, ok := routeStringArg(file, stringConsts, call.Args[1]); ok {
				prefix = joinPath(parent.prefix, local)
			}
		}
		return parent, prefix, true
	}
	if !isRouteGroupWrapper(name) {
		return groupContext{}, "", false
	}
	return groupCall(file, funcs, stringConsts, groups, call.Args[0])
}

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
	if parsed.PathRaw == "" {
		resolved = joinPath(group.prefix, parsed.LocalPath)
	}
	route := facts.RouteRegistrationFact{
		ID:             routeID(routeFunc, parsed.Method, parsed.LocalPath, statementIndex),
		Method:         parsed.Method,
		LocalPath:      parsed.LocalPath,
		PathRaw:        parsed.PathRaw,
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
	if parsed.PathRaw != "" {
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
	return route, true
}

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

func rootGroupID(routeFunc facts.SymbolID, name string) string {
	return "route_group:" + string(routeFunc) + ":" + name + ":root"
}

func routeGroupID(routeFunc facts.SymbolID, name string, statementIndex int) string {
	return "route_group:" + string(routeFunc) + ":" + name + ":" + strconv.Itoa(statementIndex)
}

func routeID(routeFunc facts.SymbolID, method, localPath string, statementIndex int) string {
	return "route:" + string(routeFunc) + ":" + method + ":" + localPath + ":" + strconv.Itoa(statementIndex)
}

func middlewareID(routeFunc facts.SymbolID, groupVar string, statementIndex int) string {
	return "middleware:" + string(routeFunc) + ":" + groupVar + ":" + strconv.Itoa(statementIndex)
}

func spanFor(p *project.Project, file *project.File, start, end token.Pos) facts.SourceSpan {
	span := astindex.SourceSpanFor(file.FileSet, start, end)
	if rel, err := filepath.Rel(p.Root, span.File); err == nil {
		span.File = filepath.ToSlash(rel)
	}
	return span
}

func mustRel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
