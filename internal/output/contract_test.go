package output

import (
	"bytes"
	"encoding/json"
	"testing"
)

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

func TestSchemaJSONRejectsUnknownType(t *testing.T) {
	_, err := SchemaJSON("unknown")
	if err == nil {
		t.Fatal("expected unknown schema type to fail")
	}
}
