// change.go 实现 diff 映射产生的传播根事实类型 ChangeFact 及其 kind、range 定义。
// 由 diff mapper、删除路由恢复与 gomod usage 等阶段写入，impact tree 为每个 ChangeFact 独立展开。

package facts

// ChangeKind 枚举传播根的领域种类，决定 impact tree 如何进入反向图与路由图。
type ChangeKind string

const (
	// ChangeKindSymbolChanged 表示某个 declaration symbol 被改动，沿 ReverseGraph 反向传播。
	ChangeKindSymbolChanged ChangeKind = "symbol_changed"
	// ChangeKindRouteGroupChanged 表示某个 route group 被改动，进入 RouteGraph 找 descendant routes。
	ChangeKindRouteGroupChanged ChangeKind = "route_group_changed"
	// ChangeKindRouteChanged 表示某条 route 注册被改动，直接进入 RouteGraph。
	ChangeKindRouteChanged ChangeKind = "route_changed"
	// ChangeKindRouteDeleted 表示某条 route 被删除，由删除路由恢复阶段合成。
	ChangeKindRouteDeleted ChangeKind = "route_deleted"
	// ChangeKindMiddlewareChanged 表示某个 middleware 被改动，进入 RouteGraph 找其后注册的 route。
	ChangeKindMiddlewareChanged ChangeKind = "middleware_changed"
	// ChangeKindJobRegistrationChanged 表示静态任务注册语句被改动。
	ChangeKindJobRegistrationChanged ChangeKind = "job_registration_changed"
	// ChangeKindDubboProviderChanged 表示 Dubbo method provider 注册配置被改动。
	ChangeKindDubboProviderChanged ChangeKind = "dubbo_provider_changed"
	// ChangeKindDubboServiceChanged 表示 Dubbo interface/version 等 service 级注册配置被改动。
	ChangeKindDubboServiceChanged ChangeKind = "dubbo_service_changed"
	// ChangeKindAnnotationChanged 表示某个 annotation 被改动，直接落到 endpoint。
	ChangeKindAnnotationChanged ChangeKind = "annotation_changed"
	// ChangeKindFileChanged 是文件级 fallback root，无法映射到更精确语义 fact 时使用。
	ChangeKindFileChanged ChangeKind = "file_changed"
)

// ChangeRange 描述一个 diff 变更区间在新版本源码中的行号范围。
type ChangeRange struct {
	// StartLine 是变更区间起始行号（新版本）。
	StartLine int `json:"start_line"`
	// EndLine 是变更区间结束行号（新版本）。
	EndLine int `json:"end_line"`
}

// ChangeFact 描述 diff 映射到的一个传播根，是 impact tree 的根节点来源。
// 每个 ChangeFact 独立生成一个 root，多个 root 互不覆盖。
type ChangeFact struct {
	// ID 是该传播根的唯一标识。
	ID string `json:"id"`
	// Kind 是领域种类，决定后续传播路径。
	Kind ChangeKind `json:"kind"`
	// TargetID 是该 root 指向的目标领域 fact ID（如 route/group/annotation），symbol/file 类 root 留空不输出。
	TargetID string `json:"target_id,omitempty"`
	// SymbolID 是 symbol 类 root 指向的 symbol ID；非 symbol root 留空不输出。
	SymbolID SymbolID `json:"symbol_id,omitempty"`
	// File 是变更所在文件（项目相对路径）。
	File string `json:"file"`
	// Ranges 是该 root 在文件中的变更区间列表。
	Ranges []ChangeRange `json:"ranges"`
	// Source 标识该 root 的来源（如 diff/deleted_route/gomod_usage）。
	Source string `json:"source"`
	// SourceFactID 是触发该 root 的源 fact ID（如 gomod usage 触发的 symbol/file change），缺失时不输出。
	SourceFactID string `json:"source_fact_id,omitempty"`
	// Confidence 是该 root 的静态证据强度（high/medium/low）。
	Confidence Confidence `json:"confidence"`
}
