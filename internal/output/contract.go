package output

import (
	"encoding/json"
	"fmt"
)

const SchemaVersion = "v1alpha1"

var schemaDocuments = map[string]map[string]any{
	"facts": {
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://gopkg.inshopline.com/bff/go-analyzer/schemas/facts.v1alpha1.schema.json",
		"title":                "go-analyzer facts output",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"project", "symbols", "annotations", "route_groups", "routes", "middleware", "changes", "references", "modules", "module_changes", "module_usages", "links", "diagnostics"},
		"properties": map[string]any{
			"project":        ref("project"),
			"symbols":        arrayOf(ref("symbol")),
			"annotations":    arrayOf(ref("annotation")),
			"route_groups":   arrayOf(ref("route_group")),
			"routes":         arrayOf(ref("route")),
			"middleware":     arrayOf(ref("middleware")),
			"changes":        arrayOf(ref("change")),
			"references":     arrayOf(ref("reference")),
			"modules":        arrayOf(ref("module")),
			"module_changes": arrayOf(ref("module_change")),
			"module_usages":  arrayOf(ref("module_usage")),
			"links":          arrayOf(ref("link")),
			"diagnostics":    arrayOf(ref("diagnostic")),
		},
		"$defs": factsDefinitions(),
	},
	"impact": {
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://gopkg.inshopline.com/bff/go-analyzer/schemas/go-impact.v1alpha1.schema.json",
		"title":                "go-analyzer reviewable impact tree",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"meta", "summary", "module_changes", "module_usages", "fileSources"},
		"properties": map[string]any{
			"meta":           ref("impact_meta"),
			"summary":        ref("impact_summary"),
			"module_changes": arrayOf(ref("module_change")),
			"module_usages":  arrayOf(ref("module_usage")),
			"fileSources":    arrayOf(ref("file_source_impact")),
		},
		"$defs": impactDefinitions(),
	},
}

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

func ref(name string) map[string]any {
	return map[string]any{"$ref": "#/$defs/" + name}
}

func arrayOf(item any) map[string]any {
	return map[string]any{"type": "array", "items": item}
}

func object(properties map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             required,
		"properties":           properties,
	}
}

func factsDefinitions() map[string]any {
	return selectDefinitions(
		"annotation",
		"change",
		"change_range",
		"diagnostic",
		"link",
		"middleware",
		"module",
		"module_change",
		"module_usage",
		"project",
		"reference",
		"route",
		"route_group",
		"source_span",
		"symbol",
		"wrapper",
	)
}

func impactDefinitions() map[string]any {
	return selectDefinitions(
		"diagnostic",
		"endpoint_summary",
		"file_source_impact",
		"impact_meta",
		"impact_node",
		"impact_summary",
		"module_change",
		"module_usage",
		"source_span",
	)
}

func selectDefinitions(names ...string) map[string]any {
	all := commonDefinitions()
	selected := make(map[string]any, len(names))
	for _, name := range names {
		selected[name] = all[name]
	}
	return selected
}

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
		"change": object(map[string]any{
			"id":         stringType(),
			"kind":       stringType(),
			"target_id":  stringType(),
			"symbol_id":  stringType(),
			"file":       stringType(),
			"ranges":     arrayOf(ref("change_range")),
			"source":     stringType(),
			"confidence": confidenceType(),
		}, "id", "kind", "file", "ranges", "source", "confidence"),
		"change_range": object(map[string]any{
			"start_line": numberType(),
			"end_line":   numberType(),
		}, "start_line", "end_line"),
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
		"file_source_impact": object(map[string]any{
			"sourceFile":        stringType(),
			"diff":              stringType(),
			"symbols":           map[string]any{"type": "object", "additionalProperties": ref("impact_node")},
			"impactedEndpoints": arrayOf(ref("endpoint_summary")),
		}, "sourceFile", "symbols", "impactedEndpoints"),
		"impact_meta": object(map[string]any{
			"schemaVersion": stringType(),
			"projectRoot":   stringType(),
			"diagnostics":   arrayOf(ref("diagnostic")),
		}, "schemaVersion", "projectRoot", "diagnostics"),
		"impact_summary": object(map[string]any{
			"impactedEndpointCount": numberType(),
			"impactedEndpoints":     arrayOf(ref("endpoint_summary")),
		}, "impactedEndpointCount", "impactedEndpoints"),
		"impact_node": object(map[string]any{
			"id":           stringType(),
			"kind":         stringType(),
			"name":         stringType(),
			"file":         stringType(),
			"package":      stringType(),
			"relation":     stringType(),
			"raw":          stringType(),
			"span":         ref("source_span"),
			"confidence":   confidenceType(),
			"level":        numberType(),
			"cycle":        boolType(),
			"stopBoundary": boolType(),
			"children":     arrayOf(ref("impact_node")),
			"method":       stringType(),
			"path":         stringType(),
		}, "id", "kind", "level", "children"),
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
		"module_change": object(map[string]any{
			"id":                  stringType(),
			"path":                stringType(),
			"kind":                stringType(),
			"old_version":         stringType(),
			"new_version":         stringType(),
			"old_replace_path":    stringType(),
			"old_replace_version": stringType(),
			"new_replace_path":    stringType(),
			"new_replace_version": stringType(),
		}, "id", "path", "kind"),
		"module_usage": object(map[string]any{
			"id":          stringType(),
			"module_path": stringType(),
			"import_path": stringType(),
			"alias":       stringType(),
			"basis":       stringType(),
			"symbol_id":   stringType(),
			"file":        stringType(),
			"confidence":  confidenceType(),
		}, "id", "module_path", "basis", "confidence"),
		"project": object(map[string]any{
			"root":        stringType(),
			"module_path": stringType(),
		}, "root", "module_path"),
		"reference": object(map[string]any{
			"id":          stringType(),
			"kind":        stringType(),
			"from_symbol": stringType(),
			"to_symbol":   stringType(),
			"to_raw":      stringType(),
			"confidence":  confidenceType(),
			"span":        ref("source_span"),
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
			"source_family":   stringType(),
			"file":            stringType(),
			"span":            ref("source_span"),
		}, "id", "method", "local_path", "group_id", "group_var", "handler_raw", "route_func", "statement_index", "file", "span"),
		"route_group": object(map[string]any{
			"id":               stringType(),
			"group_var":        stringType(),
			"parent_group_id":  stringType(),
			"parent_group_var": stringType(),
			"prefix":           stringType(),
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

func stringType() map[string]any {
	return map[string]any{"type": "string"}
}

func confidenceType() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"high", "medium", "low"}}
}

func numberType() map[string]any {
	return map[string]any{"type": "integer"}
}

func boolType() map[string]any {
	return map[string]any{"type": "boolean"}
}
