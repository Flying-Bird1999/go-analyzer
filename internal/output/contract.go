package output

import (
	"encoding/json"
	"fmt"
)

var schemaDocuments = map[string]map[string]any{
	"facts": {
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://gopkg.inshopline.com/bff/go-analyzer/schemas/facts.v1alpha1.schema.json",
		"title":                "go-analyzer facts output",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"project", "symbols", "annotations", "route_groups", "routes", "middleware", "references", "modules", "im_events", "links", "diagnostics"},
		"properties": map[string]any{
			"project":      ref("project"),
			"symbols":      arrayOf(ref("symbol")),
			"annotations":  arrayOf(ref("annotation")),
			"route_groups": arrayOf(ref("route_group")),
			"routes":       arrayOf(ref("route")),
			"middleware":   arrayOf(ref("middleware")),
			"references":   arrayOf(ref("reference")),
			"modules":      arrayOf(ref("module")),
			"im_events":    arrayOf(ref("im_event")),
			"links":        arrayOf(ref("link")),
			"diagnostics":  arrayOf(ref("diagnostic")),
		},
		"$defs": factsDefinitions(),
	},
	"impact": {
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://gopkg.inshopline.com/bff/go-analyzer/schemas/go-impact.v1alpha1.schema.json",
		"title":                "go-analyzer reviewable impact tree",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"summary", "fileSources"},
		"properties": map[string]any{
			"summary":       ref("impact_summary"),
			"fileSources":   arrayOf(ref("file_source_impact")),
			"moduleSources": arrayOf(ref("module_source_impact")),
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
		"diagnostic",
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

func impactDefinitions() map[string]any {
	return selectDefinitions(
		"endpoint_summary",
		"file_source_impact",
		"impact_node",
		"impact_summary",
		"module_replacement",
		"module_source_impact",
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
		"impact_summary": object(map[string]any{
			"impactedEndpointCount": numberType(),
			"impactedEndpoints":     arrayOf(ref("endpoint_summary")),
		}, "impactedEndpointCount", "impactedEndpoints"),
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
		}, "id", "kind", "level", "children"),
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
