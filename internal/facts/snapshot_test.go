package facts

import "testing"

func TestSnapshotIsIsolatedFromBuilderAndReaders(t *testing.T) {
	builder := NewBuilder("/project", "example.com/project")
	store := builder.MutableStore()
	store.Symbols = append(store.Symbols, SymbolFact{ID: "func:example.com/project::Handler", Name: "Handler"})
	store.Diagnostics = append(store.Diagnostics, DiagnosticFact{ID: "diagnostic:one", RelatedFactIDs: []string{"fact:one"}})

	snapshot := builder.Freeze()
	store.Symbols[0].Name = "mutated-builder"
	store.Diagnostics[0].RelatedFactIDs[0] = "mutated-builder"

	first := snapshot.Store()
	if first.Symbols[0].Name != "Handler" || first.Diagnostics[0].RelatedFactIDs[0] != "fact:one" {
		t.Fatalf("snapshot changed with builder: %#v", first)
	}
	first.Symbols[0].Name = "mutated-reader"
	first.Diagnostics[0].RelatedFactIDs[0] = "mutated-reader"

	second := snapshot.Store()
	if second.Symbols[0].Name != "Handler" || second.Diagnostics[0].RelatedFactIDs[0] != "fact:one" {
		t.Fatalf("snapshot changed with reader: %#v", second)
	}
}

func TestSnapshotFocusedAccessorsAreDefensive(t *testing.T) {
	store := NewStore("/project", "example.com/project")
	store.Routes = []RouteRegistrationFact{{
		ID:       "route:orders",
		Method:   "GET",
		Wrappers: []WrapperFact{{Name: "ControllerWithResp"}},
	}}
	store.Middleware = []MiddlewareBindingFact{{
		ID:                "middleware:auth",
		MiddlewareSymbols: []SymbolID{"func:example.com/project::Auth"},
	}}
	snapshot := Freeze(store)

	routes := snapshot.Routes()
	routes[0].Wrappers[0].Name = "mutated-route-reader"
	middleware := snapshot.MiddlewareBindings()
	middleware[0].MiddlewareSymbols[0] = "mutated-middleware-reader"

	if got := snapshot.Routes()[0].Wrappers[0].Name; got != "ControllerWithResp" {
		t.Fatalf("route accessor leaked mutation: %q", got)
	}
	if got := snapshot.MiddlewareBindings()[0].MiddlewareSymbols[0]; got != "func:example.com/project::Auth" {
		t.Fatalf("middleware accessor leaked mutation: %q", got)
	}
}
