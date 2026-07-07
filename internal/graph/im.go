// im.go 实现 IM 图视图：把 facts.Store 中的 IM 事件按 sender 聚合，并按当前
// 传播 path 上的 payload/event/control 依赖精确匹配，避免同一 sender 内多个
// event 因宽泛匹配而产生误报。
package graph

import (
	"path/filepath"
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// IMEventMatch 是一次 IM 事件匹配结果：命中事件本身及其匹配到的关系类型。
type IMEventMatch struct {
	// Fact 命中的 IM 事件事实。
	Fact facts.IMEventFact
	// Relation 命中的关系类型（Payload / EventValue / Control）。
	Relation facts.IMEventRelation
}

// IMGraph 是 IM 图：按 sender 聚合 IM 事件，供 EventsForPath 按传播 path 精确查询。
type IMGraph struct {
	// bySender 按 sender symbol 聚合的 IM 事件，已按 Event、ID 排序。
	bySender map[facts.SymbolID][]facts.IMEventFact
}

// imRelationPriority 定义依赖/证据关系的匹配优先级：载荷（Payload）最高，
// 其次事件取值（EventValue），最后控制（Control）。同一事件命中多种关系时，
// 优先返回靠前的关系类型。
var imRelationPriority = []facts.IMEventRelation{
	facts.IMRelationPayload,
	facts.IMRelationEventValue,
	facts.IMRelationControl,
}

// NewIMGraph 扫描 store 中全部 IM 事件，按 sender 聚合并排序后构造 IM 图。
// 没有 sender symbol 的事件会被跳过。
func NewIMGraph(store *facts.Store) *IMGraph {
	graph := &IMGraph{bySender: map[facts.SymbolID][]facts.IMEventFact{}}
	for _, event := range store.IMEvents {
		if event.SenderSymbol == "" {
			continue
		}
		graph.bySender[event.SenderSymbol] = append(graph.bySender[event.SenderSymbol], event)
	}
	// 每个 sender 下的事件按 Event 名称、ID 排序，保证输出稳定。
	for sender := range graph.bySender {
		sort.Slice(graph.bySender[sender], func(i, j int) bool {
			left := graph.bySender[sender][i]
			right := graph.bySender[sender][j]
			if left.Event != right.Event {
				return left.Event < right.Event
			}
			return left.ID < right.ID
		})
	}
	return graph
}

// EventsForPath 返回 sender 下与当前传播 path 相交的 IM 事件。
// 匹配分两类，按优先级：
//  1. 依赖匹配（matchIMDependency）：事件的某个依赖 symbol 出现在传播 path 上。
//  2. 直接证据匹配（matchIMEvidence）：仅当变更的 symbol 恰好是 sender 自身时，
//     检查变更行范围是否落在事件的证据 span 内，用于 sender 函数体被直接改动的情况。
//
// 若变更的不是 sender 自身且依赖匹配失败，则跳过该事件，避免误报。
func (g *IMGraph) EventsForPath(
	sender facts.SymbolID,
	path map[facts.SymbolID]bool,
	change facts.ChangeFact,
) []IMEventMatch {
	var out []IMEventMatch
	for _, event := range g.bySender[sender] {
		// 优先按传播 path 上的依赖匹配：path 表示当前影响传播链路上涉及的 symbol 集合。
		if relation, ok := matchIMDependency(event, path); ok {
			out = append(out, IMEventMatch{Fact: event, Relation: relation})
			continue
		}
		// 依赖未命中时，仅在变更对象就是 sender 本身时走直接证据匹配。
		if change.SymbolID != sender {
			continue
		}
		if relation, ok := matchIMEvidence(event, change); ok {
			out = append(out, IMEventMatch{Fact: event, Relation: relation})
		}
	}
	return out
}

// matchIMDependency 按优先级顺序检查事件的依赖：若某依赖 symbol 出现在当前传播
// path 上，则返回匹配到的关系类型。优先级见 imRelationPriority。
func matchIMDependency(event facts.IMEventFact, path map[facts.SymbolID]bool) (facts.IMEventRelation, bool) {
	for _, relation := range imRelationPriority {
		for _, dependency := range event.Dependencies {
			if dependency.Relation == relation && path[dependency.SymbolID] {
				return relation, true
			}
		}
	}
	return "", false
}

// matchIMEvidence 按 sender 被直接改动的场景匹配：检查变更的文件与行范围是否
// 与事件的某个证据 span 相交（同文件且行重叠）。优先级见 imRelationPriority。
func matchIMEvidence(event facts.IMEventFact, change facts.ChangeFact) (facts.IMEventRelation, bool) {
	for _, relation := range imRelationPriority {
		for _, evidence := range event.Evidence {
			// 同一文件才可能相交：统一转 slash 以兼容不同平台路径分隔符。
			if evidence.Relation != relation ||
				filepath.ToSlash(evidence.Span.File) != filepath.ToSlash(change.File) {
				continue
			}
			for _, changed := range change.Ranges {
				if rangesOverlap(changed.StartLine, changed.EndLine, evidence.Span.StartLine, evidence.Span.EndLine) {
					return relation, true
				}
			}
		}
	}
	return "", false
}

// rangesOverlap 判断两个行区间是否重叠。EndLine 为 0 时视为单行（等于 StartLine），
// 以兼容只记录起始行的事实。
func rangesOverlap(leftStart, leftEnd, rightStart, rightEnd int) bool {
	if leftEnd == 0 {
		leftEnd = leftStart
	}
	if rightEnd == 0 {
		rightEnd = rightStart
	}
	return leftStart <= rightEnd && rightStart <= leftEnd
}
