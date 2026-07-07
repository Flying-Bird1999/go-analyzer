// module.go 实现 go.mod 相关事实类型：dependency、change 与本仓 usage 映射。
// 由 gomod extractor 产出，仅 ModuleDependencyFact 进入公开 facts JSON；
// ModuleChanges/ModuleUsages 仅在 impact 阶段填充，供传播使用。

package facts

// ModuleDependencyFact 描述当前 go.mod 中的一条 dependency 或 replace 项。
type ModuleDependencyFact struct {
	// ID 是该依赖事实的唯一标识。
	ID string `json:"id"`
	// Path 是依赖的 module path。
	Path string `json:"path"`
	// Version 是依赖版本。
	Version string `json:"version"`
	// Indirect 指示该依赖是否为间接依赖。
	Indirect bool `json:"indirect"`
	// ReplacePath 是 replace 指令的目标 path，无 replace 时留空不输出。
	ReplacePath string `json:"replace_path,omitempty"`
	// ReplaceVersion 是 replace 指令的目标 version，无 replace 时留空不输出。
	ReplaceVersion string `json:"replace_version,omitempty"`
}

// ModuleChangeKind 枚举 go.mod diff 识别出的模块变更种类。
type ModuleChangeKind string

const (
	// ModuleChangeAdded 表示新增依赖。
	ModuleChangeAdded ModuleChangeKind = "added"
	// ModuleChangeRemoved 表示删除依赖。
	ModuleChangeRemoved ModuleChangeKind = "removed"
	// ModuleChangeUpgraded 表示依赖版本升级（按 semantic version 判定方向）。
	ModuleChangeUpgraded ModuleChangeKind = "upgraded"
	// ModuleChangeDowngraded 表示依赖版本降级（按 semantic version 判定方向）。
	ModuleChangeDowngraded ModuleChangeKind = "downgraded"
	// ModuleChangeReplaced 表示 replace 指令发生变化。
	ModuleChangeReplaced ModuleChangeKind = "replaced"
)

// ModuleChangeFact 描述从 go.mod diff 恢复的一条模块变更。
// 仅在 impact 阶段填充，公开 facts JSON 不输出，impact projection 将其合并为面向消费方的 moduleSources。
type ModuleChangeFact struct {
	// ID 是该模块变更事实的唯一标识。
	ID string `json:"id"`
	// Path 是发生变更的 module path。
	Path string `json:"path"`
	// Kind 是变更种类（added/removed/upgraded/downgraded/replaced）。
	Kind ModuleChangeKind `json:"kind"`
	// OldVersion 是变更前版本，新增依赖时留空不输出。
	OldVersion string `json:"old_version,omitempty"`
	// NewVersion 是变更后版本，删除依赖时留空不输出。
	NewVersion string `json:"new_version,omitempty"`
	// OldReplacePath 是 replace 变更前的目标 path，无变化时留空不输出。
	OldReplacePath string `json:"old_replace_path,omitempty"`
	// OldReplaceVersion 是 replace 变更前的目标 version，无变化时留空不输出。
	OldReplaceVersion string `json:"old_replace_version,omitempty"`
	// NewReplacePath 是 replace 变更后的目标 path，无变化时留空不输出。
	NewReplacePath string `json:"new_replace_path,omitempty"`
	// NewReplaceVersion 是 replace 变更后的目标 version，无变化时留空不输出。
	NewReplaceVersion string `json:"new_replace_version,omitempty"`
}

// ModuleUsageBasis 枚举变更模块在本仓 import usage 的解析精度等级。
type ModuleUsageBasis string

const (
	// ModuleUsagePrecise 表示 usage 精确到 symbol（函数/方法体直接使用 import alias）。
	ModuleUsagePrecise ModuleUsageBasis = "module_reference_precise"
	// ModuleUsageFileFallback 表示 usage 只能确认到 importing file，降级为 file/declaration fallback。
	ModuleUsageFileFallback ModuleUsageBasis = "module_reference_file_fallback"
	// ModuleUsageUnreferenced 表示本仓没有 import 该变更模块，不产生 endpoint root。
	ModuleUsageUnreferenced ModuleUsageBasis = "module_unreferenced"
)

// ModuleUsageFact 描述变更模块到本仓 import usage 的映射结果。
// 仅在 impact 阶段填充，公开 facts JSON 不输出；usage 进一步触发对应的 symbol/file ChangeFact。
type ModuleUsageFact struct {
	// ID 是该 usage 事实的唯一标识。
	ID string `json:"id"`
	// ModulePath 是变更模块的 module path。
	ModulePath string `json:"module_path"`
	// ImportPath 是本仓 import 该模块的路径，留空时不输出。
	ImportPath string `json:"import_path,omitempty"`
	// Alias 是本仓 import 该模块时的别名，留空时不输出。
	Alias string `json:"alias,omitempty"`
	// Basis 是 usage 解析精度等级（precise / file_fallback / unreferenced）。
	Basis ModuleUsageBasis `json:"basis"`
	// SymbolID 是 precise 等级下命中的本仓 symbol，非 precise 时留空不输出。
	SymbolID SymbolID `json:"symbol_id,omitempty"`
	// File 是 file_fallback 等级下命中的 importing file，非该等级时留空不输出。
	File string `json:"file,omitempty"`
	// Confidence 是该 usage 的静态证据强度。
	Confidence Confidence `json:"confidence"`
}
