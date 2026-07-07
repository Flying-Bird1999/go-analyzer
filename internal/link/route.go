// route.go 提供注解按 handler 符号聚合的辅助索引，供 linker 建立 handler->annotation 关联。
package link

import (
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// annotationsByHandler 把 store 中的注解按 HandlerSymbol 聚合，并对每个 handler 的注解按 ID 排序。
// 排序保证后续生成的 handler_to_annotation 关联顺序稳定。
func annotationsByHandler(store *facts.Store) map[facts.SymbolID][]facts.AnnotationFact {
	out := map[facts.SymbolID][]facts.AnnotationFact{}
	for _, annotation := range store.Annotations {
		out[annotation.HandlerSymbol] = append(out[annotation.HandlerSymbol], annotation)
	}
	// 每个 handler 的注解按 ID 排序，保证输出确定性。
	for handler := range out {
		sort.Slice(out[handler], func(i, j int) bool {
			return out[handler][i].ID < out[handler][j].ID
		})
	}
	return out
}
