package impact

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	annotationextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/annotation"
	routeextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/route"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/link"
)

func RecoverDeletedRoutes(fileChanges []diff.FileChange, idx *astindex.Index, store *facts.Store, source string) {
	if source == "" {
		source = "git_diff"
	}
	for _, fileChange := range fileChanges {
		file := filepath.ToSlash(fileChange.NewPath)
		if file == "" {
			file = filepath.ToSlash(fileChange.OldPath)
		}
		if filepath.Ext(file) != ".go" {
			continue
		}
		for _, block := range fileChange.DeletedBlocks {
			recoverDeletedRoutesInBlock(file, block, idx, store, source)
			recoverDeletedHandlersInBlock(file, block, idx, store, source)
		}
	}
}

func recoverDeletedRoutesInBlock(file string, block diff.DeletedBlock, idx *astindex.Index, store *facts.Store, source string) {
	for _, candidate := range parseDeletedRouteCalls(block.Lines) {
		call := candidate.call
		parsed, ok := routeextract.ParseRouteCall(call)
		if !ok {
			continue
		}
		oldLine := block.OldStartLine + candidate.offset
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
			ID:                deletedRouteID(file, parsed.Method, parsed.LocalPath, oldLine, candidate.offset),
			Method:            parsed.Method,
			LocalPath:         parsed.LocalPath,
			PathRaw:           parsed.PathRaw,
			ResolvedPath:      resolvedPath,
			GroupID:           group.id,
			GroupVar:          parsed.GroupRaw,
			HandlerRaw:        parsed.HandlerRaw,
			Wrappers:          wrappers,
			RouteFunc:         group.routeFunc,
			StatementIndex:    oldLine,
			RecoveredFromDiff: true,
			File:              file,
			Span: facts.SourceSpan{
				File:      file,
				StartLine: anchorLine,
				EndLine:   anchorLine,
			},
		}
		link.LinkRoute(idx, store, &route)
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

type deletedRouteCall struct {
	call   *ast.CallExpr
	offset int
}

func parseDeletedRouteCalls(lines []string) []deletedRouteCall {
	source := "package deleted\nfunc recover() {\n" + strings.Join(lines, "\n") + "\n}\n"
	fset := token.NewFileSet()
	file, _ := parser.ParseFile(fset, "deleted.go", source, parser.AllErrors)
	var out []deletedRouteCall
	if file != nil {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			offset := fset.Position(call.Pos()).Line - 3
			if offset >= 0 && offset < len(lines) {
				out = append(out, deletedRouteCall{call: call, offset: offset})
			}
			return true
		})
	}
	if len(out) == 0 {
		for offset, line := range lines {
			if call, ok := parseDeletedRouteLine(line); ok {
				out = append(out, deletedRouteCall{call: call, offset: offset})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].offset != out[j].offset {
			return out[i].offset < out[j].offset
		}
		return out[i].call.Pos() < out[j].call.Pos()
	})
	return out
}

func parseDeletedRouteLine(line string) (*ast.CallExpr, bool) {
	expr, err := parser.ParseExpr(strings.TrimSpace(line))
	if err != nil {
		return nil, false
	}
	call, ok := expr.(*ast.CallExpr)
	return call, ok
}

type deletedHandlerDecl struct {
	fn          *ast.FuncDecl
	startOffset int
	endOffset   int
}

func recoverDeletedHandlersInBlock(file string, block diff.DeletedBlock, idx *astindex.Index, store *facts.Store, source string) {
	packagePath := deletedFilePackagePath(file, idx, store)
	if packagePath == "" {
		return
	}
	for _, candidate := range parseDeletedHandlerDecls(block.Lines) {
		fn := candidate.fn
		if fn.Name == nil || fn.Name.Name == "" {
			continue
		}
		anchorLine := block.NewAnchorLine
		if anchorLine <= 0 {
			anchorLine = block.OldStartLine
		}
		if anchorLine <= 0 {
			anchorLine = 1
		}
		startLine := anchorLine + candidate.startOffset
		if startLine <= 0 {
			startLine = anchorLine
		}
		endLine := anchorLine + candidate.endOffset
		if endLine < startLine {
			endLine = startLine
		}
		symbol := deletedHandlerSymbol(packagePath, file, fn, facts.SourceSpan{
			File:      file,
			StartLine: startLine,
			EndLine:   endLine,
		})
		if symbol.ID == "" || symbolExists(store, symbol.ID) {
			continue
		}
		if idx != nil {
			idx.Symbols[symbol.ID] = symbol
		}
		store.AddSymbol(symbol)
		annotations := annotationextract.ParseAPIAnnotations(fn.Doc)
		for index, annotation := range annotations {
			store.Annotations = append(store.Annotations, facts.AnnotationFact{
				ID:            deletedAnnotationID(symbol.ID, annotation.Method, annotation.Path, index),
				Kind:          "annotation",
				Method:        annotation.Method,
				Path:          annotation.Path,
				Raw:           annotation.Raw,
				HandlerSymbol: symbol.ID,
				Span:          symbol.Span,
			})
		}
		store.Changes = append(store.Changes, facts.ChangeFact{
			ID:       fmt.Sprintf("change:%s:%s:%d:%d", facts.ChangeKindSymbolChanged, file, startLine, len(store.Changes)),
			Kind:     facts.ChangeKindSymbolChanged,
			TargetID: string(symbol.ID),
			SymbolID: symbol.ID,
			File:     file,
			Ranges: []facts.ChangeRange{{
				StartLine: startLine,
				EndLine:   endLine,
			}},
			Source:     source + "_deleted_handler",
			Confidence: facts.ConfidenceMedium,
		})
		removeDeletedBlockFileFallbackChange(store, file, anchorLine, anchorLine+len(block.Lines))
		relinkUnresolvedRoutesForDeletedHandler(idx, store, symbol.ID)
	}
}

func parseDeletedHandlerDecls(lines []string) []deletedHandlerDecl {
	source := "package deleted\n" + strings.Join(lines, "\n") + "\n"
	fset := token.NewFileSet()
	file, _ := parser.ParseFile(fset, "deleted.go", source, parser.ParseComments|parser.AllErrors)
	var out []deletedHandlerDecl
	if file == nil {
		return out
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		startOffset := fset.Position(fn.Pos()).Line - 2
		endOffset := fset.Position(fn.End()).Line - 2
		if startOffset < 0 {
			startOffset = 0
		}
		if startOffset >= len(lines) {
			startOffset = len(lines) - 1
		}
		if endOffset < startOffset {
			endOffset = startOffset
		}
		if endOffset >= len(lines) {
			endOffset = len(lines) - 1
		}
		out = append(out, deletedHandlerDecl{fn: fn, startOffset: startOffset, endOffset: endOffset})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].startOffset != out[j].startOffset {
			return out[i].startOffset < out[j].startOffset
		}
		return out[i].fn.Name.Name < out[j].fn.Name.Name
	})
	return out
}

func deletedHandlerSymbol(packagePath, file string, fn *ast.FuncDecl, span facts.SourceSpan) facts.SymbolFact {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return facts.SymbolFact{
			ID:          astindex.FunctionSymbolID(packagePath, fn.Name.Name),
			Kind:        "func",
			PackagePath: packagePath,
			Name:        fn.Name.Name,
			Span:        span,
		}
	}
	receiver := deletedReceiverTypeName(fn.Recv.List[0].Type)
	return facts.SymbolFact{
		ID:          astindex.MethodSymbolID(packagePath, receiver, fn.Name.Name),
		Kind:        "method",
		PackagePath: packagePath,
		Receiver:    receiver,
		Name:        fn.Name.Name,
		Span:        span,
	}
}

func deletedReceiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return deletedReceiverTypeName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.IndexExpr:
		return deletedReceiverTypeName(t.X)
	case *ast.IndexListExpr:
		return deletedReceiverTypeName(t.X)
	default:
		return ""
	}
}

func deletedAnnotationID(handler facts.SymbolID, method, routePath string, index int) string {
	return "annotation:" + string(handler) + ":" + method + ":" + routePath + ":" + strconv.Itoa(index)
}

func deletedFilePackagePath(file string, idx *astindex.Index, store *facts.Store) string {
	file = filepath.ToSlash(file)
	if idx != nil && idx.Project != nil {
		for _, pkg := range idx.Project.Packages {
			for _, projectFile := range pkg.Files {
				rel, err := filepath.Rel(idx.Project.Root, projectFile.Path)
				if err != nil {
					continue
				}
				if filepath.ToSlash(rel) == file {
					return pkg.Path
				}
			}
		}
		if idx.Project.ModulePath != "" {
			return packagePathFromFile(idx.Project.ModulePath, file)
		}
	}
	if store != nil && store.Project.ModulePath != "" {
		return packagePathFromFile(store.Project.ModulePath, file)
	}
	return ""
}

func packagePathFromFile(modulePath, file string) string {
	dir := path.Dir(filepath.ToSlash(file))
	if dir == "." || dir == "/" {
		return modulePath
	}
	return strings.TrimRight(modulePath, "/") + "/" + dir
}

func symbolExists(store *facts.Store, id facts.SymbolID) bool {
	for _, symbol := range store.Symbols {
		if symbol.ID == id {
			return true
		}
	}
	return false
}

func relinkUnresolvedRoutesForDeletedHandler(idx *astindex.Index, store *facts.Store, handler facts.SymbolID) {
	if idx == nil || store == nil || handler == "" {
		return
	}
	for i := range store.Routes {
		if store.Routes[i].HandlerSymbol != "" {
			continue
		}
		if !link.LinkRoute(idx, store, &store.Routes[i]) {
			continue
		}
		if store.Routes[i].HandlerSymbol != handler {
			continue
		}
	}
}

func removeDeletedBlockFileFallbackChange(store *facts.Store, file string, startLine, endLine int) {
	if store == nil {
		return
	}
	file = filepath.ToSlash(file)
	filtered := store.Changes[:0]
	for _, change := range store.Changes {
		if change.Kind == facts.ChangeKindFileChanged &&
			filepath.ToSlash(change.File) == file &&
			changeRangesOverlap(change.Ranges, startLine, endLine) {
			continue
		}
		filtered = append(filtered, change)
	}
	store.Changes = filtered
}

func changeRangesOverlap(ranges []facts.ChangeRange, startLine, endLine int) bool {
	for _, item := range ranges {
		if item.EndLine >= startLine && item.StartLine <= endLine {
			return true
		}
	}
	return false
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
	if route.HandlerSymbol == "" {
		diagnostics.AddFact(store, diagnostics.Diagnostic{
			Code:           diagnostics.CodeDeletedRouteHandlerUnresolved,
			Severity:       diagnostics.SeverityWarning,
			Message:        "deleted route handler could not be resolved to a project symbol",
			Span:           route.Span,
			RelatedFactIDs: []string{route.ID},
		})
	}
}
