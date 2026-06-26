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
		{name: "impact", wantProp: "impacted_endpoints"},
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

func TestSchemaJSONRejectsUnknownType(t *testing.T) {
	_, err := SchemaJSON("unknown")
	if err == nil {
		t.Fatal("expected unknown schema type to fail")
	}
}
