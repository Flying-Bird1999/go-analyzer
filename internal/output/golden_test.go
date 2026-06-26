package output_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/app"
)

func TestMiniBFFGolden(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "mini-bff")
	got, err := app.RunFacts(app.Options{ProjectPath: root, Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	got = normalizeMiniBFFGolden(t, got)
	goldenPath := filepath.Join("..", "..", "testdata", "golden", "mini-bff.facts.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch for %s; run UPDATE_GOLDEN=1 go test ./internal/output -run TestMiniBFFGolden", goldenPath)
	}
}

func normalizeMiniBFFGolden(t *testing.T, input []byte) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(input, &doc); err != nil {
		t.Fatal(err)
	}
	project, ok := doc["project"].(map[string]any)
	if !ok {
		t.Fatal("project object missing")
	}
	project["root"] = "testdata/fixtures/mini-bff"
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}
