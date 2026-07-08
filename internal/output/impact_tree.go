// impact_tree.go 实现内部 impact 树到对外 JSON 的稳定投影：按来源聚合 change root、
// 去重端点与 IM 事件、稳定排序子树，并构建顶层 summary。
//
// 该文件是 internal/output 包的主入口，承载以下职责：
//   - 把 impact.TreeResult 投影为 ImpactDocument。
//   - 按变更来源（普通文件来源 fileSources / 模块来源 moduleSources）聚合传播树。
//   - 去重端点（endpoint）与已解析的 IM 事件。
//   - 对 symbols、endpoints、IM 事件、来源文件施加稳定排序，保证相同输入产生字节级一致的输出。
//   - 保留人工 review 所需的 raw evidence 与原始 diff。

// Package output 把 internal/impact 产出的传播树投影为稳定的对外 JSON 文档。
//
// 该包承担 ARCHITECTURE.md 第 5.15、11 节定义的输出契约：
//   - 把内部 impact 树投影为稳定 JSON，保留从 change root 到 endpoint / IM event 的完整 children 递归链路。
//   - 按变更来源聚合 change root：普通文件逻辑变更进入 fileSources，go.mod 语义模块变更进入 moduleSources。
//   - 去重端点（endpoint）摘要与已解析的 IM 事件字符串；动态 IM 事件以 im_event_unresolved 终端保留在树中但不计入摘要。
//   - 对文档各层级施加确定性排序，降低 golden 样本与下游消费者的抖动。
//   - 暴露 facts / impact 的 JSON Schema（见 contract.go、schema.go）。
//
// 该包不生产业务事实，也不补业务关系；只负责“如何稳定表达”已计算出的影响范围。
package output

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/impact"
)

// ImpactDocument 是 impact 命令的顶层 JSON 文档。
// 顶层包含全局去重后的 summary、按文件聚合的 fileSources，以及可选的 moduleSources
// （仅当 go.mod diff 成功形成 module change 时输出）。
type ImpactDocument struct {
	// Summary 是全局去重后的端点与 IM 事件摘要，面向默认消费场景。
	Summary ImpactSummary `json:"summary"`
	// FileSources 按变更后文件聚合普通源码 diff 对应的传播树与摘要。
	FileSources []FileSourceImpact `json:"fileSources"`
	// ModuleSources 仅在形成 go.mod 模块变更时输出，避免 go.mod 噪音混入 fileSources。
	ModuleSources []ModuleSourceImpact `json:"moduleSources,omitempty"`
	// EndpointSourcesSummary 是 endpoint -> 变更来源的轻量反查摘要，完整证据仍保留在 sources 的 symbols 树中。
	EndpointSourcesSummary []EndpointSourceSummary `json:"endpointSourcesSummary"`
}

// FileSourceImpact 描述单个变更来源（通常是源码文件）对应的影响范围。
// 它保留原始 diff、按 changed root 组织的传播树，以及该来源去重后的端点和 IM 事件摘要。
type FileSourceImpact struct {
	// SourceFile 是变更后文件相对项目根目录的 slash 路径。
	SourceFile string `json:"sourceFile"`
	// Diff 保留该文件的原始 unified diff 文本，供人工 review。
	Diff string `json:"diff,omitempty"`
	// Symbols 按 changed root ID（或非符号 root 的 "__non_symbol__"）组织的递归传播树。
	Symbols map[string]ImpactNode `json:"symbols"`
	// ImpactedEndpoints 该来源下全局去重后的端点摘要。
	ImpactedEndpoints []EndpointSummary `json:"impactedEndpoints"`
	// ImpactedIMEvents 该来源下已解析且去重的 IM 事件字符串。
	ImpactedIMEvents []string `json:"impactedIMEvents"`
}

// ImpactNode 是 impact 传播树的 JSON 节点，由内部 impact.Node 投影而来。
// 节点保留 relation、raw、confidence、level、cycle 等 review 证据，但不输出 span。
type ImpactNode struct {
	// ID 是节点对应的稳定 symbol/fact 标识，如 "func:<package>::<name>"。
	ID string `json:"id"`
	// Kind 是节点领域类型，如 func、type、route、annotation、endpoint、im_event 等。
	Kind string `json:"kind"`
	// Name 是便于人工阅读的声明名称，缺省时省略。
	Name string `json:"name,omitempty"`
	// File 是节点所在的项目相对 slash 路径，缺省时省略。
	File string `json:"file,omitempty"`
	// Package 是节点所属的 Go package path，缺省时省略。
	Package string `json:"package,omitempty"`
	// Relation 描述该节点相对父节点的影响关系，例如 call、type、resolved_endpoint。
	Relation string `json:"relation,omitempty"`
	// Raw 保留原始 AST 表达式文本，供人工 review 追溯。
	Raw string `json:"raw,omitempty"`
	// Confidence 表示该节点的静态证据强度（high/medium/low），不是概率分数。
	Confidence facts.Confidence `json:"confidence,omitempty"`
	// Level 是节点在传播树中的深度，根节点为 0。
	Level int `json:"level"`
	// Cycle 标记该节点因当前 DFS 路径已存在相同 symbol 而形成的环边。
	Cycle bool `json:"cycle,omitempty"`
	// Children 是递归传播子节点，消费者无需再与顶层图做 join。
	Children []ImpactNode `json:"children"`
	// Method 是 endpoint 终端的 HTTP method。
	Method string `json:"method,omitempty"`
	// Path 是 endpoint 终端的 HTTP path。
	Path string `json:"path,omitempty"`
}

// EndpointSummary 是去重后的受影响 HTTP 端点摘要。
type EndpointSummary struct {
	// Method 是 HTTP method，例如 GET、POST。
	Method string `json:"method"`
	// Path 是完整 HTTP path。
	Path string `json:"path"`
}

// EndpointSourceSummary 描述一个 endpoint 被哪些变更来源影响。
type EndpointSourceSummary struct {
	// Method 是 HTTP method，例如 GET、POST。
	Method string `json:"method"`
	// Path 是完整 HTTP path。
	Path string `json:"path"`
	// Sources 是影响该 endpoint 的变更来源摘要。
	Sources []EndpointImpactSource `json:"sources"`
}

// EndpointImpactSource 描述单个变更来源到 endpoint 的轻量证据摘要。
type EndpointImpactSource struct {
	// SourceType 是来源类型：file 表示普通源码 diff，module 表示 go.mod module 语义变更。
	SourceType string `json:"sourceType"`
	// SourceFile 是触发传播的项目相对文件路径。
	SourceFile string `json:"sourceFile,omitempty"`
	// ModulePath 是 module 来源的变更模块 path。
	ModulePath string `json:"modulePath,omitempty"`
	// ChangeType 是 module 来源的变更类型。
	ChangeType facts.ModuleChangeKind `json:"changeType,omitempty"`
	// VersionBefore 是 module 变更前版本。
	VersionBefore string `json:"versionBefore,omitempty"`
	// VersionAfter 是 module 变更后版本。
	VersionAfter string `json:"versionAfter,omitempty"`
	// RootSymbols 是该来源中能到达 endpoint 的 changed roots。
	RootSymbols []EndpointRootSymbolSummary `json:"rootSymbols"`
	// Chains 是每个 root 到 endpoint 的最短人读链路摘要。
	Chains [][]string `json:"chains"`
	// Confidence 是所选链路中的最弱证据强度。
	Confidence facts.Confidence `json:"confidence"`
}

// EndpointRootSymbolSummary 是 endpoint source 中 changed root 的轻量信息。
type EndpointRootSymbolSummary struct {
	// ID 是 root 的稳定 symbol/fact ID。
	ID string `json:"id"`
	// Kind 是 root 节点类型。
	Kind string `json:"kind"`
	// Name 是 root 的人读名称。
	Name string `json:"name,omitempty"`
	// File 是 root 所在的项目相对路径。
	File string `json:"file,omitempty"`
}

// ImpactSummary 是全局去重后的轻量结果，面向默认消费场景。
// impactedEndpointCount 与 impactedIMCount 分别是 fileSources / moduleSources
// 对应摘要的并集大小。
type ImpactSummary struct {
	// ImpactedEndpointCount 是全局去重后的受影响端点数量。
	ImpactedEndpointCount int `json:"impactedEndpointCount"`
	// ImpactedEndpoints 是全局去重后的受影响端点列表。
	ImpactedEndpoints []EndpointSummary `json:"impactedEndpoints"`
	// ImpactedIMCount 是全局去重后已解析的 IM 事件数量。
	ImpactedIMCount int `json:"impactedIMCount"`
	// ImpactedIMEvents 是全局去重后已解析的 IM 事件字符串列表。
	ImpactedIMEvents []string `json:"impactedIMEvents"`
}

// ModuleSourceImpact 描述单个变更模块（来自 go.mod diff）及其在本仓的传播入口。
// SourceFiles 是实际命中本仓 usage 的文件入口，不重复 go.mod diff 本身。
type ModuleSourceImpact struct {
	// ModulePath 是发生变更的 Go module path。
	ModulePath string `json:"modulePath"`
	// ChangeType 是模块变更类型，例如 upgraded / downgraded / replaced / added / removed。
	ChangeType facts.ModuleChangeKind `json:"changeType"`
	// VersionBefore 是变更前版本，省略时表示新增或不可用。
	VersionBefore string `json:"versionBefore,omitempty"`
	// VersionAfter 是变更后版本，省略时表示删除或不可用。
	VersionAfter string `json:"versionAfter,omitempty"`
	// ReplacementBefore 描述 replace 变更前的替换目标，仅在 replace 变化时输出。
	ReplacementBefore *ModuleReplacement `json:"replacementBefore,omitempty"`
	// ReplacementAfter 描述 replace 变更后的替换目标，仅在 replace 变化时输出。
	ReplacementAfter *ModuleReplacement `json:"replacementAfter,omitempty"`
	// Basis 是该模块在本仓的传播入口依据，例如 matched_import_usage / matched_file_usage / module_unreferenced。
	Basis string `json:"basis"`
	// SourceFiles 是实际引用该模块的本仓文件入口及其传播树与摘要，按文件稳定排序。
	SourceFiles []FileSourceImpact `json:"sourceFiles,omitempty"`
}

// ModuleReplacement 描述 replace 指令的替换目标 path 与可选 version。
type ModuleReplacement struct {
	// Path 是 replace 目标的 module path 或本地相对路径。
	Path string `json:"path"`
	// Version 是 replace 目标的版本，省略时表示本地路径替换。
	Version string `json:"version,omitempty"`
}

// ImpactDocumentOptions 是 BuildImpactDocument 的可选输入。
// 携带模块变更与模块 usage 的事实，用于把 go.mod diff 投影为 moduleSources。
type ImpactDocumentOptions struct {
	// ModuleChanges 是已识别的 go.mod 模块变更事实。
	ModuleChanges []facts.ModuleChangeFact
	// ModuleUsages 是本仓对变更模块的 usage 事实，用于确定传播入口与 basis。
	ModuleUsages []facts.ModuleUsageFact
	// SuppressGoModFileSource 控制是否抑制 go.mod 作为 fileSource 出现。
	// 当存在模块变更或显式抑制时，go.mod 不再以低置信度 __non_symbol__ root 出现在 fileSources。
	SuppressGoModFileSource bool
}

// fileSourceBuilder 是构造 FileSourceImpact 的中间结构。
// source 是对外结构，endpoints 与 imEvents 用 map 完成去重后再投影为有序切片。
type fileSourceBuilder struct {
	source    FileSourceImpact
	endpoints map[string]EndpointSummary
	imEvents  map[string]struct{}
}

// moduleSourceBuilder 是构造 ModuleSourceImpact 的中间结构。
// files 按本仓 usage 文件聚合，最终复用 finalizeFileSources 完成稳定排序。
type moduleSourceBuilder struct {
	source ModuleSourceImpact
	files  map[string]*fileSourceBuilder
}

// BuildImpactDocument 把 impact 树结果投影为稳定的对外 ImpactDocument。
//
// 主要步骤：
//  1. 按 source file 聚合原始 diff（go.mod 在已形成模块变更或显式抑制时跳过，避免噪音）。
//  2. 遍历每个 RootImpact，根据 Change.Source 决定归属到普通 fileSources 还是 moduleSources。
//  3. 把 impact.Node 投影为 ImpactNode，对同 key 的 root 合并子树。
//  4. 把每个 root 的 endpoints / IM events 收集到来源 builder 与全局去重 map。
//  5. 通过 finalize* / normalizeImpactDocument 完成稳定排序与 nil 切片归一化。
//
// 相同的项目快照与 diff 因此得到确定性输出，满足 golden 样本与下游消费者要求。
func BuildImpactDocument(fileChanges []diff.FileChange, result impact.TreeResult, opts ImpactDocumentOptions) ImpactDocument {
	// files 聚合普通源码来源；moduleSources 聚合 go.mod 模块来源；globalEndpoints / globalIMEvents 维护全局去重。
	files := map[string]*fileSourceBuilder{}
	moduleSources := buildModuleSourceBuilders(opts.ModuleChanges)
	moduleUsages := indexModuleUsages(opts.ModuleUsages)
	globalEndpoints := map[string]EndpointSummary{}
	globalIMEvents := map[string]struct{}{}

	for _, change := range fileChanges {
		file := changedFile(change)
		// 存在模块变更或显式抑制时，go.mod 不再作为 fileSource，避免低置信度噪音。
		if file == "go.mod" && (len(opts.ModuleChanges) > 0 || opts.SuppressGoModFileSource) {
			continue
		}
		builder := ensureFileSource(files, file)
		builder.source.Diff += change.Raw
	}

	for _, root := range result.Roots {
		// 非 go_mod_diff 来源的 go.mod root 在已形成模块变更时同样跳过。
		if filepath.ToSlash(root.Change.File) == "go.mod" &&
			root.Change.Source != "go_mod_diff" &&
			(len(opts.ModuleChanges) > 0 || opts.SuppressGoModFileSource) {
			continue
		}
		builder := sourceBuilderForRoot(files, moduleSources, moduleUsages, root)
		// file fallback 或缺少 symbol/target 的 root 统一归到 __non_symbol__，便于跨 root 合并。
		key := root.Root.ID
		if root.Root.Kind == "file" || root.Change.SymbolID == "" && root.Change.TargetID == "" {
			key = "__non_symbol__"
		}
		node := projectImpactNode(root.Root)
		if existing, ok := builder.source.Symbols[key]; ok {
			// 同一来源下同 key 的多个 root 合并 children，避免重复顶层节点。
			node = mergeImpactNodes(existing, node)
		}
		builder.source.Symbols[key] = node
		for _, endpoint := range root.Endpoints {
			// 跳过不完整的 endpoint，确保摘要只包含 method/path 齐全的端点。
			if endpoint.Method == "" || endpoint.Path == "" {
				continue
			}
			summary := EndpointSummary{Method: endpoint.Method, Path: endpoint.Path}
			endpointID := endpointKey(summary)
			builder.endpoints[endpointID] = summary
			globalEndpoints[endpointID] = summary
		}
		for _, event := range root.IMEvents {
			// 空事件不计入任何摘要；动态事件在树中以 im_event_unresolved 终端存在但 Event 为空。
			if event.Event == "" {
				continue
			}
			builder.imEvents[event.Event] = struct{}{}
			globalIMEvents[event.Event] = struct{}{}
		}
	}

	doc := normalizeImpactDocument(ImpactDocument{
		Summary:       buildImpactSummary(globalEndpoints, globalIMEvents),
		FileSources:   finalizeFileSources(files),
		ModuleSources: finalizeModuleSources(moduleSources),
	})
	doc.EndpointSourcesSummary = buildEndpointSourcesSummary(doc)
	return normalizeImpactDocument(doc)
}

// changedFile 返回变更后文件相对项目根目录的 slash 路径。
// 优先使用 NewPath（变更后路径），删除文件回退到 OldPath。
func changedFile(change diff.FileChange) string {
	file := change.NewPath
	if file == "" {
		file = change.OldPath
	}
	return filepath.ToSlash(file)
}

// sourceBuilderForRoot 决定某个 RootImpact 应聚合到哪个来源 builder。
//
// 如果 Change.Source 是 "go_mod_diff"，且其 SourceFactID 能在 moduleUsages 中找到对应 usage，
// 则归属到该模块的 moduleSourceBuilder，并按 usage basis 强化该模块的对外 basis。
// 否则按变更文件归属到普通 fileSources。
func sourceBuilderForRoot(
	files map[string]*fileSourceBuilder,
	moduleSources map[string]*moduleSourceBuilder,
	moduleUsages map[string]facts.ModuleUsageFact,
	root impact.RootImpact,
) *fileSourceBuilder {
	if root.Change.Source == "go_mod_diff" {
		if usage, ok := moduleUsages[root.Change.SourceFactID]; ok {
			if module := moduleSources[usage.ModulePath]; module != nil {
				// 同一模块可能被多个 usage 入口命中，basis 取最强证据（precise > file > unreferenced）。
				module.source.Basis = strongerModuleBasis(module.source.Basis, moduleBasis(usage.Basis))
				return ensureFileSource(module.files, root.Change.File)
			}
		}
	}
	return ensureFileSource(files, root.Change.File)
}

// ensureFileSource 取或创建某文件对应的 fileSourceBuilder，路径统一转为 slash。
// 返回的 builder 持有去重 map 与对外结构，后续不断累加 endpoint / IM event / 子树。
func ensureFileSource(files map[string]*fileSourceBuilder, file string) *fileSourceBuilder {
	file = filepath.ToSlash(file)
	if existing := files[file]; existing != nil {
		return existing
	}
	builder := &fileSourceBuilder{
		source: FileSourceImpact{
			SourceFile:        file,
			Symbols:           map[string]ImpactNode{},
			ImpactedEndpoints: []EndpointSummary{},
			ImpactedIMEvents:  []string{},
		},
		endpoints: map[string]EndpointSummary{},
		imEvents:  map[string]struct{}{},
	}
	files[file] = builder
	return builder
}

// finalizeFileSources 把 fileSourceBuilder map 投影为按 SourceFile 稳定排序的切片。
// 过程中对每个 builder 的 symbols、endpoints、IM 事件完成归一化与稳定排序。
func finalizeFileSources(files map[string]*fileSourceBuilder) []FileSourceImpact {
	out := make([]FileSourceImpact, 0, len(files))
	for _, builder := range files {
		for key, node := range builder.source.Symbols {
			builder.source.Symbols[key] = normalizeImpactNode(node)
		}
		for _, endpoint := range builder.endpoints {
			builder.source.ImpactedEndpoints = append(builder.source.ImpactedEndpoints, endpoint)
		}
		sortEndpointSummaries(builder.source.ImpactedEndpoints)
		builder.source.ImpactedIMEvents = sortedStrings(builder.imEvents)
		out = append(out, builder.source)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].SourceFile < out[j].SourceFile
	})
	return out
}

// buildModuleSourceBuilders 为每个 ModuleChangeFact 构造一个 moduleSourceBuilder。
// 初始 Basis 设为 module_unreferenced，后续若被 usage root 命中会被强化。
func buildModuleSourceBuilders(changes []facts.ModuleChangeFact) map[string]*moduleSourceBuilder {
	out := make(map[string]*moduleSourceBuilder, len(changes))
	for _, change := range changes {
		out[change.Path] = &moduleSourceBuilder{
			source: ModuleSourceImpact{
				ModulePath:        change.Path,
				ChangeType:        change.Kind,
				VersionBefore:     change.OldVersion,
				VersionAfter:      change.NewVersion,
				ReplacementBefore: moduleReplacement(change.OldReplacePath, change.OldReplaceVersion),
				ReplacementAfter:  moduleReplacement(change.NewReplacePath, change.NewReplaceVersion),
				Basis:             moduleBasis(facts.ModuleUsageUnreferenced),
				SourceFiles:       []FileSourceImpact{},
			},
			files: map[string]*fileSourceBuilder{},
		}
	}
	return out
}

// finalizeModuleSources 把 moduleSourceBuilder map 投影为按 ModulePath 稳定排序的切片。
// 每个模块的 SourceFiles 复用 finalizeFileSources 完成内部文件入口的稳定排序。
func finalizeModuleSources(modules map[string]*moduleSourceBuilder) []ModuleSourceImpact {
	out := make([]ModuleSourceImpact, 0, len(modules))
	for _, module := range modules {
		module.source.SourceFiles = finalizeFileSources(module.files)
		out = append(out, module.source)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ModulePath < out[j].ModulePath
	})
	return out
}

// moduleReplacement 把 replace path/version 投影为 ModuleReplacement 指针。
// path 与 version 同时为空时返回 nil，使 JSON omitempty 生效。
func moduleReplacement(path, version string) *ModuleReplacement {
	if path == "" && version == "" {
		return nil
	}
	return &ModuleReplacement{Path: path, Version: version}
}

// indexModuleUsages 按 usage ID 建立索引，便于 go_mod_diff root 反查 usage 与归属模块。
func indexModuleUsages(usages []facts.ModuleUsageFact) map[string]facts.ModuleUsageFact {
	out := make(map[string]facts.ModuleUsageFact, len(usages))
	for _, usage := range usages {
		out[usage.ID] = usage
	}
	return out
}

// moduleBasis 把内部 ModuleUsageBasis 枚举翻译为对外 basis 字符串。
// matched_import_usage（precise）/ matched_file_usage（file fallback）/ module_unreferenced。
func moduleBasis(basis facts.ModuleUsageBasis) string {
	switch basis {
	case facts.ModuleUsagePrecise:
		return "matched_import_usage"
	case facts.ModuleUsageFileFallback:
		return "matched_file_usage"
	default:
		return "module_unreferenced"
	}
}

// strongerModuleBasis 取两个 basis 字符串中证据更强者。
// 排序：matched_import_usage > matched_file_usage > module_unreferenced > 未知。
func strongerModuleBasis(left, right string) string {
	rank := func(basis string) int {
		switch basis {
		case "matched_import_usage":
			return 3
		case "matched_file_usage":
			return 2
		case "module_unreferenced":
			return 1
		default:
			return 0
		}
	}
	if rank(right) > rank(left) {
		return right
	}
	return left
}

// projectImpactNode 把内部 impact.Node 递归投影为对外 ImpactNode。
// 过程中统一 slash 文件路径，并经 normalizeImpactNode 完成 children 合并与 nil 归一化。
// span 等内部调试字段在此处被丢弃，符合 impact 不输出 span 的契约。
func projectImpactNode(node impact.Node) ImpactNode {
	projected := ImpactNode{
		ID:         node.ID,
		Kind:       node.Kind,
		Name:       node.Name,
		File:       filepath.ToSlash(node.File),
		Package:    node.Package,
		Relation:   node.Relation,
		Raw:        node.Raw,
		Confidence: node.Confidence,
		Level:      node.Level,
		Cycle:      node.Cycle,
		Method:     node.Method,
		Path:       node.Path,
		Children:   make([]ImpactNode, 0, len(node.Children)),
	}
	for _, child := range node.Children {
		projected.Children = append(projected.Children, projectImpactNode(child))
	}
	return normalizeImpactNode(projected)
}

// mergeImpactNodes 合并两个同 key ImpactNode：拼接 children、统一重排，并保留 cycle 标记。
// 用于同一来源下出现多个 root 落到同一 changed root key 的场景。
func mergeImpactNodes(left, right ImpactNode) ImpactNode {
	left.Children = append(left.Children, right.Children...)
	left.Children = mergeImpactNodeChildren(left.Children)
	left.Cycle = left.Cycle || right.Cycle
	return left
}

// mergeImpactNodeChildren 按 (ID, Relation) 合并重复 children 并稳定排序。
// 用 \x00 作为分隔符避免不同字段值意外拼接出相同 key。
func mergeImpactNodeChildren(children []ImpactNode) []ImpactNode {
	merged := make([]ImpactNode, 0, len(children))
	indexes := map[string]int{}
	for _, child := range children {
		key := child.ID + "\x00" + child.Relation
		if index, ok := indexes[key]; ok {
			merged[index] = mergeImpactNodes(merged[index], child)
			continue
		}
		indexes[key] = len(merged)
		merged = append(merged, child)
	}
	sortImpactNodes(merged)
	return merged
}

// normalizeImpactNode 归一化单个节点：合并 children 并把 nil 切片转为空切片，
// 使 JSON 输出始终为 "children": []，保证 golden 与消费者确定性。
func normalizeImpactNode(node ImpactNode) ImpactNode {
	node.Children = mergeImpactNodeChildren(node.Children)
	if node.Children == nil {
		node.Children = []ImpactNode{}
	}
	return node
}

// sortImpactNodes 对同级节点按多级排序键稳定排序，并递归归一化子树。
// 排序优先级：Level -> Kind -> File -> Package -> ID -> Relation。
// 多级排序避免同 Level/Kind 下出现 tie-break 不确定，是确定性输出的关键。
func sortImpactNodes(nodes []ImpactNode) {
	for i := range nodes {
		nodes[i] = normalizeImpactNode(nodes[i])
	}
	sort.Slice(nodes, func(i, j int) bool {
		left, right := nodes[i], nodes[j]
		if left.Level != right.Level {
			return left.Level < right.Level
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Package != right.Package {
			return left.Package < right.Package
		}
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		return left.Relation < right.Relation
	})
}

// buildImpactSummary 根据全局去重的 endpoints / IM events 构造顶层 ImpactSummary。
// 切片经稳定排序，count 字段直接取自切片长度，保证 count 与列表一致。
func buildImpactSummary(endpoints map[string]EndpointSummary, imEvents map[string]struct{}) ImpactSummary {
	out := ImpactSummary{
		ImpactedEndpoints: make([]EndpointSummary, 0, len(endpoints)),
		ImpactedIMEvents:  sortedStrings(imEvents),
	}
	for _, endpoint := range endpoints {
		out.ImpactedEndpoints = append(out.ImpactedEndpoints, endpoint)
	}
	sortEndpointSummaries(out.ImpactedEndpoints)
	out.ImpactedEndpointCount = len(out.ImpactedEndpoints)
	out.ImpactedIMCount = len(out.ImpactedIMEvents)
	return out
}

// buildEndpointSourcesSummary 从已归一化的来源树构造 endpoint -> sources 轻量反查摘要。
func buildEndpointSourcesSummary(doc ImpactDocument) []EndpointSourceSummary {
	builders := map[string]*endpointSourceSummaryBuilder{}
	for _, source := range doc.FileSources {
		addEndpointSourceFile(builders, source, endpointSourceMetadata{
			sourceType: "file",
			sourceFile: source.SourceFile,
		})
	}
	for _, module := range doc.ModuleSources {
		for _, source := range module.SourceFiles {
			addEndpointSourceFile(builders, source, endpointSourceMetadata{
				sourceType:    "module",
				sourceFile:    source.SourceFile,
				modulePath:    module.ModulePath,
				changeType:    module.ChangeType,
				versionBefore: module.VersionBefore,
				versionAfter:  module.VersionAfter,
			})
		}
	}
	out := make([]EndpointSourceSummary, 0, len(builders))
	for _, builder := range builders {
		for _, source := range builder.sources {
			normalizeEndpointImpactSource(&source)
			builder.summary.Sources = append(builder.summary.Sources, source)
		}
		sortEndpointImpactSources(builder.summary.Sources)
		out = append(out, builder.summary)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Method != out[j].Method {
			return out[i].Method < out[j].Method
		}
		return out[i].Path < out[j].Path
	})
	return out
}

type endpointSourceSummaryBuilder struct {
	summary EndpointSourceSummary
	sources map[string]EndpointImpactSource
}

type endpointSourceMetadata struct {
	sourceType    string
	sourceFile    string
	modulePath    string
	changeType    facts.ModuleChangeKind
	versionBefore string
	versionAfter  string
}

func addEndpointSourceFile(builders map[string]*endpointSourceSummaryBuilder, source FileSourceImpact, metadata endpointSourceMetadata) {
	for _, endpoint := range source.ImpactedEndpoints {
		if endpoint.Method == "" || endpoint.Path == "" {
			continue
		}
		endpointID := endpointKey(endpoint)
		builder := builders[endpointID]
		if builder == nil {
			builder = &endpointSourceSummaryBuilder{
				summary: EndpointSourceSummary{
					Method:  endpoint.Method,
					Path:    endpoint.Path,
					Sources: []EndpointImpactSource{},
				},
				sources: map[string]EndpointImpactSource{},
			}
			builders[endpointID] = builder
		}
		sourceKey := endpointImpactSourceKey(metadata)
		impactSource := builder.sources[sourceKey]
		if impactSource.SourceType == "" {
			impactSource = EndpointImpactSource{
				SourceType:    metadata.sourceType,
				SourceFile:    metadata.sourceFile,
				ModulePath:    metadata.modulePath,
				ChangeType:    metadata.changeType,
				VersionBefore: metadata.versionBefore,
				VersionAfter:  metadata.versionAfter,
				RootSymbols:   []EndpointRootSymbolSummary{},
				Chains:        [][]string{},
				Confidence:    facts.ConfidenceLow,
			}
		}
		foundChain := false
		for _, root := range source.Symbols {
			path, ok := shortestEndpointPath(root, endpoint)
			if !ok {
				continue
			}
			foundChain = true
			impactSource.RootSymbols = append(impactSource.RootSymbols, rootSymbolSummary(root))
			impactSource.Chains = append(impactSource.Chains, chainLabels(path))
			pathConfidence := weakestConfidence(path)
			if impactSource.Confidence == "" || len(impactSource.Chains) == 1 {
				impactSource.Confidence = pathConfidence
			} else {
				impactSource.Confidence = weakerConfidence(impactSource.Confidence, pathConfidence)
			}
		}
		if !foundChain && impactSource.Confidence == "" {
			impactSource.Confidence = facts.ConfidenceLow
		}
		builder.sources[sourceKey] = impactSource
	}
}

func endpointImpactSourceKey(metadata endpointSourceMetadata) string {
	return strings.Join([]string{
		metadata.sourceType,
		metadata.sourceFile,
		metadata.modulePath,
		string(metadata.changeType),
		metadata.versionBefore,
		metadata.versionAfter,
	}, "\x00")
}

func shortestEndpointPath(root ImpactNode, endpoint EndpointSummary) ([]ImpactNode, bool) {
	var best []ImpactNode
	var walk func(ImpactNode, []ImpactNode)
	walk = func(node ImpactNode, path []ImpactNode) {
		current := append(path, node)
		if node.Kind == "endpoint" && node.Method == endpoint.Method && node.Path == endpoint.Path {
			if best == nil || len(current) < len(best) {
				best = append([]ImpactNode(nil), current...)
			}
			return
		}
		if best != nil && len(current) >= len(best) {
			return
		}
		for _, child := range node.Children {
			walk(child, current)
		}
	}
	walk(root, nil)
	return best, best != nil
}

func rootSymbolSummary(root ImpactNode) EndpointRootSymbolSummary {
	return EndpointRootSymbolSummary{
		ID:   root.ID,
		Kind: root.Kind,
		Name: root.Name,
		File: root.File,
	}
}

func chainLabels(path []ImpactNode) []string {
	out := make([]string, 0, len(path))
	for _, node := range path {
		out = append(out, impactNodeLabel(node))
	}
	return out
}

func impactNodeLabel(node ImpactNode) string {
	if node.Kind == "endpoint" && node.Method != "" && node.Path != "" {
		return node.Method + " " + node.Path
	}
	if node.Name != "" {
		return strings.TrimSpace(node.Kind + " " + node.Name)
	}
	if node.ID != "" {
		return strings.TrimSpace(node.Kind + " " + node.ID)
	}
	return node.Kind
}

func weakestConfidence(path []ImpactNode) facts.Confidence {
	out := facts.ConfidenceHigh
	for _, node := range path {
		out = weakerConfidence(out, node.Confidence)
	}
	return out
}

func weakerConfidence(left, right facts.Confidence) facts.Confidence {
	if confidenceRank(right) < confidenceRank(left) {
		return right
	}
	return left
}

func confidenceRank(confidence facts.Confidence) int {
	switch confidence {
	case facts.ConfidenceHigh:
		return 3
	case facts.ConfidenceMedium:
		return 2
	case facts.ConfidenceLow:
		return 1
	default:
		return 0
	}
}

func normalizeEndpointImpactSource(source *EndpointImpactSource) {
	sort.Slice(source.RootSymbols, func(i, j int) bool {
		return source.RootSymbols[i].ID < source.RootSymbols[j].ID
	})
	source.RootSymbols = uniqueEndpointRootSymbols(source.RootSymbols)
	sort.Slice(source.Chains, func(i, j int) bool {
		return strings.Join(source.Chains[i], "\x00") < strings.Join(source.Chains[j], "\x00")
	})
	source.Chains = uniqueChains(source.Chains)
	if source.RootSymbols == nil {
		source.RootSymbols = []EndpointRootSymbolSummary{}
	}
	if source.Chains == nil {
		source.Chains = [][]string{}
	}
}

func uniqueEndpointRootSymbols(values []EndpointRootSymbolSummary) []EndpointRootSymbolSummary {
	if len(values) < 2 {
		return values
	}
	out := values[:0]
	var last string
	for i, value := range values {
		if i > 0 && value.ID == last {
			continue
		}
		out = append(out, value)
		last = value.ID
	}
	return out
}

func uniqueChains(values [][]string) [][]string {
	if len(values) < 2 {
		return values
	}
	out := values[:0]
	var last string
	for i, value := range values {
		key := strings.Join(value, "\x00")
		if i > 0 && key == last {
			continue
		}
		out = append(out, value)
		last = key
	}
	return out
}

func sortEndpointImpactSources(sources []EndpointImpactSource) {
	sort.Slice(sources, func(i, j int) bool {
		left, right := sources[i], sources[j]
		if left.SourceType != right.SourceType {
			return left.SourceType < right.SourceType
		}
		if left.SourceFile != right.SourceFile {
			return left.SourceFile < right.SourceFile
		}
		if left.ModulePath != right.ModulePath {
			return left.ModulePath < right.ModulePath
		}
		return left.VersionAfter < right.VersionAfter
	})
}

// normalizeImpactDocument 对整篇 ImpactDocument 做最终归一化与稳定排序。
// 包括 nil 切片转空切片、endpoints / IM 事件排序去重、fileSources / moduleSources
// 及其嵌套 SourceFiles 的稳定排序。这是 RenderImpactTreeJSON 之前的最后一道保障。
func normalizeImpactDocument(doc ImpactDocument) ImpactDocument {
	if doc.Summary.ImpactedEndpoints == nil {
		doc.Summary.ImpactedEndpoints = []EndpointSummary{}
	}
	sortEndpointSummaries(doc.Summary.ImpactedEndpoints)
	doc.Summary.ImpactedEndpointCount = len(doc.Summary.ImpactedEndpoints)
	if doc.Summary.ImpactedIMEvents == nil {
		doc.Summary.ImpactedIMEvents = []string{}
	}
	sort.Strings(doc.Summary.ImpactedIMEvents)
	doc.Summary.ImpactedIMEvents = uniqueStrings(doc.Summary.ImpactedIMEvents)
	doc.Summary.ImpactedIMCount = len(doc.Summary.ImpactedIMEvents)
	if doc.FileSources == nil {
		doc.FileSources = []FileSourceImpact{}
	}
	for i := range doc.FileSources {
		doc.FileSources[i] = normalizeFileSource(doc.FileSources[i])
	}
	for i := range doc.ModuleSources {
		for j := range doc.ModuleSources[i].SourceFiles {
			doc.ModuleSources[i].SourceFiles[j] = normalizeFileSource(doc.ModuleSources[i].SourceFiles[j])
		}
		sort.Slice(doc.ModuleSources[i].SourceFiles, func(left, right int) bool {
			return doc.ModuleSources[i].SourceFiles[left].SourceFile < doc.ModuleSources[i].SourceFiles[right].SourceFile
		})
	}
	sort.Slice(doc.FileSources, func(i, j int) bool {
		return doc.FileSources[i].SourceFile < doc.FileSources[j].SourceFile
	})
	sort.Slice(doc.ModuleSources, func(i, j int) bool {
		return doc.ModuleSources[i].ModulePath < doc.ModuleSources[j].ModulePath
	})
	if doc.EndpointSourcesSummary == nil {
		doc.EndpointSourcesSummary = []EndpointSourceSummary{}
	}
	for i := range doc.EndpointSourcesSummary {
		sortEndpointImpactSources(doc.EndpointSourcesSummary[i].Sources)
		if doc.EndpointSourcesSummary[i].Sources == nil {
			doc.EndpointSourcesSummary[i].Sources = []EndpointImpactSource{}
		}
		for j := range doc.EndpointSourcesSummary[i].Sources {
			normalizeEndpointImpactSource(&doc.EndpointSourcesSummary[i].Sources[j])
		}
	}
	sort.Slice(doc.EndpointSourcesSummary, func(i, j int) bool {
		if doc.EndpointSourcesSummary[i].Method != doc.EndpointSourcesSummary[j].Method {
			return doc.EndpointSourcesSummary[i].Method < doc.EndpointSourcesSummary[j].Method
		}
		return doc.EndpointSourcesSummary[i].Path < doc.EndpointSourcesSummary[j].Path
	})
	return doc
}

// normalizeFileSource 归一化单个来源：symbols 子树合并、nil 切片转空切片、
// endpoints / IM 事件排序去重。
func normalizeFileSource(source FileSourceImpact) FileSourceImpact {
	if source.Symbols == nil {
		source.Symbols = map[string]ImpactNode{}
	}
	for key, node := range source.Symbols {
		source.Symbols[key] = normalizeImpactNode(node)
	}
	if source.ImpactedEndpoints == nil {
		source.ImpactedEndpoints = []EndpointSummary{}
	}
	sortEndpointSummaries(source.ImpactedEndpoints)
	if source.ImpactedIMEvents == nil {
		source.ImpactedIMEvents = []string{}
	}
	sort.Strings(source.ImpactedIMEvents)
	source.ImpactedIMEvents = uniqueStrings(source.ImpactedIMEvents)
	return source
}

// RenderImpactTreeJSON 把 ImpactDocument 序列化为缩进 JSON，末尾追加换行。
// 序列化前再次走 normalizeImpactDocument，确保即便外部直接传入也得到稳定输出。
func RenderImpactTreeJSON(doc ImpactDocument) ([]byte, error) {
	normalized := normalizeImpactDocument(doc)
	out, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// endpointKey 构造端点去重 key。用 \x00 分隔避免 method/path 拼接歧义。
func endpointKey(endpoint EndpointSummary) string {
	return endpoint.Method + "\x00" + endpoint.Path
}

// sortEndpointSummaries 按 (Method, Path) 稳定排序端点摘要。
func sortEndpointSummaries(endpoints []EndpointSummary) {
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].Method != endpoints[j].Method {
			return endpoints[i].Method < endpoints[j].Method
		}
		return endpoints[i].Path < endpoints[j].Path
	})
}

// sortedStrings 把字符串集合转为字典序切片，用于 IM 事件去重后的稳定输出。
func sortedStrings(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// uniqueStrings 去除已排序切片中的相邻重复项。
// 调用方需先排序；少于 2 个元素时直接返回。
func uniqueStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
