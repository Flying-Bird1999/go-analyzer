// route.go 实现路由领域事实类型：wrapper、route group、route registration 与 middleware binding。

package facts

// WrapperFact 描述 route 注册或 group 创建时包裹 handler 的 wrapper（如 ControllerWithReqResp）。
type WrapperFact struct {
	// Name 是 wrapper 标识符名称。
	Name string `json:"name"`
	// Raw 是 wrapper 调用的原始表达式文本，供调试与人工 review。
	Raw string `json:"raw"`
	// Guessed 标记该 wrapper 是否经"结构兜底"猜测得出：调用名不在已知 handler
	// wrapper 白名单中时，提取器会退化为"取最后一个长得像 handler 的实参"这一启发式
	// （handlerArgument 的非白名单分支）。当 wrapper 语义并非原样转发（例如记录/审计
	// 后返回另一个闭包、按条件交换实参等）时，猜出的表达式可能并非真正被注册的
	// handler。Guessed=true 提示这条 wrapper 未经白名单验证，是启发式而非已确认证据；
	// 缺省为 false（省略时表示已知白名单 wrapper 或非 handler wrapper 场景，如
	// route group wrapper）。
	Guessed bool `json:"guessed,omitempty"`
}

// RouteGroupFact 描述一个路由分组，记录其变量名、前缀、父级 group 与所在 route function。
type RouteGroupFact struct {
	// ID 是 group 事实的唯一标识。
	ID string `json:"id"`
	// GroupVar 是该 group 在 route function 内的局部变量名。
	GroupVar string `json:"group_var"`
	// ParentGroupID 是父级 group 的 ID，根 group 留空不输出。
	ParentGroupID string `json:"parent_group_id,omitempty"`
	// ParentGroupVar 是父级 group 的变量名，根 group 留空不输出。
	ParentGroupVar string `json:"parent_group_var,omitempty"`
	// Prefix 是该 group 自身声明的前缀，子 group 的完整前缀由父级前缀拼接而来。
	Prefix string `json:"prefix"`
	// PrefixRaw 是无法静态解析的 group prefix 表达式；存在时不得把 Prefix 当作完整路径。
	PrefixRaw string `json:"prefix_raw,omitempty"`
	// RouteFunc 是声明该 group 的 route function symbol ID。
	RouteFunc SymbolID `json:"route_func"`
	// StatementIndex 是该 group 创建语句在 route function 内的语句序号，用于中间件顺序判断。
	StatementIndex int `json:"statement_index"`
	// Span 是 group 创建表达式的位置区间。
	Span SourceSpan `json:"span"`
}

// RouteRegistrationFact 描述一条路由注册（如 g.GET("/path", handler)），是路由域的核心事实。
// route extractor 与删除路由恢复阶段共同写入；linker 会回填 HandlerSymbol。
type RouteRegistrationFact struct {
	// ID 是 route 注册事实的唯一标识。
	ID string `json:"id"`
	// Method 是 HTTP 方法（GET/POST/...）。
	Method string `json:"method"`
	// LocalPath 是该 route 在注册点声明的局部路径，未包含 group 前缀。
	LocalPath string `json:"local_path"`
	// PathRaw 是路径参数的原始文本，留空时不输出。
	PathRaw string `json:"path_raw,omitempty"`
	// ResolvedPath 是拼接 group 前缀后的完整路径，无法解析时留空不输出。
	ResolvedPath string `json:"resolved_path,omitempty"`
	// GroupID 是该 route 所属 group 的事实 ID。
	GroupID string `json:"group_id"`
	// GroupVar 是该 route 所属 group 的变量名。
	GroupVar string `json:"group_var"`
	// HandlerRaw 是 route 注册点 handler 表达式的原始文本，尚未解析为 symbol。
	HandlerRaw string `json:"handler_raw"`
	// HandlerSymbol 是经 linker 解析后的 handler symbol ID；未解析时留空不输出。
	HandlerSymbol SymbolID `json:"handler_symbol,omitempty"`
	// Wrappers 是包裹 handler 的 wrapper 列表，缺失时不输出。
	Wrappers []WrapperFact `json:"wrappers,omitempty"`
	// RouteFunc 是声明该 route 的 route function symbol ID。
	RouteFunc SymbolID `json:"route_func"`
	// StatementIndex 是该 route 注册语句在 route function 内的语句序号，用于中间件顺序判断。
	StatementIndex int `json:"statement_index"`
	// RecoveredFromDiff 标记该 route 是否由删除路由恢复阶段合成，仅供内部传播使用，不进入公开 JSON。
	RecoveredFromDiff bool `json:"-"`
	// File 是该 route 注册所在的文件（项目相对路径）。
	File string `json:"file"`
	// Span 是 route 注册表达式的位置区间。
	Span SourceSpan `json:"span"`
	// Evidence 记录关键 AST 表达式的证据（kind/raw/span），供 facts 调试与解释能力复用，缺失时不输出。
	Evidence []EvidenceFact `json:"evidence,omitempty"`
}

// MiddlewareBindingFact 描述路由分组上绑定的中间件及其注册顺序。
// route extractor 提取原始表达式，linker 解析其中间件 symbol。
type MiddlewareBindingFact struct {
	// ID 是中间件绑定事实的唯一标识。
	ID string `json:"id"`
	// GroupID 是该中间件绑定所属 group 的事实 ID。
	GroupID string `json:"group_id"`
	// GroupVar 是该中间件绑定所属 group 的变量名。
	GroupVar string `json:"group_var"`
	// MiddlewareRaw 是中间件表达式的原始文本，尚未解析为 symbol。
	MiddlewareRaw string `json:"middleware_raw"`
	// MiddlewareSymbols 是经 linker 解析后的中间件 function/method symbol 列表；未解析时留空不输出。
	MiddlewareSymbols []SymbolID `json:"middleware_symbols,omitempty"`
	// RouteFunc 是执行绑定的 route function symbol ID。
	RouteFunc SymbolID `json:"route_func"`
	// StatementIndex 是该绑定语句在 route function 内的语句序号，用于判断在其后注册的 route 受影响。
	StatementIndex int `json:"statement_index"`
	// Span 是中间件绑定表达式的位置区间。
	Span SourceSpan `json:"span"`
}
