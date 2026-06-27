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
	routeFunc := astindex.FunctionSymbolID(pkg.Path, fn.Name.Name)
	groups := rootGroups(routeFunc, fn)
	for i, stmt := range fn.Body.List {
		collectStmt(p, file, routeFunc, store, groups, stmt, i+1, cfg)
	}
}

func collectStmt(p *project.Project, file *project.File, routeFunc facts.SymbolID, store *facts.Store, groups map[string]groupContext, stmt ast.Stmt, statementIndex int, cfg config.Config) {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		for i, lhs := range s.Lhs {
			name, ok := lhs.(*ast.Ident)
			if !ok || i >= len(s.Rhs) {
				continue
			}
			if parent, prefix, ok := groupCall(groups, s.Rhs[i]); ok {
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
					store.Middleware = append(store.Middleware, facts.MiddlewareBindingFact{
						ID:             middlewareID(routeFunc, name.Name, statementIndex) + ":" + strconv.Itoa(len(store.Middleware)),
						GroupID:        groupID,
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
		if binding, ok := middlewareCall(p, file, routeFunc, groups, call, statementIndex, cfg); ok {
			store.Middleware = append(store.Middleware, binding)
			return
		}
		if route, ok := routeCall(p, file, routeFunc, store, groups, call, statementIndex, cfg); ok {
			store.Routes = append(store.Routes, route)
		}
	case *ast.BlockStmt:
		for i, child := range s.List {
			collectStmt(p, file, routeFunc, store, groups, child, statementIndex+i+1, cfg)
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

func groupCall(groups map[string]groupContext, expr ast.Expr) (parent groupContext, prefix string, ok bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return groupContext{}, "", false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Group" {
		return groupContext{}, "", false
	}
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

func routeCall(p *project.Project, file *project.File, routeFunc facts.SymbolID, store *facts.Store, groups map[string]groupContext, call *ast.CallExpr, statementIndex int, cfg config.Config) (facts.RouteRegistrationFact, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !cfg.IsHTTPMethod(selector.Sel.Name) || len(call.Args) < 2 {
		return facts.RouteRegistrationFact{}, false
	}
	group, groupWrappers, ok := groupForExpr(groups, selector.X, cfg)
	if !ok {
		return facts.RouteRegistrationFact{}, false
	}
	localPath, ok := stringLiteral(call.Args[0])
	pathRaw := ""
	if !ok {
		pathRaw = exprString(call.Args[0])
	}
	handlerRaw, handlerWrappers := unwrapHandler(call.Args[1], cfg)
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
		GroupID:        group.id,
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
