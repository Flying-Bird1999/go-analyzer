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
		"$defs": commonDefinitions(),
	},
	"impact": {
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://gopkg.inshopline.com/bff/go-analyzer/schemas/impact.v1alpha1.schema.json",
		"title":                "go-analyzer impact output",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"impacted_endpoints", "evidence_chains", "module_impacts", "diagnostics"},
		"properties": map[string]any{
			"impacted_endpoints": arrayOf(ref("endpoint_impact")),
			"evidence_chains":    arrayOf(ref("evidence_chain")),
			"module_impacts":     arrayOf(ref("module_impact")),
			"diagnostics":        arrayOf(map[string]any{"type": "string"}),
		},
		"$defs": commonDefinitions(),
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
			"confidence": stringType(),
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
		"edge": object(map[string]any{
			"from_id": stringType(),
			"to_id":   stringType(),
			"reason":  stringType(),
		}, "from_id", "to_id", "reason"),
		"endpoint_impact": object(map[string]any{
			"id":                stringType(),
			"method":            stringType(),
			"path":              stringType(),
			"annotation_id":     stringType(),
			"handler_symbol":    stringType(),
			"trigger_change_id": stringType(),
			"evidence_chain_id": stringType(),
		}, "id", "method", "path", "annotation_id", "handler_symbol", "trigger_change_id", "evidence_chain_id"),
		"evidence_chain": object(map[string]any{
			"id":    stringType(),
			"nodes": arrayOf(ref("node")),
			"edges": arrayOf(ref("edge")),
		}, "id", "nodes", "edges"),
		"link": object(map[string]any{
			"id":         stringType(),
			"kind":       stringType(),
			"from_id":    stringType(),
			"to_id":      stringType(),
			"confidence": stringType(),
		}, "id", "kind", "from_id", "to_id", "confidence"),
		"middleware": object(map[string]any{
			"id":              stringType(),
			"group_var":       stringType(),
			"middleware_raw":  stringType(),
			"route_func":      stringType(),
			"statement_index": numberType(),
			"span":            ref("source_span"),
		}, "id", "group_var", "middleware_raw", "route_func", "statement_index", "span"),
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
		"module_impact": object(map[string]any{
			"module_path": stringType(),
			"basis":       stringType(),
			"symbol_id":   stringType(),
		}, "module_path", "basis"),
		"module_usage": object(map[string]any{
			"id":          stringType(),
			"module_path": stringType(),
			"import_path": stringType(),
			"alias":       stringType(),
			"basis":       stringType(),
			"symbol_id":   stringType(),
			"file":        stringType(),
			"confidence":  stringType(),
		}, "id", "module_path", "basis", "confidence"),
		"node": object(map[string]any{
			"id":     stringType(),
			"reason": stringType(),
			"span":   ref("source_span"),
		}, "id", "reason", "span"),
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
			"confidence":  stringType(),
			"span":        ref("source_span"),
		}, "id", "kind", "from_symbol", "confidence", "span"),
		"route": object(map[string]any{
			"id":              stringType(),
			"method":          stringType(),
			"local_path":      stringType(),
			"path_raw":        stringType(),
			"resolved_path":   stringType(),
			"group_var":       stringType(),
			"handler_raw":     stringType(),
			"handler_symbol":  stringType(),
			"wrappers":        arrayOf(ref("wrapper")),
			"route_func":      stringType(),
			"statement_index": numberType(),
			"source_family":   stringType(),
			"file":            stringType(),
			"span":            ref("source_span"),
		}, "id", "method", "local_path", "group_var", "handler_raw", "route_func", "statement_index", "file", "span"),
		"route_group": object(map[string]any{
			"id":               stringType(),
			"group_var":        stringType(),
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

func numberType() map[string]any {
	return map[string]any{"type": "integer"}
}

func boolType() map[string]any {
	return map[string]any{"type": "boolean"}
}
