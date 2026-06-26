package route

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func Extract(p *project.Project, _ *astindex.Index, store *facts.Store) error {
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				collectFunc(p, pkg, file, fn, store, rootGroups(fn))
			}
		}
	}
	return nil
}

func rootGroups(fn *ast.FuncDecl) map[string]groupContext {
	out := map[string]groupContext{}
	if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return out
	}
	for _, name := range fn.Type.Params.List[0].Names {
		out[name.Name] = groupContext{varName: name.Name, prefix: ""}
	}
	return out
}

func collectFunc(p *project.Project, pkg *project.Package, file *project.File, fn *ast.FuncDecl, store *facts.Store, groups map[string]groupContext) {
	routeFunc := astindex.FunctionSymbolID(pkg.Path, fn.Name.Name)
	for i, stmt := range fn.Body.List {
		collectStmt(p, file, routeFunc, store, groups, stmt, i+1)
	}
}

func collectStmt(p *project.Project, file *project.File, routeFunc facts.SymbolID, store *facts.Store, groups map[string]groupContext, stmt ast.Stmt, statementIndex int) {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		for i, lhs := range s.Lhs {
			name, ok := lhs.(*ast.Ident)
			if !ok || i >= len(s.Rhs) {
				continue
			}
			if parent, prefix, ok := groupCall(groups, s.Rhs[i]); ok {
				groups[name.Name] = groupContext{varName: name.Name, prefix: prefix}
				store.RouteGroups = append(store.RouteGroups, facts.RouteGroupFact{
					ID:             routeGroupID(routeFunc, name.Name, statementIndex),
					GroupVar:       name.Name,
					ParentGroupVar: parent,
					Prefix:         prefix,
					RouteFunc:      routeFunc,
					StatementIndex: statementIndex,
					Span:           spanFor(p, file, s.Pos(), s.End()),
				})
				for _, raw := range groupMiddlewareArgs(s.Rhs[i]) {
					store.Middleware = append(store.Middleware, facts.MiddlewareBindingFact{
						ID:             middlewareID(routeFunc, name.Name, statementIndex) + ":" + strconv.Itoa(len(store.Middleware)),
						GroupVar:       name.Name,
						MiddlewareRaw:  raw,
						RouteFunc:      routeFunc,
						StatementIndex: statementIndex,
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
		if binding, ok := middlewareCall(p, file, routeFunc, groups, call, statementIndex); ok {
			store.Middleware = append(store.Middleware, binding)
			return
		}
		if route, ok := routeCall(p, file, routeFunc, store, groups, call, statementIndex); ok {
			store.Routes = append(store.Routes, route)
		}
	case *ast.BlockStmt:
		for i, child := range s.List {
			collectStmt(p, file, routeFunc, store, groups, child, statementIndex+i+1)
		}
	}
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

func groupCall(groups map[string]groupContext, expr ast.Expr) (parentVar string, prefix string, ok bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return "", "", false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Group" {
		return "", "", false
	}
	parentIdent, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", "", false
	}
	parent, ok := groups[parentIdent.Name]
	if !ok {
		return "", "", false
	}
	local, ok := stringLiteral(call.Args[0])
	if !ok {
		local = exprString(call.Args[0])
	}
	return parentIdent.Name, joinPath(parent.prefix, local), true
}

func routeCall(p *project.Project, file *project.File, routeFunc facts.SymbolID, store *facts.Store, groups map[string]groupContext, call *ast.CallExpr, statementIndex int) (facts.RouteRegistrationFact, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !isHTTPMethod(selector.Sel.Name) || len(call.Args) < 2 {
		return facts.RouteRegistrationFact{}, false
	}
	group, groupWrappers, ok := groupForExpr(groups, selector.X)
	if !ok {
		return facts.RouteRegistrationFact{}, false
	}
	localPath, ok := stringLiteral(call.Args[0])
	pathRaw := ""
	if !ok {
		pathRaw = exprString(call.Args[0])
	}
	handlerRaw, handlerWrappers := unwrapHandler(call.Args[1])
	wrappers := append(groupWrappers, handlerWrappers...)
	resolved := ""
	if localPath != "" {
		resolved = joinPath(group.prefix, localPath)
	}
	method := strings.ToUpper(selector.Sel.Name)
	route := facts.RouteRegistrationFact{
		ID:             routeID(routeFunc, method, localPath, statementIndex),
		Method:         method,
		LocalPath:      localPath,
		PathRaw:        pathRaw,
		ResolvedPath:   resolved,
		GroupVar:       group.varName,
		HandlerRaw:     handlerRaw,
		Wrappers:       wrappers,
		RouteFunc:      routeFunc,
		StatementIndex: statementIndex,
		SourceFamily:   sourceFamily(file),
		File:           filepath.ToSlash(mustRel(p.Root, file.Path)),
		Span:           spanFor(p, file, call.Pos(), call.End()),
	}
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

func sourceFamily(file *project.File) string {
	path := filepath.ToSlash(file.Path)
	if strings.Contains(path, "/generated/") || strings.Contains(path, "/gen/") {
		return "generated"
	}
	return ""
}

func middlewareCall(p *project.Project, file *project.File, routeFunc facts.SymbolID, groups map[string]groupContext, call *ast.CallExpr, statementIndex int) (facts.MiddlewareBindingFact, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Use" || len(call.Args) == 0 {
		return facts.MiddlewareBindingFact{}, false
	}
	group, _, ok := groupForExpr(groups, selector.X)
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
		GroupVar:       group.varName,
		MiddlewareRaw:  raw,
		RouteFunc:      routeFunc,
		StatementIndex: statementIndex,
		Span:           spanFor(p, file, call.Pos(), call.End()),
	}, true
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
