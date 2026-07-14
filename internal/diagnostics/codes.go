// codes.go 集中定义诊断码与严重级别枚举，是诊断码的唯一真值来源。
package diagnostics

// Code 是诊断码的类型。诊断码标识诊断的种类，便于程序化过滤与统计。
type Code string

const (
	// CodeRouteDynamicPath：路由路径无法静态解析（例如来自非常量或运行时拼接），route 提取保留原始表达式并降级。
	CodeRouteDynamicPath Code = "route_dynamic_path"
	// CodeRouteUnresolvedHandler：route 注册里的 handler 表达式无法解析为具体符号。
	CodeRouteUnresolvedHandler Code = "route_unresolved_handler"
	// CodeDeletedRouteUnresolved：从 diff 删除块恢复的 route 无法解析出 method/path 等关键信息。
	CodeDeletedRouteUnresolved Code = "deleted_route_unresolved"
	// CodeDeletedRouteHandlerUnresolved：被删除 route 的 handler 无法解析为符号，回退到 method/path fallback。
	CodeDeletedRouteHandlerUnresolved Code = "deleted_route_handler_unresolved"
	// CodeDeletedRouteEndpointFallback：被删除 route 缺少注解，使用 route method/path 作为降级端点。
	CodeDeletedRouteEndpointFallback Code = "deleted_route_endpoint_fallback"
	// CodeModuleDiffUnresolved：go.mod 发生了变更，但无法从中解析出任何 require/replace 模块变化。
	CodeModuleDiffUnresolved Code = "module_diff_unresolved"
	// CodeModuleUsageFileFallback：模块变更只能定位到导入文件，无法精确到具体符号。
	CodeModuleUsageFileFallback Code = "module_usage_file_fallback"
	// CodeModuleUnreferenced：变更模块在本仓没有任何 import 引用，因此不产生传播根。
	CodeModuleUnreferenced Code = "module_unreferenced"
	// CodeSymbolReferenceUnresolved：函数/方法调用无法解析为项目内具体符号（非接口分发场景）。
	CodeSymbolReferenceUnresolved Code = "symbol_reference_unresolved"
	// CodeSymbolReferenceAmbiguousInterface：包级接口变量存在多个具体赋值，严格证据下拒绝猜测分发目标。
	CodeSymbolReferenceAmbiguousInterface Code = "symbol_reference_ambiguous_interface"
	// CodeSymbolReferenceUnknownInterfaceBinding：包级接口变量的赋值来源无法静态解析，拒绝猜测分发目标。
	CodeSymbolReferenceUnknownInterfaceBinding Code = "symbol_reference_unknown_interface_binding"
	// CodeTypeReferenceUnresolved：类型引用无法解析为项目内具体类型符号。
	CodeTypeReferenceUnresolved Code = "type_reference_unresolved"
	// CodeDeletedSymbolUnresolved：删除声明时缺少 base 快照，无法精确恢复被删除的符号，回退到文件级根。
	CodeDeletedSymbolUnresolved Code = "deleted_symbol_unresolved"
	// CodeIMSDKArgumentMismatch：调用按精确 import path 与函数名命中公共 IM SDK，但实参不足以承载 event/payload 位置，疑似 SDK 签名漂移。
	CodeIMSDKArgumentMismatch Code = "im_sdk_argument_mismatch"
	// CodeIMSummaryIterationCapped：IM 摘要不动点传播触达迭代上限，结果可能不完整。
	CodeIMSummaryIterationCapped Code = "im_summary_iteration_capped"
	// CodePackageLoadFailed：单个源码文件解析失败，不中断整体加载，仅记录诊断后继续。
	CodePackageLoadFailed Code = "package_load_failed"
	// CodeGrpcDependencyLoadFailed：gRPC dependency graph 无法在只读模式下解析。
	CodeGrpcDependencyLoadFailed Code = "grpc_dependency_load_failed"
	// CodeGrpcCatalogFailed：generated gRPC client catalog 无法可靠构建。
	CodeGrpcCatalogFailed Code = "grpc_catalog_failed"
	// CodeGrpcCallAmbiguous：项目调用的 receiver 无法唯一收敛到 generated binding。
	CodeGrpcCallAmbiguous Code = "grpc_call_ambiguous"
	// CodeGrpcServerBindingUnresolved: a generated registration is known but its concrete provider type is not statically provable.
	CodeGrpcServerBindingUnresolved Code = "grpc_server_binding_unresolved"
	// CodeGrpcServerBindingAmbiguous: multiple concrete provider types remain possible.
	CodeGrpcServerBindingAmbiguous Code = "grpc_server_binding_ambiguous"
)

// Severity 是诊断严重级别的类型。
type Severity string

const (
	// SeverityInfo：提示性信息，通常不需要处理。
	SeverityInfo Severity = "info"
	// SeverityWarning：需要人工复核的不确定性，是大多数诊断的级别。
	SeverityWarning Severity = "warning"
	// SeverityError：严重问题（当前诊断模型中较少使用）。
	SeverityError Severity = "error"
)
