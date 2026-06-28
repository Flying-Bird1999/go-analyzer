package output

import (
	"encoding/json"
	"testing"
)

func TestSchemaDocumentsAreValidJSON(t *testing.T) {
	cases := []struct {
		name     string
		wantProp string
	}{
		{name: "facts", wantProp: "project"},
		{name: "impact", wantProp: "meta"},
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
		{name: "facts", absent: []string{"endpoint_summary", "file_source_impact", "impact_meta", "impact_node"}},
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

func TestSchemasConstrainConfidenceAndExposeImpactSummary(t *testing.T) {
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
	impactNode := defs["impact_node"].(map[string]any)
	nodeProps := impactNode["properties"].(map[string]any)
	confidence := nodeProps["confidence"].(map[string]any)
	enum, ok := confidence["enum"].([]any)
	if !ok || len(enum) != 3 {
		t.Fatalf("confidence enum missing: %#v", confidence)
	}
}

func TestSchemaJSONRejectsUnknownType(t *testing.T) {
	_, err := SchemaJSON("unknown")
	if err == nil {
		t.Fatal("expected unknown schema type to fail")
	}
}
