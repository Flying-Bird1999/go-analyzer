package diagnostics

type Code string

const (
	CodeRouteDynamicPath            Code = "route_dynamic_path"
	CodeRouteUnresolvedHandler      Code = "route_unresolved_handler"
	CodeRouteWrapperUnsupported     Code = "route_wrapper_unsupported"
	CodeMiddlewareOrderUncertain    Code = "middleware_order_uncertain"
	CodeAnnotationMissingForHandler Code = "annotation_missing_for_handler"
	CodePackageLoadFailed           Code = "package_load_failed"
	CodeModuleUsageFileFallback     Code = "module_usage_file_fallback"
	CodeModuleUnreferenced          Code = "module_unreferenced"
)

type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)
