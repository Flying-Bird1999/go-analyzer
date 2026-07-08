// deleted_route.go 实现被删除路由注册的恢复：从 diff 删除块中解析 route call 与 handler 声明，
// 补充合成路由事实、handler 符号、注解事实与对应的 route_deleted / symbol_changed 变更根。
//
// 该文件是 ARCHITECTURE 第 8.4 节描述的"删除 route 定向增强"的实现入口。
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

// RecoverDeletedRoutes 遍历 diff 文件变更，对每个 .go 文件的删除块分别恢复被删除的路由注册与
// handler 声明，把合成事实写入 store 并追加对应的变更根。
//
// source 标识变更来源（默认 "git_diff"），会拼到变更事实的 Source 字段中，便于消费方区分。
// 非 .go 文件直接跳过；删除块逐块交给 recoverDeletedRoutesInBlock / recoverDeletedHandlersInBlock。
func RecoverDeletedRoutes(fileChanges []diff.FileChange, idx *astindex.Index, store *facts.Store, source string) {
	if source == "" {
		source = "git_diff"
	}
	for _, fileChange := range fileChanges {
		// 优先取变更后路径，缺失时退回旧路径（例如整文件删除场景）。
		file := filepath.ToSlash(fileChange.NewPath)
		if file == "" {
			file = filepath.ToSlash(fileChange.OldPath)
		}
		// 仅处理 Go 文件的删除块。
		if filepath.Ext(file) != ".go" {
			continue
		}
		for _, block := range fileChange.DeletedBlocks {
			recoverDeletedRoutesInBlock(file, block, idx, store, source)
			recoverDeletedHandlersInBlock(file, block, idx, store, source)
		}
	}
}

// recoverDeletedRoutesInBlock 在单个删除块中查找路由注册调用，把每条恢复出来的路由写入 store，
// 并追加一条 route_deleted 变更根。
//
// 关键步骤：解析删除行得到 route call；用变更后 route/group facts 恢复 group 前缀；
// 通过 link.LinkRoute 恢复 handler symbol 与注解；为路由与变更根补全诊断。
func recoverDeletedRoutesInBlock(file string, block diff.DeletedBlock, idx *astindex.Index, store *facts.Store, source string) {
	for _, candidate := range parseDeletedRouteCalls(block.Lines) {
		call := candidate.call
		// 复用正常 route call parser，避免删除恢复与正常提取产生语法漂移。
		parsed, ok := routeextract.ParseRouteCall(call)
		if !ok {
			continue
		}
		oldLine := block.OldStartLine + candidate.offset
		// anchorLine 是删除行在变更后源码中的近似行号，用作 span 与变更根定位。
		anchorLine := block.NewAnchorLine
		if anchorLine <= 0 {
			anchorLine = 1
		}
		// 从变更后 facts 尝试恢复 group id/prefix/route func。
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
		// 恢复 handler symbol 与注解；link 写回 route.HandlerSymbol 等字段。
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
		// 为动态 path、未恢复 group 前缀、未解析 handler 等情况补诊断。
		addDeletedRouteDiagnostics(store, route, group.ok)
	}
}

// deletedRouteCall 表示删除块中识别出的一条 route call 及其在删除块中的行偏移。
type deletedRouteCall struct {
	call   *ast.CallExpr
	offset int
}

// parseDeletedRouteCalls 把删除块的多行文本包装进临时 Go function 解析出所有 CallExpr，
// 用于支持多行 route call（例如参数换行的注册语句）。包装失败时退化为按行单行解析。
func parseDeletedRouteCalls(lines []string) []deletedRouteCall {
	// 用临时 package + function 包裹删除行，使多行 route call 能被完整解析。
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
			// 行号 -3 是因为包装模板在删除行前有 package 行、func 行、删除首行之前的空白。
			offset := fset.Position(call.Pos()).Line - 3
			if offset >= 0 && offset < len(lines) {
				out = append(out, deletedRouteCall{call: call, offset: offset})
			}
			return true
		})
	}
	if len(out) == 0 {
		// 临时 function 解析失败时退化为按行单行解析，仍能覆盖单行 route call。
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

// parseDeletedRouteLine 把单行文本作为表达式解析成 CallExpr，仅用于单行 route call 兜底。
func parseDeletedRouteLine(line string) (*ast.CallExpr, bool) {
	expr, err := parser.ParseExpr(strings.TrimSpace(line))
	if err != nil {
		return nil, false
	}
	call, ok := expr.(*ast.CallExpr)
	return call, ok
}

// deletedHandlerDecl 表示从删除块中解析出的 FuncDecl 及其在删除块中的起止行偏移。
type deletedHandlerDecl struct {
	fn          *ast.FuncDecl
	startOffset int
	endOffset   int
}

// recoverDeletedHandlersInBlock 在删除块中恢复被删除的 handler 声明（function/receiver method），
// 重建对应符号、注解与 symbol_changed 变更根，并尝试重新 link 之前未解析的路由。
//
// 这是 ARCHITECTURE 第 8.4 节描述的"恢复 handler symbol 与 annotation"的实现。
// 恢复出的 handler 视为 medium 置信度（基于单快照 + anchor 推断）。
func recoverDeletedHandlersInBlock(file string, block diff.DeletedBlock, idx *astindex.Index, store *facts.Store, source string) {
	packagePath := deletedFilePackagePath(file, idx, store)
	if packagePath == "" {
		// 无法定位包路径则不能产生稳定 symbol ID，直接放弃恢复。
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
			// 已存在的符号不重复恢复，避免与现存事实冲突。
			continue
		}
		if idx != nil {
			idx.Symbols[symbol.ID] = symbol
		}
		store.AddSymbol(symbol)
		// 同步恢复 handler 上的 HTTP 注解，使其与现存符号建立关联。
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
		// 恢复出具体 handler 后，移除同一删除块范围内的 file 降级根，
		// 改由精确 symbol 根承接传播。
		removeDeletedBlockFileFallbackChange(store, file, anchorLine, anchorLine+len(block.Lines))
		// 重新 link 之前 handler 未解析的路由，可能因恢复的 handler 而获得解析。
		relinkUnresolvedRoutesForDeletedHandler(idx, store, symbol.ID)
	}
}

// parseDeletedHandlerDecls 把删除块包装成临时 package 文件解析出所有 FuncDecl，
// 并记录每个声明在删除块中的起止行偏移，用于后续计算变更后近似行号。
func parseDeletedHandlerDecls(lines []string) []deletedHandlerDecl {
	// 用临时 package 包裹删除行，使完整函数声明（含注释）可以被解析。
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
		// 行号 -2 抵消 package 行与删除首行之间的偏移，得到删除块内偏移。
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

// deletedHandlerSymbol 根据函数声明构造对应的符号事实：
// 普通函数使用 FunctionSymbolID，receiver method 使用 MethodSymbolID。
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
	receiver := astindex.ReceiverTypeName(fn.Recv.List[0].Type)
	return facts.SymbolFact{
		ID:          astindex.MethodSymbolID(packagePath, receiver, fn.Name.Name),
		Kind:        "method",
		PackagePath: packagePath,
		Receiver:    receiver,
		Name:        fn.Name.Name,
		Span:        span,
	}
}

// deletedAnnotationID 为恢复出的注解生成稳定 ID，包含 handler、method、path 与序号。
func deletedAnnotationID(handler facts.SymbolID, method, routePath string, index int) string {
	return "annotation:" + string(handler) + ":" + method + ":" + routePath + ":" + strconv.Itoa(index)
}

// deletedFilePackagePath 推断删除块所属文件的 package path：
// 优先在 idx 中按文件名匹配；匹配不到则按 module path + 文件目录拼出。
// 无法确定时返回空字符串，调用方据此放弃 handler 恢复。
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

// packagePathFromFile 由 module path 与文件相对路径拼出 package path。
// 根目录返回 module path；其它目录拼接 module path + 文件目录。
func packagePathFromFile(modulePath, file string) string {
	dir := path.Dir(filepath.ToSlash(file))
	if dir == "." || dir == "/" {
		return modulePath
	}
	return strings.TrimRight(modulePath, "/") + "/" + dir
}

// symbolExists 判断给定符号 ID 是否已存在于 store，避免恢复时与现存符号冲突。
func symbolExists(store *facts.Store, id facts.SymbolID) bool {
	for _, symbol := range store.Symbols {
		if symbol.ID == id {
			return true
		}
	}
	return false
}

// relinkUnresolvedRoutesForDeletedHandler 在恢复出 handler 后重新尝试 link 此前 handler 为空的路由：
// 如果 link 后命中刚恢复的 handler，则该路由获得解析。
func relinkUnresolvedRoutesForDeletedHandler(idx *astindex.Index, store *facts.Store, handler facts.SymbolID) {
	if idx == nil || store == nil || handler == "" {
		return
	}
	for i := range store.Routes {
		// 只重新 link 之前未解析 handler 的路由，避免影响已解析路由。
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

// removeDeletedBlockFileFallbackChange 删除同一文件、且范围覆盖删除块的 file 降级变更根。
// 恢复出精确 handler 后，原本的 file fallback 不再需要，移除以免重复传播。
func removeDeletedBlockFileFallbackChange(store *facts.Store, file string, startLine, endLine int) {
	if store == nil {
		return
	}
	file = filepath.ToSlash(file)
	// 复用底层切片就地过滤，避免重新分配。
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

// changeRangesOverlap 判断任一变更 range 与 [startLine, endLine] 是否存在行重叠。
func changeRangesOverlap(ranges []facts.ChangeRange, startLine, endLine int) bool {
	for _, item := range ranges {
		if item.EndLine >= startLine && item.StartLine <= endLine {
			return true
		}
	}
	return false
}

// deletedRouteGroup 是被删除路由所在 group 的恢复结果。
type deletedRouteGroup struct {
	id        string
	prefix    string
	routeFunc facts.SymbolID
	ok        bool
}

// resolveDeletedRouteGroup 在变更后 facts 中尝试恢复被删除路由所属的 group：
//  1. 在同文件同 group 变量中选择"声明行最接近且早于删除行"的 group；
//  2. 若无满足条件的 group，退化为同文件同变量中最早的 group；
//  3. 仍找不到时，回退到同 group 变量的路由（推断 prefix）；
//  4. 都失败时使用合成的占位 group ID，prefix 留空（后续走 local path 降级）。
func resolveDeletedRouteGroup(file string, anchorLine int, groupVar string, store *facts.Store) deletedRouteGroup {
	var selected *facts.RouteGroupFact
	var fallback *facts.RouteGroupFact
	for i := range store.RouteGroups {
		group := &store.RouteGroups[i]
		if group.GroupVar != groupVar || filepath.ToSlash(group.Span.File) != file {
			continue
		}
		// 记录同文件同变量中最早声明的 group 作为兜底。
		if fallback == nil || group.Span.StartLine < fallback.Span.StartLine {
			fallback = group
		}
		// 只考虑声明早于删除行的 group（删除路由应属于"在其之前"声明的 group）。
		if group.Span.StartLine > anchorLine {
			continue
		}
		// 在剩余 group 中选声明最晚（最接近删除行）的一个。
		if selected == nil || group.Span.StartLine > selected.Span.StartLine {
			selected = group
		}
	}
	if selected == nil {
		// 无早于删除行的 group 时退化为最早的同变量 group。
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
	// group fact 缺失时，尝试用同 group 变量的路由反推 prefix。
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
	// 全部失败：使用合成占位 group ID，prefix 为空，触发后续 local path 降级。
	return deletedRouteGroup{
		id: "deleted_route_group:" + file + ":" + groupVar,
	}
}

// deriveRoutePrefix 由已解析路径与 local path 反推 group prefix：
// 当 resolved path 以 local path 为后缀时取其前缀，否则返回空。
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

// joinDeletedRoutePath 把 group prefix 与 local path 拼成完整路径，
// 处理首尾斜杠与重复斜杠，确保结果路径规范。
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
	out = strings.ReplaceAll(out, "//", "/")
	// 与 route.joinPath 保持一致的尾斜杠归一：避免同一逻辑端点在删除前后
	// 路径字符串不同（如 group.GET("/") 在活路由得 /api、删除路由曾得 /api/）。
	if len(out) > 1 {
		out = strings.TrimRight(out, "/")
	}
	return out
}

// deletedRouteID 为被删除路由生成稳定 ID，包含文件、method、local path、旧行号与偏移，
// 动态 path 用 "dynamic" 占位。
func deletedRouteID(file, method, localPath string, oldLine, offset int) string {
	pathPart := localPath
	if pathPart == "" {
		pathPart = "dynamic"
	}
	return "route:deleted:" + file + ":" + method + ":" + pathPart + ":" + strconv.Itoa(oldLine) + ":" + strconv.Itoa(offset)
}

// addDeletedRouteDiagnostics 为被删除路由补诊断：
//   - 动态 path（PathRaw 非空）无法解析出端点；
//   - group 前缀未恢复但有 local path，会使用 local path 作为端点降级；
//   - handler symbol 未能解析。
//
// 这些诊断属于可恢复的不确定性，不阻断分析。
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
