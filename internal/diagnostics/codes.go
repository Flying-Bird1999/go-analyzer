package diagnostics

type Code string

const (
	CodeRouteDynamicPath                       Code = "route_dynamic_path"
	CodeRouteUnresolvedHandler                 Code = "route_unresolved_handler"
	CodeDeletedRouteUnresolved                 Code = "deleted_route_unresolved"
	CodeDeletedRouteHandlerUnresolved          Code = "deleted_route_handler_unresolved"
	CodeDeletedRouteEndpointFallback           Code = "deleted_route_endpoint_fallback"
	CodeModuleDiffUnresolved                   Code = "module_diff_unresolved"
	CodeModuleUsageFileFallback                Code = "module_usage_file_fallback"
	CodeModuleUnreferenced                     Code = "module_unreferenced"
	CodeSymbolReferenceUnresolved              Code = "symbol_reference_unresolved"
	CodeSymbolReferenceAmbiguousInterface      Code = "symbol_reference_ambiguous_interface"
	CodeSymbolReferenceUnknownInterfaceBinding Code = "symbol_reference_unknown_interface_binding"
	CodeTypeReferenceUnresolved                Code = "type_reference_unresolved"
	CodeDeletedSymbolUnresolved                Code = "deleted_symbol_unresolved"
)

type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)
