package link

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	annotationextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/annotation"
	routeextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/route"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func TestResolveFunctionHandlerSymbol(t *testing.T) {
	p, idx, store := loadAndExtract(t, filepath.Join("..", "..", "testdata", "fixtures", "controller-wrapper"))
	_ = p
	got, ok := ResolveHandlerSymbol(idx, store.Routes[0])
	if !ok {
		t.Fatal("handler did not resolve")
	}
	want := facts.SymbolID("func:example.com/controller-wrapper/controller::CheckIn")
	if got != want {
		t.Fatalf("handler symbol = %q, want %q", got, want)
	}
}

func TestResolvePackageVarMethodHandlerSymbol(t *testing.T) {
	_, idx, store := loadAndExtract(t, filepath.Join("..", "..", "testdata", "fixtures", "handler-method-var"))
	got, ok := ResolveHandlerSymbol(idx, store.Routes[0])
	if !ok {
		t.Fatal("handler did not resolve")
	}
	want := facts.SymbolID("method:example.com/handler-method-var/controller/uc:merchantSettingApi:UpdateSubMerchantSettingByCode")
	if got != want {
		t.Fatalf("handler symbol = %q, want %q", got, want)
	}
}

func TestRunLinksRouteHandlerAndAnnotation(t *testing.T) {
	_, idx, store := loadAndExtract(t, filepath.Join("..", "..", "testdata", "fixtures", "route-annotation-link"))

	if err := Run(idx, store); err != nil {
		t.Fatal(err)
	}

	if got := store.Routes[0].HandlerSymbol; got != "func:example.com/route-annotation-link/controller::CheckIn" {
		t.Fatalf("route handler symbol = %q", got)
	}
	if len(store.Links) != 2 {
		t.Fatalf("links = %d: %#v", len(store.Links), store.Links)
	}
	kinds := map[facts.LinkKind]bool{}
	for _, link := range store.Links {
		kinds[link.Kind] = true
	}
	if !kinds[facts.LinkKindRouteToHandler] {
		t.Fatal("missing route_to_handler link")
	}
	if !kinds[facts.LinkKindHandlerToAnnotation] {
		t.Fatal("missing handler_to_annotation link")
	}
}

func TestRunLinksMiddlewareSymbols(t *testing.T) {
	_, idx, store := loadAndExtract(t, filepath.Join("..", "..", "testdata", "fixtures", "middleware-order"))

	if err := Run(idx, store); err != nil {
		t.Fatal(err)
	}

	assertMiddlewareSymbol(t, store, "Auth()", "func:example.com/middleware-order/router::Auth")
	assertMiddlewareSymbol(t, store, "h1", "func:example.com/middleware-order/router::h1")
	assertMiddlewareSymbol(t, store, "Audit()", "func:example.com/middleware-order/router::Audit")
}

func TestRunLinksConstructorInitializedPackageVarMiddlewareSymbol(t *testing.T) {
	root := t.TempDir()
	writeLinkTestFile(t, root, "go.mod", "module example.com/constructor-middleware\n\ngo 1.24\n")
	writeLinkTestFile(t, root, "auth/auth.go", `package auth

var Default = NewAuth()

type Auth struct{}

func NewAuth() *Auth {
	return &Auth{}
}

func (a *Auth) Middleware() {}
`)
	writeLinkTestFile(t, root, "router/router.go", `package router

import auth "example.com/constructor-middleware/auth"

type RouterGroup struct{}

func (g *RouterGroup) Use(middleware any) {}
func (g *RouterGroup) GET(path string, handler any) {}

func Handler() {}

func InitRouter(g *RouterGroup) {
	g.Use(auth.Default.Middleware)
	g.GET("/x", Handler)
}
`)
	_, idx, store := loadAndExtract(t, root)

	if err := Run(idx, store); err != nil {
		t.Fatal(err)
	}

	assertMiddlewareSymbol(t, store, "auth.Default.Middleware", "method:example.com/constructor-middleware/auth:Auth:Middleware")
}

func assertMiddlewareSymbol(t *testing.T, store *facts.Store, raw string, want facts.SymbolID) {
	t.Helper()
	for _, binding := range store.Middleware {
		if binding.MiddlewareRaw != raw {
			continue
		}
		for _, symbol := range binding.MiddlewareSymbols {
			if symbol == want {
				return
			}
		}
		t.Fatalf("middleware %q symbols = %#v, want %q", raw, binding.MiddlewareSymbols, want)
	}
	t.Fatalf("middleware %q not found: %#v", raw, store.Middleware)
}

func loadAndExtract(t *testing.T, root string) (*project.Project, *astindex.Index, *facts.Store) {
	t.Helper()
	p, err := project.Load(root, project.Options{})
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
	if err := routeextract.Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}
	return p, idx, store
}

func writeLinkTestFile(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
