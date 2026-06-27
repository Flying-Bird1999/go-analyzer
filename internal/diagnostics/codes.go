package diagnostics

type Code string

const (
	CodeRouteDynamicPath              Code = "route_dynamic_path"
	CodeRouteUnresolvedHandler        Code = "route_unresolved_handler"
	CodeRouteWrapperUnsupported       Code = "route_wrapper_unsupported"
	CodeDeletedRouteUnresolved        Code = "deleted_route_unresolved"
	CodeDeletedRouteHandlerUnresolved Code = "deleted_route_handler_unresolved"
	CodeDeletedRouteEndpointFallback  Code = "deleted_route_endpoint_fallback"
	CodeMiddlewareOrderUncertain      Code = "middleware_order_uncertain"
	CodeAnnotationMissingForHandler   Code = "annotation_missing_for_handler"
	CodePackageLoadFailed             Code = "package_load_failed"
	CodeModuleUsageFileFallback       Code = "module_usage_file_fallback"
	CodeModuleUnreferenced            Code = "module_unreferenced"
	CodePropagationDepthTruncated     Code = "propagation_depth_truncated"
	CodeSymbolReferenceUnresolved     Code = "symbol_reference_unresolved"
	CodeTypeReferenceUnresolved       Code = "type_reference_unresolved"
	CodeDeletedSymbolUnresolved       Code = "deleted_symbol_unresolved"
)

type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)
