// json_test.go 校验 RenderJSON 的确定性、稳定排序与 IM 事件输出。
package output

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"sort"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// 场景：同一 Store 两次渲染 JSON 字节级一致，且 symbols 按 ID 字典序排序。
func TestRenderJSONIsDeterministicAndSorted(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "mini-bff")
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

	first, err := RenderJSON(store)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderJSON(store)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("json output is not deterministic")
	}

	var doc Document
	if err := json.Unmarshal(first, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Project.ModulePath != "example.com/mini-bff" {
		t.Fatalf("module path = %q", doc.Project.ModulePath)
	}
	if len(doc.Symbols) == 0 {
		t.Fatal("expected symbols in json output")
	}
	ids := make([]string, 0, len(doc.Symbols))
	for _, symbol := range doc.Symbols {
		ids = append(ids, string(symbol.ID))
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("symbols are not sorted: %v", ids)
	}
}

// 场景：references 与 links 按 ID 字典序排序，与输入顺序无关。
func TestRenderJSONSortsReferencesAndLinks(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.References = append(store.References,
		facts.ReferenceFact{ID: "ref:b", FromSymbol: "func:example.com/project::B", ToSymbol: "func:example.com/project::A"},
		facts.ReferenceFact{ID: "ref:a", FromSymbol: "func:example.com/project::A", ToSymbol: "func:example.com/project::B"},
	)
	store.Links = append(store.Links,
		facts.LinkFact{ID: "link:b", FromID: "route:b", ToID: "func:b"},
		facts.LinkFact{ID: "link:a", FromID: "route:a", ToID: "func:a"},
	)

	out, err := RenderJSON(store)
	if err != nil {
		t.Fatal(err)
	}

	var doc Document
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if got := doc.References[0].ID; got != "ref:a" {
		t.Fatalf("first reference id = %q", got)
	}
	if got := doc.Links[0].ID; got != "link:a" {
		t.Fatalf("first link id = %q", got)
	}
}

// 场景：im_events 按 ID 字典序排序输出。
func TestRenderJSONIncludesSortedIMEvents(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	store.IMEvents = append(store.IMEvents,
		facts.IMEventFact{ID: "im_event:z", Event: "z", Resolved: true},
		facts.IMEventFact{ID: "im_event:a", Event: "a", Resolved: true},
	)

	out, err := RenderJSON(store)
	if err != nil {
		t.Fatal(err)
	}

	var doc Document
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.IMEvents) != 2 || doc.IMEvents[0].Event != "a" || doc.IMEvents[1].Event != "z" {
		t.Fatalf("im events = %#v", doc.IMEvents)
	}
}
