// json.go 实现 facts 命令的 JSON 投影：拷贝 Store 事实、按 ID 稳定排序各类数组，
// 并把 nil 切片归一化为空数组，保证 facts 输出确定性。
package output

import (
	"encoding/json"
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// RenderJSON 把 facts.Store 序列化为缩进 JSON，末尾追加换行。
//
// 步骤：用 append(nil, src...) 拷贝每类事实避免污染 Store；按 ID 字典序稳定排序；
// nil 切片转为空切片使 JSON 输出 "[]" 而非 null；最后 MarshalIndent 序列化。
// 该函数是 facts 命令的对外契约实现，相同 Store 产出字节级一致输出。
func RenderJSON(store *facts.Store) ([]byte, error) {
	doc := Document{
		Project:          store.Project,
		Symbols:          append([]facts.SymbolFact(nil), store.Symbols...),
		Annotations:      append([]facts.AnnotationFact(nil), store.Annotations...),
		RouteGroups:      append([]facts.RouteGroupFact(nil), store.RouteGroups...),
		Routes:           append([]facts.RouteRegistrationFact(nil), store.Routes...),
		Middleware:       append([]facts.MiddlewareBindingFact(nil), store.Middleware...),
		References:       append([]facts.ReferenceFact(nil), store.References...),
		Modules:          append([]facts.ModuleDependencyFact(nil), store.Modules...),
		IMEvents:         append([]facts.IMEventFact(nil), store.IMEvents...),
		GrpcOperations:   append([]facts.GrpcOperationFact(nil), store.GrpcOperations...),
		GrpcCalls:        append([]facts.GrpcCallFact(nil), store.GrpcCalls...),
		GrpcProviders:    append([]facts.GrpcProviderFact(nil), store.GrpcProviders...),
		DubboProviders:   append([]facts.DubboProviderFact(nil), store.DubboProviders...),
		JobRegistrations: append([]facts.JobRegistrationFact(nil), store.JobRegistrations...),
		Links:            append([]facts.LinkFact(nil), store.Links...),
		Diagnostics:      append([]facts.DiagnosticFact(nil), store.Diagnostics...),
	}
	// 各类事实按 ID 字典序排序，保证相同事实集合产生稳定输出，便于 golden 与 diff。
	sort.Slice(doc.Symbols, func(i, j int) bool {
		return doc.Symbols[i].ID < doc.Symbols[j].ID
	})
	sort.Slice(doc.Annotations, func(i, j int) bool {
		return doc.Annotations[i].ID < doc.Annotations[j].ID
	})
	sort.Slice(doc.RouteGroups, func(i, j int) bool {
		return doc.RouteGroups[i].ID < doc.RouteGroups[j].ID
	})
	sort.Slice(doc.Routes, func(i, j int) bool {
		return doc.Routes[i].ID < doc.Routes[j].ID
	})
	sort.Slice(doc.Middleware, func(i, j int) bool {
		return doc.Middleware[i].ID < doc.Middleware[j].ID
	})
	sort.Slice(doc.References, func(i, j int) bool {
		return doc.References[i].ID < doc.References[j].ID
	})
	sort.Slice(doc.Modules, func(i, j int) bool {
		return doc.Modules[i].ID < doc.Modules[j].ID
	})
	sort.Slice(doc.IMEvents, func(i, j int) bool {
		return doc.IMEvents[i].ID < doc.IMEvents[j].ID
	})
	sort.Slice(doc.GrpcOperations, func(i, j int) bool {
		return doc.GrpcOperations[i].ID < doc.GrpcOperations[j].ID
	})
	sort.Slice(doc.GrpcCalls, func(i, j int) bool {
		return doc.GrpcCalls[i].ID < doc.GrpcCalls[j].ID
	})
	sort.Slice(doc.GrpcProviders, func(i, j int) bool {
		return doc.GrpcProviders[i].ID < doc.GrpcProviders[j].ID
	})
	sort.Slice(doc.DubboProviders, func(i, j int) bool {
		return doc.DubboProviders[i].ID < doc.DubboProviders[j].ID
	})
	sort.Slice(doc.JobRegistrations, func(i, j int) bool {
		return doc.JobRegistrations[i].ID < doc.JobRegistrations[j].ID
	})
	sort.Slice(doc.Links, func(i, j int) bool {
		return doc.Links[i].ID < doc.Links[j].ID
	})
	sort.Slice(doc.Diagnostics, func(i, j int) bool {
		return doc.Diagnostics[i].ID < doc.Diagnostics[j].ID
	})
	ensureNonNilSlices(&doc)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// ensureNonNilSlices 把所有事实切片从 nil 归一化为空切片，
// 使 JSON 输出为空数组而非 null，简化消费方解析。
func ensureNonNilSlices(doc *Document) {
	if doc.Symbols == nil {
		doc.Symbols = []facts.SymbolFact{}
	}
	if doc.Annotations == nil {
		doc.Annotations = []facts.AnnotationFact{}
	}
	if doc.RouteGroups == nil {
		doc.RouteGroups = []facts.RouteGroupFact{}
	}
	if doc.Routes == nil {
		doc.Routes = []facts.RouteRegistrationFact{}
	}
	if doc.Middleware == nil {
		doc.Middleware = []facts.MiddlewareBindingFact{}
	}
	if doc.References == nil {
		doc.References = []facts.ReferenceFact{}
	}
	if doc.Modules == nil {
		doc.Modules = []facts.ModuleDependencyFact{}
	}
	if doc.IMEvents == nil {
		doc.IMEvents = []facts.IMEventFact{}
	}
	if doc.GrpcOperations == nil {
		doc.GrpcOperations = []facts.GrpcOperationFact{}
	}
	if doc.GrpcCalls == nil {
		doc.GrpcCalls = []facts.GrpcCallFact{}
	}
	if doc.GrpcProviders == nil {
		doc.GrpcProviders = []facts.GrpcProviderFact{}
	}
	if doc.DubboProviders == nil {
		doc.DubboProviders = []facts.DubboProviderFact{}
	}
	if doc.JobRegistrations == nil {
		doc.JobRegistrations = []facts.JobRegistrationFact{}
	}
	if doc.Links == nil {
		doc.Links = []facts.LinkFact{}
	}
	if doc.Diagnostics == nil {
		doc.Diagnostics = []facts.DiagnosticFact{}
	}
}
