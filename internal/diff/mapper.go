// mapper.go 实现把解析后的 diff 行范围映射到最精确语义根的逻辑。
package diff

import (
	"fmt"
	"path/filepath"

	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// MapChanges 把每个 FileChange 的行范围逐一映射为 ChangeFact。
//
// 映射按领域事实优先级选择最精确的语义根：注解 -> route group -> route ->
// 中间件 -> 最小包含符号 -> 文件 fallback。新增行使用 high confidence，
// 删除锚点行使用 medium confidence。删除锚点若最终落到 file fallback，
// 会额外记录 deleted_symbol_unresolved 诊断，表明无法在单快照下精确恢复被删除符号。
// source 标记 ChangeFact 的来源（如 git_diff），写入每个 ChangeFact.Source。
func MapChanges(changes []FileChange, store *facts.Store, source string) []facts.ChangeFact {
	var out []facts.ChangeFact
	// 先按文件建立领域事实索引，避免对每个行号都线性扫描整个 Store。
	index := newChangeIndex(store)
	for _, fileChange := range changes {
		// 删除文件的 NewPath 为空，回退到 OldPath 作为定位文件。
		file := filepath.ToSlash(fileChange.NewPath)
		if file == "" {
			file = filepath.ToSlash(fileChange.OldPath)
		}
		for _, r := range fileChange.Ranges {
			changeRange := facts.ChangeRange{StartLine: r.StartLine, EndLine: r.EndLine}
			// 默认新增/上下文行为 high；删除锚点（落在 surviving 内容上）为 medium。
			confidence := facts.ConfidenceHigh
			if r.Kind == RangeKindDeletionAnchor {
				confidence = facts.ConfidenceMedium
			}
			mapped := mapRange(file, changeRange, index, source, len(out), confidence)
			out = append(out, mapped...)
			if r.Kind == RangeKindDeletionAnchor {
				// 删除锚点若只能落到 file fallback，说明该声明在变更后已不存在，
				// 单快照无法精确恢复，记录诊断供后续 review。
				for _, item := range mapped {
					if item.Kind != facts.ChangeKindFileChanged {
						continue
					}
					diagnostics.AddFact(store, diagnostics.Diagnostic{
						Code:     diagnostics.CodeDeletedSymbolUnresolved,
						Severity: diagnostics.SeverityWarning,
						Message:  "deleted lines could not be mapped to a surviving symbol",
						Span: facts.SourceSpan{
							File:      file,
							StartLine: r.StartLine,
							EndLine:   r.EndLine,
						},
						RelatedFactIDs: []string{item.ID},
					})
				}
			}
		}
	}
	return out
}

// changeIndex 把 Store 中的领域事实按文件分组缓存，供映射时按文件快速查找。
// 各字段保存对应类型的事实切片，key 为归一化后的项目相对路径。
type changeIndex struct {
	annotations map[string][]facts.AnnotationFact
	groups      map[string][]facts.RouteGroupFact
	routes      map[string][]facts.RouteRegistrationFact
	middleware  map[string][]facts.MiddlewareBindingFact
	symbols     map[string][]facts.SymbolFact
}

// newChangeIndex 遍历 Store 中所有领域事实，按其所属文件构建索引。
// 同一文件的多条事实按 slice 保存，保留 Store 中的原始顺序。
func newChangeIndex(store *facts.Store) changeIndex {
	index := changeIndex{
		annotations: map[string][]facts.AnnotationFact{},
		groups:      map[string][]facts.RouteGroupFact{},
		routes:      map[string][]facts.RouteRegistrationFact{},
		middleware:  map[string][]facts.MiddlewareBindingFact{},
		symbols:     map[string][]facts.SymbolFact{},
	}
	for _, annotation := range store.Annotations {
		file := filepath.ToSlash(annotation.Span.File)
		index.annotations[file] = append(index.annotations[file], annotation)
	}
	for _, group := range store.RouteGroups {
		file := filepath.ToSlash(group.Span.File)
		index.groups[file] = append(index.groups[file], group)
	}
	for _, route := range store.Routes {
		file := filepath.ToSlash(route.Span.File)
		index.routes[file] = append(index.routes[file], route)
	}
	for _, binding := range store.Middleware {
		file := filepath.ToSlash(binding.Span.File)
		index.middleware[file] = append(index.middleware[file], binding)
	}
	for _, symbol := range store.Symbols {
		file := filepath.ToSlash(symbol.Span.File)
		index.symbols[file] = append(index.symbols[file], symbol)
	}
	return index
}

// mapRange 把一个行范围拆成逐行映射，再把相邻且命中间一目标的行合并回一个范围。
// 这样跨多行但落在同一符号内的变更只会产生一条 ChangeFact，避免碎片化。
// baseIndex 是当前文件之前已产出的 ChangeFact 数量，用于给每条 fact 分配唯一序号。
func mapRange(file string, r facts.ChangeRange, index changeIndex, source string, baseIndex int, confidence facts.Confidence) []facts.ChangeFact {
	var out []facts.ChangeFact
	for line := r.StartLine; line <= r.EndLine; line++ {
		point := facts.ChangeRange{StartLine: line, EndLine: line}
		mapped := mapPoint(file, point, index, source, baseIndex+len(out), confidence)
		// 若当前行命中目标与上一条完全相同、且行号恰好紧接，则延长上一条范围而不是新建。
		if len(out) > 0 && sameChangeTarget(out[len(out)-1], mapped) && out[len(out)-1].Ranges[0].EndLine+1 == line {
			out[len(out)-1].Ranges[0].EndLine = line
			continue
		}
		out = append(out, mapped)
	}
	return out
}

// mapPoint 对单个行号按领域优先级查找最精确的语义根。
// 优先级为：annotation -> route group -> route -> middleware -> 最小包含 symbol -> file。
// 当多个 symbol 同时包含该行时，选择行跨度最小的（最具体的声明），跨度相同则按 ID 取字典序最小者保证稳定。
func mapPoint(file string, r facts.ChangeRange, index changeIndex, source string, baseIndex int, confidence facts.Confidence) facts.ChangeFact {
	// 1. 注解：diff 命中注释行优先归为 annotation_changed，保证不会把注释变更错记到函数体。
	for _, annotation := range index.annotations[file] {
		if spanContains(annotation.Span, file, r) {
			return changeFact(baseIndex, facts.ChangeKindAnnotationChanged, annotation.ID, annotation.HandlerSymbol, file, r, source, confidence)
		}
	}
	// 2. route group：group 创建/前缀行优先于外层 route 注册函数 symbol。
	for _, group := range index.groups[file] {
		if spanContains(group.Span, file, r) {
			return changeFact(baseIndex, facts.ChangeKindRouteGroupChanged, group.ID, group.RouteFunc, file, r, source, confidence)
		}
	}
	// 3. route 注册行优先于其所在函数 symbol。
	for _, route := range index.routes[file] {
		if spanContains(route.Span, file, r) {
			return changeFact(baseIndex, facts.ChangeKindRouteChanged, route.ID, route.HandlerSymbol, file, r, source, confidence)
		}
	}
	// 4. 中间件绑定行优先于外层 symbol。
	for _, binding := range index.middleware[file] {
		if spanContains(binding.Span, file, r) {
			return changeFact(baseIndex, facts.ChangeKindMiddlewareChanged, binding.ID, "", file, r, source, confidence)
		}
	}
	// 5. symbol：选最小包含声明。外层 type 包含内层 var 时优先选内层更具体的 var。
	var selected *facts.SymbolFact
	for _, symbol := range index.symbols[file] {
		if spanContains(symbol.Span, file, r) {
			if selected == nil || spanSize(symbol.Span) < spanSize(selected.Span) ||
				(spanSize(symbol.Span) == spanSize(selected.Span) && symbol.ID < selected.ID) {
				candidate := symbol
				selected = &candidate
			}
		}
	}
	if selected != nil {
		return changeFact(baseIndex, facts.ChangeKindSymbolChanged, string(selected.ID), selected.ID, file, r, source, confidence)
	}
	// 6. 兜底：无法映射到任何语义事实时落到 file root，confidence 为 low。
	return changeFact(baseIndex, facts.ChangeKindFileChanged, file, "", file, r, source, facts.ConfidenceLow)
}

// sameChangeTarget 判断两条 ChangeFact 是否命中同一目标（kind/target/symbol/file/source/confidence 全等），
// 用于 mapRange 把相邻同目标行合并成一条范围。
func sameChangeTarget(left, right facts.ChangeFact) bool {
	return left.Kind == right.Kind &&
		left.TargetID == right.TargetID &&
		left.SymbolID == right.SymbolID &&
		left.File == right.File &&
		left.Source == right.Source &&
		left.Confidence == right.Confidence
}

// spanSize 返回声明 span 的行跨度（EndLine - StartLine），用于在嵌套声明中选最具体的一个。
func spanSize(span facts.SourceSpan) int {
	return span.EndLine - span.StartLine
}

// spanContains 判断行范围 r 是否完全落在 span 内，且二者文件一致。
// 文件比较统一用斜杠形式，避免 Windows 路径分隔符差异。
func spanContains(span facts.SourceSpan, file string, r facts.ChangeRange) bool {
	if filepath.ToSlash(span.File) != filepath.ToSlash(file) {
		return false
	}
	return r.StartLine >= span.StartLine && r.EndLine <= span.EndLine
}

// changeFact 构造一条 ChangeFact，ID 由 kind、file、起止行和全局序号拼接而成，保证唯一且确定。
func changeFact(index int, kind facts.ChangeKind, targetID string, symbolID facts.SymbolID, file string, r facts.ChangeRange, source string, confidence facts.Confidence) facts.ChangeFact {
	return facts.ChangeFact{
		ID:         fmt.Sprintf("change:%s:%s:%d:%d:%d", kind, file, r.StartLine, r.EndLine, index),
		Kind:       kind,
		TargetID:   targetID,
		SymbolID:   symbolID,
		File:       file,
		Ranges:     []facts.ChangeRange{r},
		Source:     source,
		Confidence: confidence,
	}
}
