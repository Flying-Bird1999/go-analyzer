package astindex

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

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

	resolved, ok := idx.ResolveSelectorMethodWithConfidence(file, []string{"Default", "Fetch"})
	if !ok {
		t.Fatal("Default.Fetch was not resolved")
	}
	want := facts.SymbolID("method:example.com/interface-binding:realClient:Fetch")
	if resolved.ID != want || resolved.Confidence != facts.ConfidenceHigh {
		t.Fatalf("Default.Fetch = %#v, want %s with high confidence", resolved, want)
	}
}

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

	resolved, ok := idx.ResolveSelectorMethodWithConfidence(file, []string{"Default", "Fetch"})
	if !ok {
		t.Fatal("Default.Fetch was not resolved through composite literal closure assignment")
	}
	want := facts.SymbolID("method:example.com/interface-binding:realClient:Fetch")
	if resolved.ID != want || resolved.Confidence != facts.ConfidenceHigh {
		t.Fatalf("Default.Fetch = %#v, want %s with high confidence", resolved, want)
	}
}

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

	if resolved, ok := idx.ResolveSelectorMethodWithConfidence(file, []string{"Default", "Fetch"}); ok {
		t.Fatalf("Default.Fetch unexpectedly resolved through unknown assignment: %#v", resolved)
	}
}

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

	if resolved, ok := idx.ResolveSelectorMethodWithConfidence(file, []string{"Default", "Fetch"}); ok {
		t.Fatalf("Default.Fetch unexpectedly resolved through multiple implementations: %#v", resolved)
	}
}

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

	resolved, ok := idx.ResolveSelectorMethodWithConfidence(file, []string{"Default", "Fetch"})
	if !ok {
		t.Fatal("Default.Fetch was not resolved after ignoring shadowing assignments")
	}
	want := facts.SymbolID("method:example.com/interface-binding:realClient:Fetch")
	if resolved.ID != want {
		t.Fatalf("Default.Fetch = %s, want %s", resolved.ID, want)
	}
}

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

	if resolved, ok := idx.ResolveSelectorMethodWithConfidence(file, []string{"Default", "Fetch"}); ok {
		t.Fatalf("Default.Fetch unexpectedly treated shadowed new as builtin: %#v", resolved)
	}
}

func buildInterfaceBindingFixture(t *testing.T, source string) (*Index, *project.File) {
	t.Helper()
	return buildInterfaceBindingFiles(t, map[string]string{"binding.go": source})
}

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
