// interface_bindings_test.go 测试接口变量严格证据绑定的接受与拒绝场景。
package astindex

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// TestInterfaceBindingResolvesUniqueConcreteAssignment 验证：唯一具体类型赋值可解析到该具体方法，且置信度为 high。
func TestInterfaceBindingResolvesUniqueConcreteAssignment(t *testing.T) {
	idx, file := buildInterfaceBindingFixture(t, `package binding

type Client interface {
	Fetch()
}

type realClient struct{}

func (*realClient) Fetch() {}

var Default Client

func Init() {
	Default = new(realClient)
}
`)

	resolved, ok := idx.ResolveSelectorMethod(file, []string{"Default", "Fetch"})
	if !ok {
		t.Fatal("Default.Fetch was not resolved")
	}
	want := facts.SymbolID("method:example.com/interface-binding:realClient:Fetch")
	if resolved.ID != want {
		t.Fatalf("Default.Fetch = %#v, want %s", resolved, want)
	}
}

// TestInterfaceBindingResolvesCompositeLiteralAssignmentInClosure 验证：闭包内的组合字面量赋值也算作绑定证据。
func TestInterfaceBindingResolvesCompositeLiteralAssignmentInClosure(t *testing.T) {
	idx, file := buildInterfaceBindingFixture(t, `package binding

type Client interface {
	Fetch()
}

type realClient struct{}

func (*realClient) Fetch() {}

var Default Client

func Register(starter func() error) {}

func Init() {
	Register(func() error {
		Default = &realClient{}
		return nil
	})
}
`)

	resolved, ok := idx.ResolveSelectorMethod(file, []string{"Default", "Fetch"})
	if !ok {
		t.Fatal("Default.Fetch was not resolved through composite literal closure assignment")
	}
	want := facts.SymbolID("method:example.com/interface-binding:realClient:Fetch")
	if resolved.ID != want {
		t.Fatalf("Default.Fetch = %#v, want %s", resolved, want)
	}
}

// TestInterfaceBindingRejectsUnknownAssignment 验证：出现返回接口类型的 constructor 赋值时，绑定被拒绝。
func TestInterfaceBindingRejectsUnknownAssignment(t *testing.T) {
	idx, file := buildInterfaceBindingFixture(t, `package binding

type Client interface {
	Fetch()
}

type realClient struct{}

func (*realClient) Fetch() {}

var Default Client

func buildClient() Client {
	return new(realClient)
}

func Init() {
	Default = new(realClient)
	Default = buildClient()
}
`)

	if resolved, ok := idx.ResolveSelectorMethod(file, []string{"Default", "Fetch"}); ok {
		t.Fatalf("Default.Fetch unexpectedly resolved through unknown assignment: %#v", resolved)
	}
}

// TestInterfaceBindingRejectsMultipleConcreteAssignments 验证：多实现赋值时绑定被拒绝，不猜测具体方法。
func TestInterfaceBindingRejectsMultipleConcreteAssignments(t *testing.T) {
	idx, file := buildInterfaceBindingFixture(t, `package binding

type Client interface {
	Fetch()
}

type realClient struct{}
func (*realClient) Fetch() {}

type otherClient struct{}
func (*otherClient) Fetch() {}

var Default Client

func Init(useOther bool) {
	if useOther {
		Default = new(otherClient)
		return
	}
	Default = new(realClient)
}
`)

	if resolved, ok := idx.ResolveSelectorMethod(file, []string{"Default", "Fetch"}); ok {
		t.Fatalf("Default.Fetch unexpectedly resolved through multiple implementations: %#v", resolved)
	}
}

// TestInterfaceBindingIgnoresShadowingAssignments 验证：局部变量与参数对同名包级变量的遮蔽不会污染包级变量的绑定。
func TestInterfaceBindingIgnoresShadowingAssignments(t *testing.T) {
	idx, file := buildInterfaceBindingFixture(t, `package binding

type Client interface {
	Fetch()
}

type realClient struct{}
func (*realClient) Fetch() {}

type otherClient struct{}
func (*otherClient) Fetch() {}

var Default Client

func Init() {
	Default = new(realClient)
}

func localShadow() {
	Default := new(otherClient)
	Default.Fetch()
}

func parameterShadow(Default Client) {
	Default = new(otherClient)
}
`)

	resolved, ok := idx.ResolveSelectorMethod(file, []string{"Default", "Fetch"})
	if !ok {
		t.Fatal("Default.Fetch was not resolved after ignoring shadowing assignments")
	}
	want := facts.SymbolID("method:example.com/interface-binding:realClient:Fetch")
	if resolved.ID != want {
		t.Fatalf("Default.Fetch = %s, want %s", resolved.ID, want)
	}
}

// TestInterfaceBindingRejectsCrossFileNewShadow 验证：跨文件定义的 new 函数会遮蔽内建 new，使 new(realClient) 不再被当作 builtin 构造。
func TestInterfaceBindingRejectsCrossFileNewShadow(t *testing.T) {
	idx, file := buildInterfaceBindingFiles(t, map[string]string{
		"binding.go": `package binding

type Client interface {
	Fetch()
}

type realClient struct{}
func (*realClient) Fetch() {}

type otherClient struct{}
func (*otherClient) Fetch() {}

var Default Client

func Init() {
	Default = new(realClient)
}
`,
		"new.go": `package binding

func new(value any) Client {
	return newOtherClient()
}

func newOtherClient() Client {
	return &otherClient{}
}
`,
	})

	if resolved, ok := idx.ResolveSelectorMethod(file, []string{"Default", "Fetch"}); ok {
		t.Fatalf("Default.Fetch unexpectedly treated shadowed new as builtin: %#v", resolved)
	}
}

// buildInterfaceBindingFixture 用单文件源码构造接口绑定测试 fixture。
func buildInterfaceBindingFixture(t *testing.T, source string) (*Index, *project.File) {
	t.Helper()
	return buildInterfaceBindingFiles(t, map[string]string{"binding.go": source})
}

// buildInterfaceBindingFiles 用多文件源码构造接口绑定测试 fixture，并返回索引与 binding.go 对应文件。
func buildInterfaceBindingFiles(t *testing.T, sources map[string]string) (*Index, *project.File) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/interface-binding\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, source := range sources {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	pkg := p.Packages[p.ModulePath]
	if pkg == nil || len(pkg.Files) != len(sources) {
		t.Fatalf("fixture files = %#v", pkg)
	}
	for _, file := range pkg.Files {
		if filepath.Base(file.Path) == "binding.go" {
			return idx, file
		}
	}
	t.Fatal("binding.go was not loaded")
	return nil, nil
}
