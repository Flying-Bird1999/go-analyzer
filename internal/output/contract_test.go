// contract_test.go 校验 facts / impact JSON Schema 的有效性、字段边界与退役定义清理。
package output

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
)

// 场景：facts / impact schema 均为有效 JSON，且包含各自的核心顶层属性。
func TestSchemaDocumentsAreValidJSON(t *testing.T) {
	cases := []struct {
		name     string
		wantProp string
	}{
		{name: "facts", wantProp: "project"},
		{name: "impact", wantProp: "fileSources"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SchemaJSON(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := json.Unmarshal(got, &doc); err != nil {
				t.Fatal(err)
			}
			if doc["$schema"] == "" {
				t.Fatal("schema marker is empty")
			}
			properties, ok := doc["properties"].(map[string]any)
			if !ok {
				t.Fatalf("properties missing: %#v", doc)
			}
			if _, ok := properties[tc.wantProp]; !ok {
				t.Fatalf("property %q missing: %#v", tc.wantProp, properties)
			}
		})
	}
}

// 场景：两份 schema 均不再暴露已退役的历史 impact 定义（edge / node 等）。
func TestSchemasDoNotExposeRetiredImpactDefinitions(t *testing.T) {
	retired := []string{"edge", "endpoint_impact", "evidence_chain", "module_impact", "node"}
	for _, name := range []string{"facts", "impact"} {
		t.Run(name, func(t *testing.T) {
			got, err := SchemaJSON(name)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := json.Unmarshal(got, &doc); err != nil {
				t.Fatal(err)
			}
			defs, ok := doc["$defs"].(map[string]any)
			if !ok {
				t.Fatalf("$defs missing: %#v", doc)
			}
			for _, key := range retired {
				if _, ok := defs[key]; ok {
					t.Fatalf("retired definition %q is still exposed", key)
				}
			}
		})
	}
}

// 场景：每份 schema 只暴露与自身相关的 $defs，不串入对方专属定义。
func TestSchemasExposeOnlyRelevantDefinitions(t *testing.T) {
	cases := []struct {
		name   string
		absent []string
	}{
		{name: "facts", absent: []string{"endpoint_summary", "file_source_impact", "impact_diagnostic", "impact_node"}},
		{name: "impact", absent: []string{"annotation", "change", "link", "middleware", "module", "project", "reference", "route", "route_group", "symbol", "wrapper"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SchemaJSON(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := json.Unmarshal(got, &doc); err != nil {
				t.Fatal(err)
			}
			defs, ok := doc["$defs"].(map[string]any)
			if !ok {
				t.Fatalf("$defs missing: %#v", doc)
			}
			for _, key := range tc.absent {
				if _, ok := defs[key]; ok {
					t.Fatalf("definition %q should not be exposed by %s schema", key, tc.name)
				}
			}
		})
	}
}

// 场景：impact schema 暴露递归 impact_node 与 file_source_impact.symbols，无需顶层 nodes。
func TestImpactSchemaExposesRecursiveReviewableNodes(t *testing.T) {
	got, err := SchemaJSON("impact")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	properties := doc["properties"].(map[string]any)
	if _, ok := properties["summary"]; !ok {
		t.Fatalf("summary property missing: %#v", properties)
	}
	defs := doc["$defs"].(map[string]any)
	if _, ok := properties["nodes"]; ok {
		t.Fatalf("raw report should not expose top-level nodes: %#v", properties)
	}
	nodeProps := defs["impact_node"].(map[string]any)["properties"].(map[string]any)
	for _, required := range []string{"id", "kind", "file", "relation", "raw", "package", "level", "confidence", "cycle", "children", "method", "path"} {
		if _, ok := nodeProps[required]; !ok {
			t.Fatalf("reviewable node should expose %q: %#v", required, nodeProps)
		}
	}
	children := nodeProps["children"].(map[string]any)
	items := children["items"].(map[string]any)
	if items["$ref"] != "#/$defs/impact_node" {
		t.Fatalf("impact children should recurse: %#v", children)
	}
	fileProps := defs["file_source_impact"].(map[string]any)["properties"].(map[string]any)
	symbols := fileProps["symbols"].(map[string]any)
	if symbols["additionalProperties"].(map[string]any)["$ref"] != "#/$defs/impact_node" {
		t.Fatalf("file source symbols should contain impact trees: %#v", symbols)
	}
}

// 场景：impact schema 暴露 moduleSources 而非已退役的 module_changes / module_usages 事实。
func TestImpactSchemaExposesModuleSourcesInsteadOfModuleFacts(t *testing.T) {
	got, err := SchemaJSON("impact")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	properties := doc["properties"].(map[string]any)
	if _, ok := properties["moduleSources"]; !ok {
		t.Fatalf("moduleSources property missing: %#v", properties)
	}
	for _, retired := range []string{"module_changes", "module_usages"} {
		if _, ok := properties[retired]; ok {
			t.Fatalf("retired impact property %q remains: %#v", retired, properties)
		}
	}
	defs := doc["$defs"].(map[string]any)
	for _, required := range []string{"module_source_impact", "module_replacement"} {
		if _, ok := defs[required]; !ok {
			t.Fatalf("definition %q missing: %#v", required, defs)
		}
	}
	for _, retired := range []string{"module_change", "module_usage"} {
		if _, ok := defs[retired]; ok {
			t.Fatalf("retired impact definition %q remains", retired)
		}
	}
}

// 场景：impact schema 暴露 endpoint/gRPC source 摘要及其定义。
func TestImpactSchemaExposesEndpointAndGrpcSources(t *testing.T) {
	got, err := SchemaJSON("impact")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	properties := doc["properties"].(map[string]any)
	for _, field := range []string{"endpointSourcesSummary", "grpcSources"} {
		if _, ok := properties[field]; !ok {
			t.Fatalf("%s property missing: %#v", field, properties)
		}
	}
	requiredValues := doc["required"].([]any)
	required := make([]string, 0, len(requiredValues))
	for _, value := range requiredValues {
		required = append(required, value.(string))
	}
	for _, field := range []string{"endpointSourcesSummary", "grpcSources"} {
		if !slices.Contains(required, field) {
			t.Fatalf("%s missing from required: %#v", field, required)
		}
	}
	defs := doc["$defs"].(map[string]any)
	for _, name := range []string{"endpoint_source_summary", "endpoint_impact_source", "endpoint_root_symbol_summary", "grpc_source_impact", "grpc_operation_summary", "grpc_consumer_impact"} {
		if _, ok := defs[name]; !ok {
			t.Fatalf("%s definition missing: %#v", name, defs)
		}
	}
}

// 场景：impact schema 的 summary 与 file_source_impact 均暴露 IM 事件摘要字段。
func TestImpactSchemaExposesIMEventSummaries(t *testing.T) {
	got, err := SchemaJSON("impact")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	defs := doc["$defs"].(map[string]any)
	summary := defs["impact_summary"].(map[string]any)
	summaryProperties := summary["properties"].(map[string]any)
	for _, field := range []string{"impactedIMCount", "impactedIMEvents"} {
		if _, ok := summaryProperties[field]; !ok {
			t.Fatalf("summary field %q missing: %#v", field, summaryProperties)
		}
	}
	fileSource := defs["file_source_impact"].(map[string]any)
	sourceProperties := fileSource["properties"].(map[string]any)
	if _, ok := sourceProperties["impactedIMEvents"]; !ok {
		t.Fatalf("source impactedIMEvents missing: %#v", sourceProperties)
	}
}

// 场景：facts schema 不暴露 diff-only 的瞬态事实（changes / module_changes / module_usages 等）。
func TestFactsSchemaOmitsDiffOnlyTransientFacts(t *testing.T) {
	got, err := SchemaJSON("facts")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	properties := doc["properties"].(map[string]any)
	for _, field := range []string{"changes", "module_changes", "module_usages"} {
		if _, ok := properties[field]; ok {
			t.Fatalf("transient facts property %q remains: %#v", field, properties)
		}
	}
	defs := doc["$defs"].(map[string]any)
	for _, name := range []string{"change", "change_range", "module_change", "module_usage"} {
		if _, ok := defs[name]; ok {
			t.Fatalf("transient facts definition %q remains", name)
		}
	}
}

// 场景：facts schema 暴露 im_events 属性及其依赖/证据 $defs。
func TestFactsSchemaExposesIMEvents(t *testing.T) {
	got, err := SchemaJSON("facts")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	properties := doc["properties"].(map[string]any)
	if _, ok := properties["im_events"]; !ok {
		t.Fatalf("im_events property missing: %#v", properties)
	}
	defs := doc["$defs"].(map[string]any)
	for _, name := range []string{"im_event", "im_event_dependency", "im_event_evidence"} {
		if _, ok := defs[name]; !ok {
			t.Fatalf("definition %q missing: %#v", name, defs)
		}
	}
}

// 场景：impact schema 不暴露 meta / impact_meta 等版本元数据。
func TestImpactSchemaDoesNotExposeSchemaVersion(t *testing.T) {
	got, err := SchemaJSON("impact")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	properties := doc["properties"].(map[string]any)
	if _, ok := properties["meta"]; ok {
		t.Fatalf("impact should not expose meta: %#v", properties)
	}
	if _, ok := doc["$defs"].(map[string]any)["impact_meta"]; ok {
		t.Fatal("impact_meta definition should be removed")
	}
}

// 场景：impact schema 不暴露 span / source_span 等调试证据。
func TestImpactSchemaDoesNotExposeSpanOrDebugEvidence(t *testing.T) {
	got, err := SchemaJSON("impact")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"span"`, `"source_span"`} {
		if bytes.Contains(got, []byte(forbidden)) {
			t.Fatalf("impact schema should not expose %s: %s", forbidden, got)
		}
	}
}

// 场景：SchemaJSON 对未知 schema 名称返回错误。
func TestSchemaJSONRejectsUnknownType(t *testing.T) {
	_, err := SchemaJSON("unknown")
	if err == nil {
		t.Fatal("expected unknown schema type to fail")
	}
}
