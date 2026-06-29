package route

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/config"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func Extract(p *project.Project, _ *astindex.Index, store *facts.Store) error {
	return ExtractWithConfig(p, nil, store, config.Default())
}

func ExtractWithConfig(p *project.Project, _ *astindex.Index, store *facts.Store, cfg config.Config) error {
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				collectFunc(p, pkg, file, fn, store, cfg)
			}
		}
	}
	return nil
}

func rootGroups(routeFunc facts.SymbolID, fn *ast.FuncDecl) map[string]groupContext {
	out := map[string]groupContext{}
	if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return out
	}
	for _, name := range fn.Type.Params.List[0].Names {
		out[name.Name] = groupContext{
			id:      rootGroupID(routeFunc, name.Name),
			varName: name.Name,
			prefix:  "",
		}
	}
	return out
}

func collectFunc(p *project.Project, pkg *project.Package, file *project.File, fn *ast.FuncDecl, store *facts.Store, cfg config.Config) {
	routeFunc := routeFuncSymbolID(pkg.Path, fn)
	groups := rootGroups(routeFunc, fn)
	cursor := &routeEventCursor{}
	for _, stmt := range fn.Body.List {
		collectStmt(p, file, routeFunc, store, groups, stmt, cursor, cfg)
	}
}

type routeEventCursor struct {
	next int
}

func (c *routeEventCursor) Next() int {
	c.next++
	return c.next
}

func collectStmt(p *project.Project, file *project.File, routeFunc facts.SymbolID, store *facts.Store, groups map[string]groupContext, stmt ast.Stmt, cursor *routeEventCursor, cfg config.Config) {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		for i, lhs := range s.Lhs {
			name, ok := lhs.(*ast.Ident)
			if !ok || i >= len(s.Rhs) {
				continue
			}
			if parent, prefix, ok := groupCall(groups, s.Rhs[i], cfg); ok {
				statementIndex := cursor.Next()
				groupID := routeGroupID(routeFunc, name.Name, statementIndex)
				groups[name.Name] = groupContext{id: groupID, varName: name.Name, prefix: prefix}
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
		nextIndex := cursor.next + 1
		if binding, ok := middlewareCall(p, file, routeFunc, groups, call, nextIndex, cfg); ok {
			cursor.Next()
			store.Middleware = append(store.Middleware, binding)
			return
		}
		nextIndex = cursor.next + 1
		if route, ok := routeCall(p, file, routeFunc, store, groups, call, nextIndex, cfg); ok {
			cursor.Next()
			store.Routes = append(store.Routes, route)
		}
	case *ast.BlockStmt:
		for _, child := range s.List {
			collectStmt(p, file, routeFunc, store, groups, child, cursor, cfg)
		}
	case *ast.IfStmt:
		branchGroups := copyGroups(groups)
		if s.Init != nil {
			collectStmt(p, file, routeFunc, store, branchGroups, s.Init, cursor, cfg)
		}
		collectStmt(p, file, routeFunc, store, copyGroups(branchGroups), s.Body, cursor, cfg)
		if s.Else != nil {
			collectStmt(p, file, routeFunc, store, copyGroups(branchGroups), s.Else, cursor, cfg)
		}
	case *ast.ForStmt:
		collectStmt(p, file, routeFunc, store, copyGroups(groups), s.Body, cursor, cfg)
	case *ast.RangeStmt:
		collectStmt(p, file, routeFunc, store, copyGroups(groups), s.Body, cursor, cfg)
	}
}

func routeFuncSymbolID(pkgPath string, fn *ast.FuncDecl) facts.SymbolID {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return astindex.FunctionSymbolID(pkgPath, fn.Name.Name)
	}
	return astindex.MethodSymbolID(pkgPath, receiverTypeName(fn.Recv.List[0].Type), fn.Name.Name)
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.IndexExpr:
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	default:
		return ""
	}
}

func copyGroups(groups map[string]groupContext) map[string]groupContext {
	out := make(map[string]groupContext, len(groups))
	for name, group := range groups {
		out[name] = group
	}
	return out
}

func groupMiddlewareArgs(expr ast.Expr) []string {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) <= 1 {
		return nil
	}
	out := make([]string, 0, len(call.Args)-1)
	for _, arg := range call.Args[1:] {
		out = append(out, exprString(arg))
	}
	return out
}

func groupCall(groups map[string]groupContext, expr ast.Expr, cfg config.Config) (parent groupContext, prefix string, ok bool) {
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
		local, ok := stringLiteral(call.Args[0])
		if !ok {
			local = exprString(call.Args[0])
		}
		return parent, joinPath(parent.prefix, local), true
	}
	if !cfg.IsRouteGroupWrapper(shortCallName(call)) {
		return groupContext{}, "", false
	}
	return groupCall(groups, call.Args[0], cfg)
}

func routeCall(p *project.Project, file *project.File, routeFunc facts.SymbolID, store *facts.Store, groups map[string]groupContext, call *ast.CallExpr, statementIndex int, cfg config.Config) (facts.RouteRegistrationFact, bool) {
	parsed, ok := ParseRouteCall(call, cfg)
	if !ok {
		return facts.RouteRegistrationFact{}, false
	}
	selector := call.Fun.(*ast.SelectorExpr)
	group, groupWrappers, ok := groupForExpr(groups, selector.X, cfg)
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
		SourceFamily:   sourceFamily(file),
		File:           filepath.ToSlash(mustRel(p.Root, file.Path)),
		Span:           spanFor(p, file, call.Pos(), call.End()),
	}
	if parsed.PathRaw != "" {
		diagnostics.AddFact(store, diagnostics.Diagnostic{
			Code:           diagnostics.CodeRouteDynamicPath,
			Severity:       diagnostics.SeverityWarning,
			Message:        "dynamic route path cannot be resolved",
			Span:           route.Span,
			RelatedFactIDs: []string{route.ID},
		})
	}
	if isUnresolvedHandlerExpression(call.Args[1], cfg) {
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

func isUnresolvedHandlerExpression(expr ast.Expr, cfg config.Config) bool {
	switch x := expr.(type) {
	case *ast.Ident, *ast.SelectorExpr:
		return false
	case *ast.CallExpr:
		_, wrappers := unwrapHandler(x, cfg)
		return len(wrappers) == 0
	default:
		return true
	}
}

func sourceFamily(file *project.File) string {
	path := filepath.ToSlash(file.Path)
	if strings.Contains(path, "/generated/") || strings.Contains(path, "/gen/") {
		return "generated"
	}
	return ""
}

func middlewareCall(p *project.Project, file *project.File, routeFunc facts.SymbolID, groups map[string]groupContext, call *ast.CallExpr, statementIndex int, cfg config.Config) (facts.MiddlewareBindingFact, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Use" || len(call.Args) == 0 {
		return facts.MiddlewareBindingFact{}, false
	}
	group, _, ok := groupForExpr(groups, selector.X, cfg)
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
