// contract.go 实现 facts / impact 的 JSON Schema 构造与导出。
// schema 文档以 map[string]any 描述，按需引用公共定义；SchemaJSON 把指定文档序列化为缩进 JSON。
package output

import (
	"encoding/json"
	"fmt"
)

// schemaDocuments 内置 facts 与 impact 两份 JSON Schema 文档（JSON Schema 2020-12 draft）。
// facts 描述完整项目事实快照；impact 描述对外可 review 的传播树。
var schemaDocuments = map[string]map[string]any{
	"facts": {
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://gopkg.inshopline.com/bff/go-analyzer/schemas/facts.v1alpha1.schema.json",
		"title":                "go-analyzer facts output",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"project", "symbols", "annotations", "route_groups", "routes", "middleware", "references", "modules", "im_events", "grpc_operations", "grpc_calls", "grpc_providers", "dubbo_providers", "job_registrations", "links", "diagnostics"},
		"properties": map[string]any{
			"project":           ref("project"),
			"symbols":           arrayOf(ref("symbol")),
			"annotations":       arrayOf(ref("annotation")),
			"route_groups":      arrayOf(ref("route_group")),
			"routes":            arrayOf(ref("route")),
			"middleware":        arrayOf(ref("middleware")),
			"references":        arrayOf(ref("reference")),
			"modules":           arrayOf(ref("module")),
			"im_events":         arrayOf(ref("im_event")),
			"grpc_operations":   arrayOf(ref("grpc_operation")),
			"grpc_calls":        arrayOf(ref("grpc_call")),
			"grpc_providers":    arrayOf(ref("grpc_provider")),
			"dubbo_providers":   arrayOf(ref("dubbo_provider")),
			"job_registrations": arrayOf(ref("job_registration")),
			"links":             arrayOf(ref("link")),
			"diagnostics":       arrayOf(ref("diagnostic")),
		},
		"$defs": factsDefinitions(),
	},
	"impact": {
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://gopkg.inshopline.com/bff/go-analyzer/schemas/go-impact.v1alpha1.schema.json",
		"title":                "go-analyzer reviewable impact tree",
		"type":                 "object",
		"additionalProperties": false,
		// fileSources、grpcSources 与 endpointSourcesSummary 必填；moduleSources 仅在形成模块变更时输出。
		"required": []string{"summary", "fileSources", "grpcSources", "endpointSourcesSummary"},
		"properties": map[string]any{
			"summary":                ref("impact_summary"),
			"fileSources":            arrayOf(ref("file_source_impact")),
			"moduleSources":          arrayOf(ref("module_source_impact")),
			"grpcSources":            arrayOf(ref("grpc_source_impact")),
			"endpointSourcesSummary": arrayOf(ref("endpoint_source_summary")),
		},
		"$defs": impactDefinitions(),
	},
	"grpc-impact": {
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://gopkg.inshopline.com/bff/go-analyzer/schemas/grpc-impact.v1alpha1.schema.json",
		"title":                "go-analyzer service entry impact tree",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"summary", "fileSources", "entrySourcesSummary"},
		"properties": map[string]any{
			"summary":             ref("service_entry_impact_groups"),
			"fileSources":         arrayOf(ref("service_entry_file_source_impact")),
			"moduleSources":       arrayOf(ref("service_entry_module_source_impact")),
			"entrySourcesSummary": ref("service_entry_source_summary_groups"),
		},
		"$defs": grpcImpactDefinitions(),
	},
}

// SchemaJSON 返回指定名称（facts / impact）的 JSON Schema 文档。
// 未知名称返回错误，避免误导。序列化结果末尾追加换行，便于文件落地。
func SchemaJSON(name string) ([]byte, error) {
	doc, ok := schemaDocuments[name]
	if !ok {
		return nil, fmt.Errorf("unsupported schema type %q", name)
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// ref 构造指向当前文档 $defs 下指定名称的 $ref 片段。
func ref(name string) map[string]any {
	return map[string]any{"$ref": "#/$defs/" + name}
}

// arrayOf 把任意 schema 片段包装为 array 类型，items 引用该片段。
func arrayOf(item any) map[string]any {
	return map[string]any{"type": "array", "items": item}
}

// object 构造一个 additionalProperties=false 的 object schema，并列出 required 字段。
// required 字段在 JSON 输出时必须存在，未列出的 properties 字段为可选。
func object(properties map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             required,
		"properties":           properties,
	}
}

// factsDefinitions 选择 facts schema 需要的公共定义，仅暴露项目事实相关的 $defs。
// 不包含 impact 专用定义（如 impact_node）与 diff-only 的瞬态事实（如 change / module_change）。
func factsDefinitions() map[string]any {
	return selectDefinitions(
		"annotation",
		"build_context",
		"diagnostic",
		"evidence",
		"grpc_call",
		"grpc_client_binding",
		"grpc_operation",
		"grpc_provider",
		"dubbo_provider",
		"job_registration",
		"im_event",
		"im_event_dependency",
		"im_event_evidence",
		"link",
		"middleware",
		"module",
		"project",
		"reference",
		"route",
		"route_group",
		"source_span",
		"symbol",
		"wrapper",
	)
}

func grpcImpactDefinitions() map[string]any {
	return selectDefinitions(
		"endpoint_root_symbol_summary",
		"contract_registration_summary",
		"contract_source_summary",
		"service_entry_impact_groups",
		"service_entry_source_summary_groups",
		"service_contract_summary",
		"service_entry_file_source_impact",
		"service_entry_module_source_impact",
		"service_entry_impact_source",
		"impact_node",
		"module_replacement",
	)
}

// impactDefinitions 选择 impact schema 需要的公共定义，仅暴露对外 review 树相关的 $defs。
// 不包含 facts 项目事实定义，也不暴露已退役的 edge / endpoint_impact 等历史定义。
func impactDefinitions() map[string]any {
	return selectDefinitions(
		"endpoint_impact_source",
		"endpoint_root_symbol_summary",
		"endpoint_source_summary",
		"endpoint_summary",
		"file_source_impact",
		"grpc_consumer_impact",
		"grpc_operation_summary",
		"grpc_source_impact",
		"dependency_call_site",
		"dependency_chain",
		"dependency_client",
		"dependency_endpoint",
		"dependency_symbol",
		"impact_node",
		"impact_summary",
		"module_replacement",
		"module_source_impact",
	)
}

// selectDefinitions 从 commonDefinitions 中挑选指定名称的子集。
// 通过显式白名单避免向某份 schema 暴露不属于它的定义，保证契约边界清晰。
func selectDefinitions(names ...string) map[string]any {
	all := commonDefinitions()
	selected := make(map[string]any, len(names))
	for _, name := range names {
		selected[name] = all[name]
	}
	return selected
}

// commonDefinitions 返回 facts / impact schema 共享的全部类型定义。
// 每个定义描述一类 JSON 对象的字段集与 required 列表，factsDefinitions / impactDefinitions
// 通过 selectDefinitions 选择各自需要的子集，从而精确控制每份 schema 暴露的字段边界。
func commonDefinitions() map[string]any {
	return map[string]any{
		"annotation": object(map[string]any{
			"id":             stringType(),
			"kind":           stringType(),
			"method":         stringType(),
			"path":           stringType(),
			"raw":            stringType(),
			"handler_symbol": stringType(),
			"span":           ref("source_span"),
		}, "id", "kind", "method", "path", "raw", "handler_symbol", "span"),
		"diagnostic": object(map[string]any{
			"id":               stringType(),
			"code":             stringType(),
			"severity":         stringType(),
			"message":          stringType(),
			"span":             ref("source_span"),
			"related_fact_ids": arrayOf(stringType()),
		}, "id", "code", "severity", "message"),
		"endpoint_summary": object(map[string]any{
			"method": stringType(),
			"path":   stringType(),
		}, "method", "path"),
		"endpoint_root_symbol_summary": object(map[string]any{
			"id":   stringType(),
			"kind": stringType(),
			"name": stringType(),
			"file": stringType(),
		}, "id", "kind"),
		"endpoint_impact_source": object(map[string]any{
			"sourceType":     stringType(),
			"sourceFile":     stringType(),
			"modulePath":     stringType(),
			"changeType":     stringType(),
			"versionBefore":  stringType(),
			"versionAfter":   stringType(),
			"grpcFullMethod": stringType(),
			"rootSymbols":    arrayOf(ref("endpoint_root_symbol_summary")),
			"chains":         arrayOf(arrayOf(stringType())),
			"confidence":     confidenceType(),
		}, "sourceType", "rootSymbols", "chains", "confidence"),
		"dependency_call_site": object(map[string]any{
			"file":   stringType(),
			"line":   numberType(),
			"column": numberType(),
		}, "file", "line", "column"),
		"dependency_chain": object(map[string]any{
			"symbols":  arrayOf(ref("dependency_symbol")),
			"callSite": ref("dependency_call_site"),
		}, "symbols", "callSite"),
		"dependency_client": object(map[string]any{
			"goPackage":  stringType(),
			"clientType": stringType(),
			"goMethod":   stringType(),
		}, "goPackage", "clientType", "goMethod"),
		"dependency_endpoint": object(map[string]any{
			"method": stringType(),
			"path":   stringType(),
		}, "method", "path"),
		"dependency_symbol": object(map[string]any{
			"id":   stringType(),
			"kind": stringType(),
			"name": stringType(),
			"file": stringType(),
		}, "id", "kind", "name", "file"),
		"grpc_consumer_impact": object(map[string]any{
			"endpoint": ref("dependency_endpoint"),
			"relation": stringType(),
			"handlers": arrayOf(ref("dependency_symbol")),
			"clients":  arrayOf(ref("dependency_client")),
			"chains":   arrayOf(ref("dependency_chain")),
		}, "endpoint", "relation", "handlers", "clients", "chains"),
		"grpc_operation_summary": object(map[string]any{
			"fullMethod":   stringType(),
			"protoPackage": stringType(),
			"service":      stringType(),
			"method":       stringType(),
		}, "fullMethod", "protoPackage", "service", "method"),
		"grpc_source_impact": object(map[string]any{
			"grpc":              ref("grpc_operation_summary"),
			"consumers":         arrayOf(ref("grpc_consumer_impact")),
			"impactedEndpoints": arrayOf(ref("endpoint_summary")),
		}, "grpc", "consumers", "impactedEndpoints"),
		"endpoint_source_summary": object(map[string]any{
			"method":  stringType(),
			"path":    stringType(),
			"sources": arrayOf(ref("endpoint_impact_source")),
		}, "method", "path", "sources"),
		"file_source_impact": object(map[string]any{
			"sourceFile":        stringType(),
			"diff":              stringType(),
			"symbols":           map[string]any{"type": "object", "additionalProperties": ref("impact_node")},
			"impactedEndpoints": arrayOf(ref("endpoint_summary")),
			"impactedIMEvents":  arrayOf(stringType()),
		}, "sourceFile", "symbols", "impactedEndpoints", "impactedIMEvents"),
		"impact_summary": object(map[string]any{
			"impactedEndpointCount": numberType(),
			"impactedEndpoints":     arrayOf(ref("endpoint_summary")),
			"impactedIMCount":       numberType(),
			"impactedIMEvents":      arrayOf(stringType()),
		}, "impactedEndpointCount", "impactedEndpoints", "impactedIMCount", "impactedIMEvents"),
		"im_event": object(map[string]any{
			"id":            stringType(),
			"event":         stringType(),
			"event_raw":     stringType(),
			"sender_symbol": stringType(),
			"dependencies":  arrayOf(ref("im_event_dependency")),
			"evidence":      arrayOf(ref("im_event_evidence")),
			"confidence":    confidenceType(),
			"span":          ref("source_span"),
			"resolved":      boolType(),
		}, "id", "sender_symbol", "dependencies", "evidence", "confidence", "span", "resolved"),
		"im_event_dependency": object(map[string]any{
			"symbol_id":  stringType(),
			"relation":   stringType(),
			"confidence": confidenceType(),
			"span":       ref("source_span"),
		}, "symbol_id", "relation", "confidence"),
		"im_event_evidence": object(map[string]any{
			"relation": stringType(),
			"span":     ref("source_span"),
		}, "relation", "span"),
		"evidence": object(map[string]any{
			"kind":       stringType(),
			"raw":        stringType(),
			"span":       ref("source_span"),
			"confidence": confidenceType(),
		}, "kind", "span"),
		"grpc_client_binding": object(map[string]any{
			"go_package":  stringType(),
			"client_type": stringType(),
			"go_method":   stringType(),
		}, "go_package", "client_type", "go_method"),
		"grpc_operation": object(map[string]any{
			"id":              stringType(),
			"full_method":     stringType(),
			"proto_package":   stringType(),
			"service":         stringType(),
			"method":          stringType(),
			"streaming_mode":  stringType(),
			"client_bindings": arrayOf(ref("grpc_client_binding")),
			"evidence":        arrayOf(ref("evidence")),
		}, "id", "full_method", "proto_package", "service", "method", "streaming_mode", "client_bindings", "evidence"),
		"grpc_call": object(map[string]any{
			"id":             stringType(),
			"caller_symbol":  stringType(),
			"operation_id":   stringType(),
			"client_binding": ref("grpc_client_binding"),
			"span":           ref("source_span"),
			"evidence":       arrayOf(ref("evidence")),
		}, "id", "caller_symbol", "operation_id", "client_binding", "span", "evidence"),
		"grpc_provider": object(map[string]any{
			"id":                        stringType(),
			"operation_id":              stringType(),
			"generated_go_package":      stringType(),
			"register_function":         stringType(),
			"server_interface":          stringType(),
			"implementation_go_package": stringType(),
			"implementation_type":       stringType(),
			"implementation_symbol":     stringType(),
			"handler_symbol":            stringType(),
			"registration_symbol":       stringType(),
			"span":                      ref("source_span"),
			"evidence":                  arrayOf(ref("evidence")),
			"confidence":                confidenceType(),
		}, "id", "operation_id", "generated_go_package", "register_function", "server_interface", "registration_symbol", "span", "confidence"),
		"dubbo_provider": object(map[string]any{
			"id":                  stringType(),
			"interface":           stringType(),
			"version":             stringType(),
			"version_expression":  stringType(),
			"method":              stringType(),
			"go_method":           stringType(),
			"implementation_type": stringType(),
			"handler_symbol":      stringType(),
			"registration_symbol": stringType(),
			"span":                ref("source_span"),
			"service_span":        ref("source_span"),
			"evidence":            arrayOf(ref("evidence")),
			"confidence":          confidenceType(),
		}, "id", "interface", "method", "go_method", "implementation_type", "handler_symbol", "registration_symbol", "span", "service_span", "confidence"),
		"job_registration": object(map[string]any{
			"id":                  stringType(),
			"name":                stringType(),
			"handler_symbol":      stringType(),
			"registration_symbol": stringType(),
			"span":                ref("source_span"),
			"evidence":            arrayOf(ref("evidence")),
			"confidence":          confidenceType(),
		}, "id", "name", "handler_symbol", "registration_symbol", "span", "confidence"),
		// impact_node 是 impact 传播树的递归节点定义；children 自引用 impact_node，实现完整传播链路。
		"impact_node": object(map[string]any{
			"id":         stringType(),
			"kind":       stringType(),
			"name":       stringType(),
			"file":       stringType(),
			"package":    stringType(),
			"relation":   stringType(),
			"raw":        stringType(),
			"confidence": confidenceType(),
			"level":      numberType(),
			"cycle":      boolType(),
			"children":   arrayOf(ref("impact_node")),
			"method":     stringType(),
			"path":       stringType(),
			"fullMethod": stringType(),
		}, "id", "kind", "level", "children"),
		"service_entry_impact_groups": object(map[string]any{
			"grpc":  arrayOf(ref("service_contract_summary")),
			"dubbo": arrayOf(ref("service_contract_summary")),
			"http":  arrayOf(ref("service_contract_summary")),
			"job":   arrayOf(ref("service_contract_summary")),
		}, "grpc", "dubbo", "http", "job"),
		"service_entry_file_source_impact": object(map[string]any{
			"sourceFile": stringType(),
			"diff":       stringType(),
			"symbols":    map[string]any{"type": "object", "additionalProperties": ref("impact_node")},
			"impacts":    ref("service_entry_impact_groups"),
		}, "sourceFile", "symbols", "impacts"),
		"service_entry_module_source_impact": object(map[string]any{
			"modulePath":        stringType(),
			"changeType":        stringType(),
			"versionBefore":     stringType(),
			"versionAfter":      stringType(),
			"replacementBefore": ref("module_replacement"),
			"replacementAfter":  ref("module_replacement"),
			"basis":             stringType(),
			"sourceFiles":       arrayOf(ref("service_entry_file_source_impact")),
		}, "modulePath", "changeType", "basis"),
		"service_entry_impact_source": object(map[string]any{
			"sourceType":    stringType(),
			"sourceFile":    stringType(),
			"modulePath":    stringType(),
			"changeType":    stringType(),
			"versionBefore": stringType(),
			"versionAfter":  stringType(),
			"rootSymbols":   arrayOf(ref("endpoint_root_symbol_summary")),
			"chains":        arrayOf(arrayOf(stringType())),
			"confidence":    confidenceType(),
		}, "sourceType", "rootSymbols", "chains", "confidence"),
		"contract_registration_summary": object(map[string]any{
			"file":   stringType(),
			"line":   numberType(),
			"column": numberType(),
		}, "file", "line", "column"),
		"service_contract_summary": object(map[string]any{
			"id":                     stringType(),
			"kind":                   stringType(),
			"identity":               stringType(),
			"identityResolution":     stringType(),
			"fullMethod":             stringType(),
			"method":                 stringType(),
			"path":                   stringType(),
			"localPath":              stringType(),
			"pathExpression":         stringType(),
			"jobName":                stringType(),
			"dubboInterface":         stringType(),
			"dubboVersion":           stringType(),
			"dubboVersionExpression": stringType(),
			"dubboMethod":            stringType(),
			"registration":           ref("contract_registration_summary"),
		}, "id", "kind", "identity", "identityResolution", "registration"),
		"contract_source_summary": object(map[string]any{
			"contract": ref("service_contract_summary"),
			"sources":  arrayOf(ref("service_entry_impact_source")),
		}, "contract", "sources"),
		"service_entry_source_summary_groups": object(map[string]any{
			"grpc":  arrayOf(ref("contract_source_summary")),
			"dubbo": arrayOf(ref("contract_source_summary")),
			"http":  arrayOf(ref("contract_source_summary")),
			"job":   arrayOf(ref("contract_source_summary")),
		}, "grpc", "dubbo", "http", "job"),
		"module_replacement": object(map[string]any{
			"path":    stringType(),
			"version": stringType(),
		}, "path"),
		"module_source_impact": object(map[string]any{
			"modulePath":        stringType(),
			"changeType":        stringType(),
			"versionBefore":     stringType(),
			"versionAfter":      stringType(),
			"replacementBefore": ref("module_replacement"),
			"replacementAfter":  ref("module_replacement"),
			"basis":             stringType(),
			"sourceFiles":       arrayOf(ref("file_source_impact")),
		}, "modulePath", "changeType", "basis"),
		"link": object(map[string]any{
			"id":         stringType(),
			"kind":       stringType(),
			"from_id":    stringType(),
			"to_id":      stringType(),
			"confidence": confidenceType(),
		}, "id", "kind", "from_id", "to_id", "confidence"),
		"middleware": object(map[string]any{
			"id":                 stringType(),
			"group_id":           stringType(),
			"group_var":          stringType(),
			"middleware_raw":     stringType(),
			"middleware_symbols": arrayOf(stringType()),
			"route_func":         stringType(),
			"statement_index":    numberType(),
			"span":               ref("source_span"),
		}, "id", "group_id", "group_var", "middleware_raw", "route_func", "statement_index", "span"),
		"module": object(map[string]any{
			"id":              stringType(),
			"path":            stringType(),
			"version":         stringType(),
			"indirect":        boolType(),
			"replace_path":    stringType(),
			"replace_version": stringType(),
		}, "id", "path", "version", "indirect"),
		"project": object(map[string]any{
			"root":          stringType(),
			"module_path":   stringType(),
			"build_context": ref("build_context"),
		}, "root", "module_path", "build_context"),
		"build_context": object(map[string]any{
			"goos":        stringType(),
			"goarch":      stringType(),
			"tags":        arrayOf(stringType()),
			"cgo_enabled": boolType(),
		}, "goos", "goarch", "tags", "cgo_enabled"),
		"reference": object(map[string]any{
			"id":          stringType(),
			"kind":        stringType(),
			"from_symbol": stringType(),
			"to_symbol":   stringType(),
			"to_raw":      stringType(),
			"confidence":  confidenceType(),
			"span":        ref("source_span"),
			"evidence":    arrayOf(ref("evidence")),
		}, "id", "kind", "from_symbol", "confidence", "span"),
		"route": object(map[string]any{
			"id":              stringType(),
			"method":          stringType(),
			"local_path":      stringType(),
			"path_raw":        stringType(),
			"resolved_path":   stringType(),
			"group_id":        stringType(),
			"group_var":       stringType(),
			"handler_raw":     stringType(),
			"handler_symbol":  stringType(),
			"wrappers":        arrayOf(ref("wrapper")),
			"route_func":      stringType(),
			"statement_index": numberType(),
			"file":            stringType(),
			"span":            ref("source_span"),
			"evidence":        arrayOf(ref("evidence")),
		}, "id", "method", "local_path", "group_id", "group_var", "handler_raw", "route_func", "statement_index", "file", "span"),
		"route_group": object(map[string]any{
			"id":               stringType(),
			"group_var":        stringType(),
			"parent_group_id":  stringType(),
			"parent_group_var": stringType(),
			"prefix":           stringType(),
			"prefix_raw":       stringType(),
			"route_func":       stringType(),
			"statement_index":  numberType(),
			"span":             ref("source_span"),
		}, "id", "group_var", "prefix", "route_func", "statement_index", "span"),
		"source_span": object(map[string]any{
			"file":       stringType(),
			"start_line": numberType(),
			"start_col":  numberType(),
			"end_line":   numberType(),
			"end_col":    numberType(),
		}, "file", "start_line", "start_col", "end_line", "end_col"),
		"symbol": object(map[string]any{
			"id":           stringType(),
			"kind":         stringType(),
			"package_path": stringType(),
			"receiver":     stringType(),
			"name":         stringType(),
			"span":         ref("source_span"),
		}, "id", "kind", "package_path", "name", "span"),
		"wrapper": object(map[string]any{
			"name": stringType(),
			"raw":  stringType(),
		}, "name", "raw"),
	}
}

// stringType 返回 JSON Schema 的 string 类型片段。
func stringType() map[string]any {
	return map[string]any{"type": "string"}
}

// confidenceType 返回 confidence 枚举类型，限定 high / medium / low 三档。
func confidenceType() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"high", "medium", "low"}}
}

// numberType 返回 integer 类型片段，用于 line / col / level / count 等数值字段。
func numberType() map[string]any {
	return map[string]any{"type": "integer"}
}

// boolType 返回 boolean 类型片段，用于 cycle / resolved / indirect 等布尔字段。
func boolType() map[string]any {
	return map[string]any{"type": "boolean"}
}
