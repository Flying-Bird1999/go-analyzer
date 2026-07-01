package im

import (
	"go/ast"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func TestEvaluatorResolvesStaticEvents(t *testing.T) {
	p, idx, file := loadEvaluatorProject(t, `package sample

type Event string

const (
	Inbox Event = "inbox_msg"
	Prefix = "POST"
	Product = Prefix + "/PRODUCT_CHANGE"
)

var literal = "direct"
var typed = Inbox
var concatenated = Product
var converted = string(Inbox)
var unknown = runtimeEvent()

func runtimeEvent() string { return "runtime" }
`)
	eval := newEvaluator(p, idx)

	tests := []struct {
		name  string
		value string
		ok    bool
	}{
		{name: "literal", value: "direct", ok: true},
		{name: "typed", value: "inbox_msg", ok: true},
		{name: "concatenated", value: "POST/PRODUCT_CHANGE", ok: true},
		{name: "converted", value: "inbox_msg", ok: true},
		{name: "unknown", ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expr := packageValueExpr(t, file, tc.name)
			got, ok := eval.eventValue(file, expr)
			if ok != tc.ok || got != tc.value {
				t.Fatalf("eventValue(%s) = %q, %v; want %q, %v", tc.name, got, ok, tc.value, tc.ok)
			}
		})
	}
}

func TestEvaluatorResolvesIotaStringTable(t *testing.T) {
	p, idx, file := loadEvaluatorProject(t, `package sample

type EventCode int

const (
	LockInventory EventCode = iota
	Conversation
)

var eventNames = [...]string{
	"LOCK_INVENTORY_UPDATE",
	"CONVERSATION_UPDATE",
}

func (e EventCode) String() string { return eventNames[e] }

var lock = LockInventory.String()
var conversation = Conversation.String()
`)
	eval := newEvaluator(p, idx)

	for name, want := range map[string]string{
		"lock":         "LOCK_INVENTORY_UPDATE",
		"conversation": "CONVERSATION_UPDATE",
	} {
		got, ok := eval.eventValue(file, packageValueExpr(t, file, name))
		if !ok || got != want {
			t.Fatalf("eventValue(%s) = %q, %v; want %q", name, got, ok, want)
		}
	}
}

func TestEvaluatorResolvesStringMethodDeclaredBeforeTable(t *testing.T) {
	p, idx, file := loadEvaluatorProject(t, `package sample

type EventCode int

func (e EventCode) String() string { return eventNames[e] }

const (
	LockInventory EventCode = iota
	Conversation
)

var eventNames = [...]string{
	"LOCK_INVENTORY_UPDATE",
	"CONVERSATION_UPDATE",
}

var lock = LockInventory.String()
`)
	eval := newEvaluator(p, idx)

	got, ok := eval.eventValue(file, packageValueExpr(t, file, "lock"))
	if !ok || got != "LOCK_INVENTORY_UPDATE" {
		t.Fatalf("eventValue(lock) = %q, %v; want %q", got, ok, "LOCK_INVENTORY_UPDATE")
	}
}

func TestEvaluatorResolvesSelectorPayloadType(t *testing.T) {
	p, idx, file := loadEvaluatorProject(t, `package sample

type Message struct{ ID string }
type Conversation struct{ ID string }
type Envelope struct {
	MsgInfo *Message
	ConvInfo *Conversation
}

func use(event Envelope) {
	_ = event.MsgInfo
	_ = event.ConvInfo
}
`)
	eval := newEvaluator(p, idx)
	fn := functionDecl(t, file, "use")
	selectors := selectorExpressions(fn)
	if len(selectors) != 2 {
		t.Fatalf("selectors = %d", len(selectors))
	}

	gotMessage := eval.expressionTypeIDs(file, fn, selectors[0])
	wantMessage := []facts.SymbolID{astindex.TypeSymbolID("example.com/im-evaluator", "Message")}
	if !reflect.DeepEqual(gotMessage, wantMessage) {
		t.Fatalf("message types = %#v; want %#v", gotMessage, wantMessage)
	}
	gotConversation := eval.expressionTypeIDs(file, fn, selectors[1])
	wantConversation := []facts.SymbolID{astindex.TypeSymbolID("example.com/im-evaluator", "Conversation")}
	if !reflect.DeepEqual(gotConversation, wantConversation) {
		t.Fatalf("conversation types = %#v; want %#v", gotConversation, wantConversation)
	}
}

func loadEvaluatorProject(t *testing.T, source string) (*project.Project, *astindex.Index, *project.File) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/im-evaluator\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	pkg := p.Packages["example.com/im-evaluator"]
	if pkg == nil || len(pkg.Files) != 1 {
		t.Fatalf("package files = %#v", pkg)
	}
	return p, idx, pkg.Files[0]
}

func packageValueExpr(t *testing.T, file *project.File, name string) ast.Expr {
	t.Helper()
	for _, decl := range file.AST.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, rawSpec := range gen.Specs {
			spec, ok := rawSpec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, ident := range spec.Names {
				if ident.Name != name || len(spec.Values) == 0 {
					continue
				}
				valueIndex := i
				if valueIndex >= len(spec.Values) {
					valueIndex = len(spec.Values) - 1
				}
				return spec.Values[valueIndex]
			}
		}
	}
	t.Fatalf("package value %q not found", name)
	return nil
}

func functionDecl(t *testing.T, file *project.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range file.AST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("function %q not found", name)
	return nil
}

func selectorExpressions(fn *ast.FuncDecl) []*ast.SelectorExpr {
	var out []*ast.SelectorExpr
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if selector, ok := node.(*ast.SelectorExpr); ok {
			out = append(out, selector)
			return false
		}
		return true
	})
	return out
}
