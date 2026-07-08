# Middleware Symbol Index Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `RouteGraph` middleware-symbol index and make impact propagation query the graph instead of scanning `facts.Store.Middleware`.

**Architecture:** `RouteGraph` owns route-domain query views. `NewRouteGraph` builds `MiddlewareBySymbol` alongside existing middleware indexes, and `treeBuilder` delegates middleware symbol lookup to the graph.

**Tech Stack:** Go, existing `internal/graph`, `internal/impact`, and `internal/facts` packages.

## Global Constraints

- Do not change middleware impact semantics.
- Do not change `MiddlewareBindingFact`.
- Do not change route group, middleware ordering, descendant group, or cross-function group flow behavior.
- Do not change public JSON output contracts.
- Do not introduce concurrency or new dependencies.
- Keep documentation paths relative; do not write absolute workspace paths into project docs.

---

## File Structure

- Modify `internal/graph/route.go`: add `MiddlewareBySymbol`, build it, sort it, and expose a copy-returning query.
- Modify `internal/graph/graph_test.go`: add a focused graph test for indexing, sorting, empty symbol filtering, and copy semantics.
- Modify `internal/impact/tree_builder.go`: replace Store scan with graph query.
- Modify `ARCHITECTURE.md`: clarify that `RouteGraph` indexes middleware by binding ID and middleware symbol.

---

### Task 1: Add Failing Graph Index Test

**Files:**
- Modify: `internal/graph/graph_test.go`

**Interfaces:**
- Consumes:
  - `func NewRouteGraph(store *facts.Store) *RouteGraph`
- Produces failing expectations for:
  - `func (g *RouteGraph) MiddlewareBindingsForSymbol(symbol facts.SymbolID) []facts.MiddlewareBindingFact`

- [ ] **Step 1: Confirm clean worktree**

Run:

```bash
git status --short
```

Expected: no output, or only this plan file before it is committed.

- [ ] **Step 2: Run graph baseline tests**

Run:

```bash
go test -count=1 ./internal/graph
```

Expected: package passes before refactor.

- [ ] **Step 3: Add failing test**

Add this test after `TestRouteGraphMiddlewareAffectsOnlyLaterRoutes` in `internal/graph/graph_test.go`:

```go
func TestRouteGraphIndexesMiddlewareBySymbol(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	auth := facts.SymbolID("method:example.com/project/auth:Auth:Middleware")
	audit := facts.SymbolID("func:example.com/project/audit::Middleware")
	store.Middleware = append(store.Middleware,
		facts.MiddlewareBindingFact{
			ID:                "middleware:b",
			MiddlewareSymbols: []facts.SymbolID{auth, ""},
			StatementIndex:    20,
			Span:              facts.SourceSpan{File: "router/b.go"},
		},
		facts.MiddlewareBindingFact{
			ID:                "middleware:a",
			MiddlewareSymbols: []facts.SymbolID{auth, audit},
			StatementIndex:    10,
			Span:              facts.SourceSpan{File: "router/a.go"},
		},
		facts.MiddlewareBindingFact{
			ID:                "middleware:c",
			MiddlewareSymbols: []facts.SymbolID{auth},
			StatementIndex:    5,
			Span:              facts.SourceSpan{File: "router/a.go"},
		},
	)

	graph := NewRouteGraph(store)
	authBindings := graph.MiddlewareBindingsForSymbol(auth)
	gotIDs := middlewareBindingIDs(authBindings)
	wantIDs := []string{"middleware:c", "middleware:a", "middleware:b"}
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("auth bindings = %#v, want %#v", gotIDs, wantIDs)
	}
	auditBindings := graph.MiddlewareBindingsForSymbol(audit)
	if len(auditBindings) != 1 || auditBindings[0].ID != "middleware:a" {
		t.Fatalf("audit bindings = %#v", auditBindings)
	}
	if bindings := graph.MiddlewareBindingsForSymbol(""); len(bindings) != 0 {
		t.Fatalf("empty symbol bindings = %#v", bindings)
	}
	authBindings[0].ID = "mutated"
	again := graph.MiddlewareBindingsForSymbol(auth)
	if again[0].ID != "middleware:c" {
		t.Fatalf("middleware bindings query did not return a copy: %#v", again)
	}
}
```

Add this helper near other graph test helpers:

```go
func middlewareBindingIDs(bindings []facts.MiddlewareBindingFact) []string {
	out := make([]string, len(bindings))
	for i, binding := range bindings {
		out[i] = binding.ID
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it fails**

Run:

```bash
go test -count=1 ./internal/graph -run TestRouteGraphIndexesMiddlewareBySymbol -v
```

Expected: FAIL to compile with `graph.MiddlewareBindingsForSymbol undefined`.

---

### Task 2: Implement RouteGraph Middleware Symbol Index

**Files:**
- Modify: `internal/graph/route.go`

**Interfaces:**
- Consumes:
  - `facts.MiddlewareBindingFact.MiddlewareSymbols`
- Produces:
  - `RouteGraph.MiddlewareBySymbol map[facts.SymbolID][]facts.MiddlewareBindingFact`
  - `func (g *RouteGraph) MiddlewareBindingsForSymbol(symbol facts.SymbolID) []facts.MiddlewareBindingFact`

- [ ] **Step 1: Add field and initialization**

In `type RouteGraph`, add:

```go
// MiddlewareBySymbol 按中间件 symbol 聚合绑定事实。
MiddlewareBySymbol map[facts.SymbolID][]facts.MiddlewareBindingFact
```

In `NewRouteGraph`, initialize it:

```go
MiddlewareBySymbol:    map[facts.SymbolID][]facts.MiddlewareBindingFact{},
```

- [ ] **Step 2: Build the index**

In the existing middleware loop:

```go
for _, binding := range store.Middleware {
	g.MiddlewareByID[binding.ID] = binding
}
```

change it to:

```go
for _, binding := range store.Middleware {
	g.MiddlewareByID[binding.ID] = binding
	for _, symbol := range binding.MiddlewareSymbols {
		if symbol == "" {
			continue
		}
		g.MiddlewareBySymbol[symbol] = append(g.MiddlewareBySymbol[symbol], binding)
	}
}
```

- [ ] **Step 3: Sort index values**

In `func (g *RouteGraph) sort()`, add:

```go
for symbol := range g.MiddlewareBySymbol {
	sortMiddlewareBindings(g.MiddlewareBySymbol[symbol])
}
```

Add helper near `sortRoutes`:

```go
func sortMiddlewareBindings(bindings []facts.MiddlewareBindingFact) {
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].Span.File != bindings[j].Span.File {
			return bindings[i].Span.File < bindings[j].Span.File
		}
		if bindings[i].StatementIndex != bindings[j].StatementIndex {
			return bindings[i].StatementIndex < bindings[j].StatementIndex
		}
		return bindings[i].ID < bindings[j].ID
	})
}
```

- [ ] **Step 4: Add copy-returning query**

Near other `RouteGraph` query methods, add:

```go
// MiddlewareBindingsForSymbol 返回引用指定中间件 symbol 的全部中间件绑定。返回副本，调用方可安全修改。
func (g *RouteGraph) MiddlewareBindingsForSymbol(symbol facts.SymbolID) []facts.MiddlewareBindingFact {
	return append([]facts.MiddlewareBindingFact(nil), g.MiddlewareBySymbol[symbol]...)
}
```

- [ ] **Step 5: Run graph tests**

Run:

```bash
gofmt -w internal/graph/route.go internal/graph/graph_test.go
go test -count=1 ./internal/graph
```

Expected: PASS.

---

### Task 3: Replace Impact Store Scan With Graph Query

**Files:**
- Modify: `internal/impact/tree_builder.go`
- Modify: `ARCHITECTURE.md`

**Interfaces:**
- Consumes:
  - `func (g *RouteGraph) MiddlewareBindingsForSymbol(symbol facts.SymbolID) []facts.MiddlewareBindingFact`
- Produces:
  - impact middleware symbol expansion uses graph query only.

- [ ] **Step 1: Replace helper body**

In `internal/impact/tree_builder.go`, replace `middlewareBindingsForSymbol` with:

```go
func (b *treeBuilder) middlewareBindingsForSymbol(symbolID facts.SymbolID) []facts.MiddlewareBindingFact {
	return b.routes.MiddlewareBindingsForSymbol(symbolID)
}
```

Remove now-obsolete sorting logic from this helper. If this makes `sort` unused in `tree_builder.go`, remove it from imports only if the compiler reports it unused.

- [ ] **Step 2: Update architecture note**

In `ARCHITECTURE.md` section `5.13 internal/graph`, update the `RouteGraph` bullet to mention middleware symbol lookup:

```markdown
- `RouteGraph`：handler/group/middleware/middleware symbol -> routes/annotations。
```

Keep the wording short and do not describe new user-visible behavior.

- [ ] **Step 3: Verify docs avoid absolute workspace paths**

Run:

```bash
workspace_root="$(git rev-parse --show-toplevel)"
workspace_parent="$(dirname "$workspace_root")"
rg -n "$workspace_root|$workspace_parent" ARCHITECTURE.md docs/superpowers/specs/2026-07-08-middleware-symbol-index-design.md docs/superpowers/plans/2026-07-08-middleware-symbol-index.md
```

Expected: no output.

- [ ] **Step 4: Run focused graph and impact tests**

Run:

```bash
go test -count=1 ./internal/graph ./internal/impact
```

Expected: PASS.

---

### Task 4: Final Verification And Commit

**Files:**
- Verify all modified files.

**Interfaces:**
- Consumes Tasks 1-3.
- Produces one refactor commit.

- [ ] **Step 1: Run formatting check**

Run:

```bash
gofmt -l $(git ls-files '*.go')
```

Expected: no output.

- [ ] **Step 2: Run full tests**

Run:

```bash
go test -count=1 ./...
```

Expected: all packages report `ok` or `[no test files]`.

- [ ] **Step 3: Run vet**

Run:

```bash
go vet ./...
```

Expected: no output.

- [ ] **Step 4: Check diff whitespace**

Run:

```bash
git diff --check
git diff --cached --check
```

Expected: no output.

- [ ] **Step 5: Review diff shape**

Run:

```bash
git diff --stat
git diff -- internal/graph/route.go internal/graph/graph_test.go internal/impact/tree_builder.go ARCHITECTURE.md
```

Expected: diff shows graph index, graph test, impact delegation, and a short architecture note.

- [ ] **Step 6: Commit**

Run:

```bash
git add ARCHITECTURE.md internal/graph/route.go internal/graph/graph_test.go internal/impact/tree_builder.go
git commit -m "refactor: index middleware bindings by symbol"
```

Expected: commit succeeds with one refactor commit.
