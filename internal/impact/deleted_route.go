package impact

import (
	"fmt"
	"go/ast"
	"go/parser"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/config"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	routeextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/route"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func RecoverDeletedRoutes(fileChanges []diff.FileChange, store *facts.Store, cfg config.Config, source string) {
	if source == "" {
		source = "git_diff"
	}
	for _, fileChange := range fileChanges {
		file := filepath.ToSlash(fileChange.NewPath)
		if file == "" {
			file = filepath.ToSlash(fileChange.OldPath)
		}
		for _, block := range fileChange.DeletedBlocks {
			recoverDeletedRoutesInBlock(file, block, store, cfg, source)
		}
	}
}

func recoverDeletedRoutesInBlock(file string, block diff.DeletedBlock, store *facts.Store, cfg config.Config, source string) {
	for offset, line := range block.Lines {
		call, ok := parseDeletedRouteCall(line)
		if !ok {
			continue
		}
		parsed, ok := routeextract.ParseRouteCall(call, cfg)
		if !ok {
			continue
		}
		oldLine := block.OldStartLine + offset
		anchorLine := block.NewAnchorLine
		if anchorLine <= 0 {
			anchorLine = 1
		}
		group := resolveDeletedRouteGroup(file, anchorLine, parsed.GroupRaw, store)
		resolvedPath := ""
		if parsed.LocalPath != "" {
			resolvedPath = joinDeletedRoutePath(group.prefix, parsed.LocalPath)
		}
		wrappers := append([]facts.WrapperFact{}, parsed.GroupWrappers...)
		wrappers = append(wrappers, parsed.HandlerWrappers...)
		route := facts.RouteRegistrationFact{
			ID:             deletedRouteID(file, parsed.Method, parsed.LocalPath, oldLine, offset),
			Method:         parsed.Method,
			LocalPath:      parsed.LocalPath,
			PathRaw:        parsed.PathRaw,
			ResolvedPath:   resolvedPath,
			GroupID:        group.id,
			GroupVar:       parsed.GroupRaw,
			HandlerRaw:     parsed.HandlerRaw,
			Wrappers:       wrappers,
			RouteFunc:      group.routeFunc,
			StatementIndex: oldLine,
			SourceFamily:   "deleted_diff",
			File:           file,
			Span: facts.SourceSpan{
				File:      file,
				StartLine: anchorLine,
				EndLine:   anchorLine,
			},
		}
		store.Routes = append(store.Routes, route)
		store.Changes = append(store.Changes, facts.ChangeFact{
			ID:       fmt.Sprintf("change:%s:%s:%d:%d", facts.ChangeKindRouteDeleted, file, anchorLine, len(store.Changes)),
			Kind:     facts.ChangeKindRouteDeleted,
			TargetID: route.ID,
			File:     file,
			Ranges: []facts.ChangeRange{{
				StartLine: anchorLine,
				EndLine:   anchorLine,
			}},
			Source:     source + "_deleted_route",
			Confidence: facts.ConfidenceHigh,
		})
		addDeletedRouteDiagnostics(store, route, group.ok)
	}
}

func parseDeletedRouteCall(line string) (*ast.CallExpr, bool) {
	expr, err := parser.ParseExpr(strings.TrimSpace(line))
	if err != nil {
		return nil, false
	}
	call, ok := expr.(*ast.CallExpr)
	return call, ok
}

type deletedRouteGroup struct {
	id        string
	prefix    string
	routeFunc facts.SymbolID
	ok        bool
}

func resolveDeletedRouteGroup(file string, anchorLine int, groupVar string, store *facts.Store) deletedRouteGroup {
	var selected *facts.RouteGroupFact
	var fallback *facts.RouteGroupFact
	for i := range store.RouteGroups {
		group := &store.RouteGroups[i]
		if group.GroupVar != groupVar || filepath.ToSlash(group.Span.File) != file {
			continue
		}
		if fallback == nil || group.Span.StartLine < fallback.Span.StartLine {
			fallback = group
		}
		if group.Span.StartLine > anchorLine {
			continue
		}
		if selected == nil || group.Span.StartLine > selected.Span.StartLine {
			selected = group
		}
	}
	if selected == nil {
		selected = fallback
	}
	if selected != nil {
		return deletedRouteGroup{
			id:        selected.ID,
			prefix:    selected.Prefix,
			routeFunc: selected.RouteFunc,
			ok:        true,
		}
	}
	for _, route := range store.Routes {
		if route.GroupVar != groupVar || filepath.ToSlash(route.File) != file {
			continue
		}
		return deletedRouteGroup{
			id:        route.GroupID,
			prefix:    deriveRoutePrefix(route.ResolvedPath, route.LocalPath),
			routeFunc: route.RouteFunc,
			ok:        route.GroupID != "",
		}
	}
	return deletedRouteGroup{
		id: "deleted_route_group:" + file + ":" + groupVar,
	}
}

func deriveRoutePrefix(resolvedPath, localPath string) string {
	if resolvedPath == "" || localPath == "" || !strings.HasSuffix(resolvedPath, localPath) {
		return ""
	}
	prefix := strings.TrimSuffix(resolvedPath, localPath)
	if prefix == "" {
		return "/"
	}
	return prefix
}

func joinDeletedRoutePath(prefix, path string) string {
	if prefix == "" {
		return path
	}
	if path == "" {
		return prefix
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	out := strings.TrimRight(prefix, "/") + path
	if out == "" {
		return "/"
	}
	return strings.ReplaceAll(out, "//", "/")
}

func deletedRouteID(file, method, localPath string, oldLine, offset int) string {
	pathPart := localPath
	if pathPart == "" {
		pathPart = "dynamic"
	}
	return "route:deleted:" + file + ":" + method + ":" + pathPart + ":" + strconv.Itoa(oldLine) + ":" + strconv.Itoa(offset)
}

func addDeletedRouteDiagnostics(store *facts.Store, route facts.RouteRegistrationFact, groupResolved bool) {
	if route.PathRaw != "" {
		diagnostics.AddFact(store, diagnostics.Diagnostic{
			Code:           diagnostics.CodeDeletedRouteUnresolved,
			Severity:       diagnostics.SeverityWarning,
			Message:        "deleted route has dynamic path and cannot be resolved to an endpoint",
			Span:           route.Span,
			RelatedFactIDs: []string{route.ID},
		})
	}
	if !groupResolved && route.LocalPath != "" {
		diagnostics.AddFact(store, diagnostics.Diagnostic{
			Code:           diagnostics.CodeDeletedRouteEndpointFallback,
			Severity:       diagnostics.SeverityWarning,
			Message:        "deleted route group prefix could not be resolved; using local path as endpoint",
			Span:           route.Span,
			RelatedFactIDs: []string{route.ID},
		})
	}
	if route.HandlerRaw == "" {
		diagnostics.AddFact(store, diagnostics.Diagnostic{
			Code:           diagnostics.CodeDeletedRouteHandlerUnresolved,
			Severity:       diagnostics.SeverityWarning,
			Message:        "deleted route handler could not be resolved",
			Span:           route.Span,
			RelatedFactIDs: []string{route.ID},
		})
	}
}
