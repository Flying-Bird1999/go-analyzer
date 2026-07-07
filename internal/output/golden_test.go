// golden_test.go 通过 mini-bff 与 type-impact 两个黄金样本，端到端锁定 facts 与 impact 的稳定 JSON 输出。
package output_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/app"
	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	annotationextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/annotation"
	referenceextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/reference"
	routeextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/route"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/impact"
	"gopkg.inshopline.com/bff/go-analyzer/internal/link"
	"gopkg.inshopline.com/bff/go-analyzer/internal/output"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// 场景：mini-bff facts 输出与 golden 样本字节级一致（root 与 build context 做环境归一化）。
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

// normalizeMiniBFFGolden 把 project.root 与 build_context 归一化为稳定值，
// 消除测试机环境差异，使 golden 比较只关注 facts 结构而非绝对路径/平台。
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
	project["build_context"] = map[string]any{
		"goos":        "normalized",
		"goarch":      "normalized",
		"tags":        []any{},
		"cgo_enabled": false,
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

// 场景：type-impact 的 struct 字段 tag 变更经完整传播树到端点，impact 输出与 golden 字节级一致。
func TestTypeImpactTreeGolden(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "type-impact")
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	store := facts.NewStore(p.Root, p.ModulePath)
	for _, symbol := range idx.Symbols {
		store.AddSymbol(symbol)
	}
	if err := annotationextract.Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}
	if err := referenceextract.Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}
	if err := routeextract.Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}
	if err := link.Run(idx, store); err != nil {
		t.Fatal(err)
	}

	patch := []byte("diff --git a/model/model.go b/model/model.go\n" +
		"--- a/model/model.go\n" +
		"+++ b/model/model.go\n" +
		"@@ -1,5 +1,5 @@\n" +
		" package model\n" +
		" \n" +
		" type Address struct {\n" +
		"-\tCity string `json:\"city_name\"`\n" +
		"+\tCity string `json:\"city\"`\n" +
		" }\n")
	fileChanges, err := diff.ParseUnified(patch)
	if err != nil {
		t.Fatal(err)
	}
	store.Changes = diff.MapChanges(fileChanges, store, "git_diff")
	result := impact.AnalyzeTrees(store)
	doc := output.BuildImpactDocument(fileChanges, result, output.ImpactDocumentOptions{})
	got, err := output.RenderImpactTreeJSON(doc)
	if err != nil {
		t.Fatal(err)
	}

	goldenPath := filepath.Join("..", "..", "testdata", "golden", "type-impact.impact.json")
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
		t.Fatalf("golden mismatch for %s; run UPDATE_GOLDEN=1 go test ./internal/output -run TestTypeImpactTreeGolden", goldenPath)
	}
}
