// store.go 实现流水线共享事实总线 Store 及项目、诊断等基础事实类型。

// Package facts 定义 go-analyzer 流水线各阶段共享的事实总线模型。
//
// Store 作为统一事实总线汇聚各 extractor 的产出：project/app 提供项目元信息，
// astindex 提供声明符号，annotation/route/reference/im/gomod 等 extractor 写入各自领域事实，
// linker 写入关联事实，diff/app/impact 写入 ChangeFact。模块之间通过 facts.Store 通信，
// 而不是直接共享私有 AST 状态。graph 与 impact 在运行时基于 Store 临时构造查询视图，
// 不写回业务事实。
package facts

// ProjectFact 描述被分析 Go 项目的基本信息，包括根目录、module path 和构建上下文。
type ProjectFact struct {
	// Root 是项目根目录，在内存中为绝对路径，进入 facts/output 后统一转为项目相对路径。
	Root string `json:"root"`
	// ModulePath 是 go.mod 中声明的 module path。
	ModulePath string `json:"module_path"`
	// BuildContext 是用于扫描与 build constraints 过滤的 Go 构建上下文。
	BuildContext BuildContextFact `json:"build_context"`
}

// BuildContextFact 记录加载源码时使用的 Go 构建上下文参数。
type BuildContextFact struct {
	// GOOS 是目标操作系统，对应 --goos 参数。
	GOOS string `json:"goos"`
	// GOARCH 是目标架构，对应 --goarch 参数。
	GOARCH string `json:"goarch"`
	// Tags 是 build tags 列表，对应 --tags 参数。
	Tags []string `json:"tags"`
	// CgoEnabled 指示是否启用 cgo，对应 --cgo 参数。
	CgoEnabled bool `json:"cgo_enabled"`
}

// DiagnosticFact 描述一条可恢复的不确定性诊断，不等于程序失败。
// impact JSON 不输出 diagnostics，可通过 facts JSON 检查项目级诊断。
type DiagnosticFact struct {
	// ID 是诊断事实的唯一标识。
	ID string `json:"id"`
	// Code 是诊断码，定义见 internal/diagnostics/codes.go。
	Code string `json:"code"`
	// Severity 是诊断严重级别。
	Severity string `json:"severity"`
	// Message 是人类可读的诊断说明。
	Message string `json:"message"`
	// Span 是诊断关联的源码位置区间，缺失时不输出。
	Span SourceSpan `json:"span,omitempty"`
	// RelatedFactIDs 列出与诊断相关的其他事实 ID，缺失时不输出。
	RelatedFactIDs []string `json:"related_fact_ids,omitempty"`
}

// Store 是 pipeline 内的共享事实总线，承载所有 extractor/linker/diff 阶段产出的事实。
// 模块间通过 Store 通信而非直接共享私有状态。
type Store struct {
	// Project 是被分析项目的基本信息。
	Project ProjectFact `json:"project"`
	// Symbols 是项目内所有 declaration symbol（function/method/type/var/const）。
	Symbols []SymbolFact `json:"symbols"`
	// Annotations 是从 handler 注释中提取的 HTTP method/path 注解。
	Annotations []AnnotationFact `json:"annotations"`
	// RouteGroups 是路由分组及其前缀、父级关系。
	RouteGroups []RouteGroupFact `json:"route_groups"`
	// RouteGroupFlows 是路由分组的跨函数参数/返回值流向事实，仅供传播使用，不进入公开 facts JSON。
	RouteGroupFlows []RouteGroupFlowFact `json:"-"`
	// Routes 是路由注册事实，由 route extractor 与删除路由恢复阶段共同写入。
	Routes []RouteRegistrationFact `json:"routes"`
	// Middleware 是路由分组绑定的中间件及其顺序。
	Middleware []MiddlewareBindingFact `json:"middleware"`
	// Changes 是 diff 映射得到的传播根，由 diff/app/impact 写入。
	Changes []ChangeFact `json:"changes"`
	// References 是 call/type/value 依赖边，由 reference extractor 写入。
	References []ReferenceFact `json:"references"`
	// Modules 是当前 go.mod 中的 dependency/replace。
	Modules []ModuleDependencyFact `json:"modules"`
	// ModuleChanges 是从 go.mod diff 恢复的模块变更。仅在 impact 阶段填充，公开 facts JSON 不输出。
	ModuleChanges []ModuleChangeFact `json:"module_changes"`
	// ModuleUsages 是变更模块在本仓的 import usage 映射。仅在 impact 阶段填充，公开 facts JSON 不输出。
	ModuleUsages []ModuleUsageFact `json:"module_usages"`
	// IMEvents 是出站 IM 事件及其 sender 与精确依赖。
	IMEvents []IMEventFact `json:"im_events"`
	// Links 是 route-handler 与 handler-annotation 的关联事实，由 linker 写入。
	Links []LinkFact `json:"links"`
	// Diagnostics 是各阶段记录的可恢复不确定性诊断。
	Diagnostics []DiagnosticFact `json:"diagnostics"`
}

// NewStore 创建一个空的 Store，并设置项目根目录、module path 与可选的构建上下文。
// 未提供 buildContext 时使用默认值（Tags 为非 nil 空切片），保证 JSON 输出稳定。
func NewStore(root, modulePath string, buildContext ...BuildContextFact) *Store {
	// 默认构建上下文：显式初始化 Tags 为空切片，避免 JSON 中出现 null。
	effectiveBuildContext := BuildContextFact{Tags: []string{}}
	if len(buildContext) > 0 {
		// 调用方显式传入构建上下文时采用其值。
		effectiveBuildContext = buildContext[0]
		// 保证 Tags 永不为 nil，统一为空切片。
		if effectiveBuildContext.Tags == nil {
			effectiveBuildContext.Tags = []string{}
		}
	}
	// 所有数组字段显式初始化为空切片，保证序列化稳定且避免 nil 写入。
	return &Store{
		Project: ProjectFact{
			Root:         root,
			ModulePath:   modulePath,
			BuildContext: effectiveBuildContext,
		},
		Symbols:         []SymbolFact{},
		Annotations:     []AnnotationFact{},
		RouteGroups:     []RouteGroupFact{},
		RouteGroupFlows: []RouteGroupFlowFact{},
		Routes:          []RouteRegistrationFact{},
		Middleware:      []MiddlewareBindingFact{},
		Changes:         []ChangeFact{},
		References:      []ReferenceFact{},
		Modules:         []ModuleDependencyFact{},
		ModuleChanges:   []ModuleChangeFact{},
		ModuleUsages:    []ModuleUsageFact{},
		IMEvents:        []IMEventFact{},
		Links:           []LinkFact{},
		Diagnostics:     []DiagnosticFact{},
	}
}

// AddSymbol 追加一个声明符号事实到 Store 的 Symbols 数组。
func (s *Store) AddSymbol(symbol SymbolFact) {
	s.Symbols = append(s.Symbols, symbol)
}
