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
		{name: "impact", wantProp: "nodes"},
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
		{name: "facts", absent: []string{"endpoint_summary", "file_source_impact", "impact_diagnostic", "impact_edge", "impact_node", "impact_root"}},
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
	for _, definition := range []string{"impact_root", "impact_edge"} {
		item := defs[definition].(map[string]any)
		itemProps := item["properties"].(map[string]any)
		confidence := itemProps["confidence"].(map[string]any)
		enum, ok := confidence["enum"].([]any)
		if !ok || len(enum) != 2 || enum[0] != "medium" || enum[1] != "low" {
			t.Fatalf("%s confidence enum missing: %#v", definition, confidence)
		}
	}
	nodeProps := defs["impact_node"].(map[string]any)["properties"].(map[string]any)
	for _, forbidden := range []string{"id", "span", "raw", "package", "level", "confidence", "cycle"} {
		if _, ok := nodeProps[forbidden]; ok {
			t.Fatalf("compact node should not expose %q: %#v", forbidden, nodeProps)
		}
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
	for _, forbidden := range []string{`"span"`, `"raw"`, `"package"`, `"level"`, `"source_span"`} {
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
