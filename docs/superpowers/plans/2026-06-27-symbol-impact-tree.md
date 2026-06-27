# Symbol Impact Tree Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a single-snapshot, symbol-first Go impact pipeline that maps diff lines to changed semantic roots, propagates through symbol and route dependencies, and emits a complete per-source reviewable tree ending at HTTP endpoints.

**Architecture:** Keep the existing fact-first pipeline. Extend symbol spans and reference extraction, repair route-group identity, then add a path-preserving impact model and a dedicated output projection. The `facts` command remains the extractor/debug contract; the `impact` command moves to the new `go-impact/v1alpha1` tree contract.

**Tech Stack:** Go 1.24 standard library (`go/ast`, `go/parser`, `go/token`, `encoding/json`), existing `internal/*` packages, table-driven tests, fixture projects, JSON Schema, golden tests.

**Execution status (2026-06-27):** Implemented and verified in the working tree.
The checkbox list below is retained as the original execution plan. Commit steps
remain intentionally uncommitted until the user approves the final commit.

---

## Scope

This plan implements the core dependency-analysis path:

```text
diff
  -> changed symbol/domain root
  -> reverse symbol references
  -> route domain
  -> endpoint
  -> per-source impact tree JSON
```

It intentionally excludes:

- Base/Head dual snapshots.
- Full deletion recovery.
- `go.mod` package-source propagation.
- `go/types` and SSA.
- compact output, skills, and natural-language reports.
- field/tag-specific facts or change classifications.

`go.mod` package sources and release/CI hardening should be planned separately after this tree contract is stable.

Implementation should start from a dedicated git worktree created from a clean commit containing the approved architecture and this plan.

## File Map

### Files to create

- `testdata/fixtures/type-impact/go.mod`
  - Fixture module for type-to-handler propagation.
- `testdata/fixtures/type-impact/model/model.go`
  - Nested request/response types with struct tags.
- `testdata/fixtures/type-impact/controller/controller.go`
  - Annotated handler using fixture types.
- `testdata/fixtures/type-impact/router/router.go`
  - Registered fixture handler.
- `testdata/fixtures/declaration-spans/go.mod`
  - Fixture module for complete declaration-span assertions.
- `testdata/fixtures/declaration-spans/types.go`
  - Multi-line type and value declarations.
- `testdata/fixtures/group-scope/go.mod`
  - Fixture module for duplicate group variable names.
- `testdata/fixtures/group-scope/controller/controller.go`
  - Two annotated handlers.
- `testdata/fixtures/group-scope/router/a.go`
  - First route function using a common group variable name.
- `testdata/fixtures/group-scope/router/b.go`
  - Second route function using the same group variable name.
- `internal/extract/reference/types.go`
  - AST type-expression traversal and type-symbol resolution.
- `internal/extract/reference/values.go`
  - Project-local selector/value/function-value references.
- `internal/impact/tree.go`
  - Internal path-preserving impact node and root result models.
- `internal/impact/tree_builder.go`
  - Per-root traversal, cycle handling, depth boundaries, and endpoint collection.
- `internal/output/impact_tree.go`
  - `go-impact/v1alpha1` document projection and deterministic rendering.
- `internal/output/impact_tree_test.go`
  - Projection and deterministic-output tests.
- `testdata/golden/type-impact.impact.json`
  - End-to-end impact tree golden output.

### Files to modify

- `internal/facts/change.go`
  - Add semantic-root change kinds; keep temporary aliases until callers migrate.
- `internal/facts/reference.go`
  - Add selector/value reference kinds and raw evidence consistency.
- `internal/facts/route.go`
  - Add stable `GroupID`/`ParentGroupID` fields.
- `internal/astindex/index.go`
  - Use complete declaration spans for var/const symbols.
- `internal/astindex/index_test.go`
  - Verify complete type/value spans.
- `internal/diff/range.go`
  - Preserve per-file raw diff and hunk anchor metadata.
- `internal/diff/parser.go`
  - Capture raw file patches and new-side deletion anchors.
- `internal/diff/parser_test.go`
  - Verify raw patch and deletion-anchor parsing.
- `internal/diff/mapper.go`
  - Map ranges to the smallest semantic root with deterministic precedence.
- `internal/diff/mapper_test.go`
  - Verify type, symbol, route-group, and fallback mapping.
- `internal/extract/reference/extractor.go`
  - Coordinate call/type/value extraction without duplicate edges.
- `internal/extract/reference/callee.go`
  - Avoid treating type conversions as function calls.
- `internal/extract/reference/extractor_test.go`
  - Verify type and value references.
- `internal/extract/route/context.go`
  - Carry stable group identity.
- `internal/extract/route/extractor.go`
  - Create root/derived group IDs and store them on routes/middleware.
- `internal/extract/route/extractor_test.go`
  - Verify stable group ownership.
- `internal/link/linker.go`
  - Resolve middleware symbols and keep route/domain links explicit.
- `internal/link/linker_test.go`
  - Verify middleware and route links.
- `internal/graph/route.go`
  - Index routes by `GroupID`, never global `GroupVar`.
- `internal/graph/graph_test.go`
  - Verify same-name groups cannot cross-impact.
- `internal/impact/analyzer.go`
  - Return tree-oriented root results.
- `internal/impact/propagation.go`
  - Delegate traversal to the tree builder.
- `internal/impact/analyzer_test.go`
  - Verify complete symbol-to-endpoint paths.
- `internal/output/contract.go`
  - Publish the new impact schema document.
- `internal/output/contract_test.go`
  - Validate the new schema.
- `internal/output/golden_test.go`
  - Add the impact tree golden assertion.
- `internal/app/options.go`
  - Add analysis depth/stop-boundary options only if required by config wiring.
- `internal/app/pipeline.go`
  - Pass parsed diff source data into impact projection.
- `internal/app/pipeline_test.go`
  - Verify end-to-end struct/type impact.
- `internal/config/config.go`
  - Add the `analysis` section.
- `internal/config/defaults.go`
  - Default unlimited traversal and enabled evidence/diff output.
- `internal/config/config_test.go`
  - Verify config merging.
- `cmd/go-analyzer/main.go`
  - Keep current CLI flags; render the new impact contract.
- `cmd/go-analyzer/main_test.go`
  - Verify CLI JSON shape.
- `docs/contracts/output-contract.md`
  - Document `go-impact/v1alpha1`.
- `docs/examples/go-analyzer.config.json`
  - Add analysis configuration.
- `README.md`
  - Update `impact` output description.
- `HANDOFF.md`
  - Record implementation state after all tasks pass.

## Task 1: Establish Full Declaration Spans and Semantic Change Kinds

**Files:**

- Modify: `internal/facts/change.go`
- Modify: `internal/astindex/index.go`
- Modify: `internal/astindex/index_test.go`

- [ ] **Step 1: Write failing tests for type and value declaration spans**

Add a fixture source in `internal/astindex/index_test.go` or extend the existing fixture so the test can locate:

```go
type Request struct {
    Name string `json:"name"`
}

var DefaultRequest = Request{Name: "default"}
```

Assert:

```go
func TestBuildUsesCompleteDeclarationSpans(t *testing.T) {
    idx := loadFixtureIndex(t, "declaration-spans")

    typeSymbol := mustSymbol(t, idx, "type:example.com/declaration-spans::Request")
    if typeSymbol.Span.EndLine <= typeSymbol.Span.StartLine {
        t.Fatalf("type span does not cover body: %#v", typeSymbol.Span)
    }

    valueSymbol := mustSymbol(t, idx, "var:example.com/declaration-spans::DefaultRequest")
    if valueSymbol.Span.EndCol <= valueSymbol.Span.StartCol {
        t.Fatalf("value span does not cover declaration: %#v", valueSymbol.Span)
    }
}
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run:

```bash
go test ./internal/astindex -run TestBuildUsesCompleteDeclarationSpans -v
```

Expected: FAIL because var/const symbols currently use only the identifier span.

- [ ] **Step 3: Expand `ValueSpec` spans**

In `internal/astindex/index.go`, build every symbol in one `ValueSpec` with `s.Pos(), s.End()`:

```go
for _, name := range s.Names {
    id := ValueSymbolID(kind, pkg.Path, name.Name)
    idx.Symbols[id] = symbolFact(
        p,
        file,
        id,
        kind,
        pkg.Path,
        "",
        name.Name,
        s.Pos(),
        s.End(),
    )
}
```

Keep `TypeSpec` on `s.Pos(), s.End()` and `FuncDecl` on `decl.Pos(), decl.End()`.

- [ ] **Step 4: Replace body-specific change kinds**

Define:

```go
const (
    ChangeKindSymbolChanged            ChangeKind = "symbol_changed"
    ChangeKindRouteGroupChanged        ChangeKind = "route_group_changed"
    ChangeKindRouteChanged             ChangeKind = "route_changed"
    ChangeKindMiddlewareChanged        ChangeKind = "middleware_changed"
    ChangeKindAnnotationChanged        ChangeKind = "annotation_changed"
    ChangeKindFileChanged              ChangeKind = "file_changed"
)
```

Do not add field/tag/body-specific kinds.

Keep temporary aliases so this commit does not break existing callers:

```go
const (
    ChangeKindMethodBodyChanged        = ChangeKindSymbolChanged
    ChangeKindRouteRegistrationChanged = ChangeKindRouteChanged
    ChangeKindMiddlewareBindingChanged = ChangeKindMiddlewareChanged
)
```

Remove these aliases only after Task 9 migrates the app, CLI, and all tests.

- [ ] **Step 5: Run astindex tests**

Run:

```bash
go test ./internal/astindex -v
```

Expected: PASS.

- [ ] **Step 6: Run the full suite before committing**

Run:

```bash
go test ./...
```

Expected: PASS; the compatibility aliases keep all existing callers compiling.

- [ ] **Step 7: Commit**

```bash
git add internal/facts/change.go internal/astindex/index.go internal/astindex/index_test.go testdata/fixtures/declaration-spans
git commit -m "refactor: model changes at symbol granularity"
```

## Task 2: Preserve Diff Source Evidence and Map the Smallest Root

**Files:**

- Modify: `internal/diff/range.go`
- Modify: `internal/diff/parser.go`
- Modify: `internal/diff/parser_test.go`
- Modify: `internal/diff/mapper.go`
- Modify: `internal/diff/mapper_test.go`

- [ ] **Step 1: Write a failing raw-patch parser test**

Add:

```go
func TestParseUnifiedPreservesRawFilePatch(t *testing.T) {
    input := []byte("diff --git a/model.go b/model.go\n--- a/model.go\n+++ b/model.go\n@@ -1 +1 @@\n-type A struct{}\n+type A struct{ Name string }\n")
    changes, err := ParseUnified(input)
    if err != nil {
        t.Fatal(err)
    }
    if len(changes) != 1 {
        t.Fatalf("changes = %d", len(changes))
    }
    if !strings.Contains(changes[0].Raw, "+type A struct{ Name string }") {
        t.Fatalf("raw patch = %q", changes[0].Raw)
    }
}
```

- [ ] **Step 2: Write a failing smallest-symbol mapping test**

Create a store with overlapping function/type/file spans and assert that the mapper chooses the containing symbol with the smallest line/column span, independent of slice order:

```go
func TestMapChangesSelectsSmallestContainingSymbol(t *testing.T) {
    store := facts.NewStore("/repo", "example.com/app")
    store.Symbols = []facts.SymbolFact{
        {ID: "type:example.com/app::Request", Kind: "type", Span: span("model.go", 1, 10)},
        {ID: "var:example.com/app::Nested", Kind: "var", Span: span("model.go", 5, 5)},
    }

    got := MapChanges([]FileChange{{
        NewPath: "model.go",
        Ranges:  []LineRange{{StartLine: 5, EndLine: 5}},
    }}, store, "git_diff")

    if got[0].SymbolID != "var:example.com/app::Nested" {
        t.Fatalf("symbol = %q", got[0].SymbolID)
    }
}
```

- [ ] **Step 3: Run focused diff tests and verify failure**

Run:

```bash
go test ./internal/diff -run 'TestParseUnifiedPreservesRawFilePatch|TestMapChangesSelectsSmallestContainingSymbol' -v
```

Expected: FAIL because `FileChange` has no raw patch and mapper returns the first matching symbol.

- [ ] **Step 4: Extend `FileChange`**

Use:

```go
type FileChange struct {
    OldPath string
    NewPath string
    Status  FileStatus
    Ranges  []LineRange
    Raw     string
}
```

Build `Raw` from the complete `diff --git` block. Do not trim meaningful patch lines.

- [ ] **Step 5: Implement deterministic root precedence**

In `mapRange`:

1. Check annotation.
2. Check route.
3. Check middleware.
4. Check route group.
5. Select the smallest containing symbol.
6. Fall back to file.

Use an explicit candidate comparison:

```go
func narrower(a, b facts.SourceSpan) bool {
    aLines := a.EndLine - a.StartLine
    bLines := b.EndLine - b.StartLine
    if aLines != bLines {
        return aLines < bLines
    }
    aCols := a.EndCol - a.StartCol
    bCols := b.EndCol - b.StartCol
    if aCols != bCols {
        return aCols < bCols
    }
    return a.File < b.File
}
```

Return `ChangeKindSymbolChanged` for every Go symbol kind.

- [ ] **Step 6: Add route-group mapping**

Before symbol fallback:

```go
for _, group := range store.RouteGroups {
    if spanContains(group.Span, file, r) {
        return changeFact(
            index,
            facts.ChangeKindRouteGroupChanged,
            group.ID,
            "",
            file,
            r,
            source,
            facts.ConfidenceHigh,
        )
    }
}
```

- [ ] **Step 7: Preserve a new-side anchor for deletion-only hunks**

Extend parsed hunk data so a hunk containing only removed lines contributes the nearest valid new-side line as a medium-confidence range. Use it only to find a surviving enclosing declaration; do not pretend the removed symbol still exists.

Add a test where a field is deleted from a surviving struct and assert the anchor maps to the owner type. Add a second test where the whole declaration is deleted and assert file fallback.

- [ ] **Step 8: Run diff tests**

Run:

```bash
go test ./internal/diff -v
```

Expected: PASS.

- [ ] **Step 9: Run the full suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/diff internal/facts/change.go
git commit -m "feat: map diffs to deterministic semantic roots"
```

## Task 3: Add Type References Without Field-Level Facts

**Files:**

- Create: `testdata/fixtures/type-impact/go.mod`
- Create: `testdata/fixtures/type-impact/model/model.go`
- Create: `testdata/fixtures/type-impact/controller/controller.go`
- Create: `testdata/fixtures/type-impact/router/router.go`
- Create: `internal/extract/reference/types.go`
- Modify: `internal/extract/reference/extractor.go`
- Modify: `internal/extract/reference/callee.go`
- Modify: `internal/extract/reference/extractor_test.go`
- Modify: `internal/facts/reference.go`
- Modify: `testdata/golden/mini-bff.facts.json`

- [ ] **Step 1: Create the type-impact fixture**

`model/model.go`:

```go
package model

type Address struct {
    City string `json:"city"`
}

type CreateOrderRequest struct {
    Address Address `json:"address"`
}

type CreateOrderResponse struct {
    ID string `json:"id"`
}
```

`controller/controller.go`:

```go
package controller

import "example.com/type-impact/model"

type OrderAPI struct{}

var API = &OrderAPI{}

// @Post /orders
func (api *OrderAPI) Create(req model.CreateOrderRequest) model.CreateOrderResponse {
    return model.CreateOrderResponse{ID: req.Address.City}
}
```

`router/router.go`:

```go
package router

import "example.com/type-impact/controller"

type Group struct{}

func (g *Group) POST(path string, handler any) {}

func Init(g *Group) {
    g.POST("/orders", controller.API.Create)
}
```

- [ ] **Step 2: Write failing type-reference tests**

Assert these edges:

```text
type:.../model::CreateOrderRequest -> type:.../model::Address
method:.../controller:OrderAPI:Create -> type:.../model::CreateOrderRequest
method:.../controller:OrderAPI:Create -> type:.../model::CreateOrderResponse
method:.../controller:OrderAPI:Create -> type:.../controller::OrderAPI
```

Test helper:

```go
func assertReference(t *testing.T, store *facts.Store, from, to facts.SymbolID, kind facts.ReferenceKind) {
    t.Helper()
    for _, ref := range store.References {
        if ref.FromSymbol == from && ref.ToSymbol == to && ref.Kind == kind {
            return
        }
    }
    t.Fatalf("reference %s -[%s]-> %s not found", from, kind, to)
}
```

- [ ] **Step 3: Run the focused test and verify failure**

Run:

```bash
go test ./internal/extract/reference -run TestExtractTypeReferences -v
```

Expected: FAIL because only call references exist.

- [ ] **Step 4: Implement recursive type-expression resolution**

In `types.go`, recursively handle:

```go
func collectTypeIDs(file *project.File, idx *astindex.Index, expr ast.Expr) []facts.SymbolID {
    switch x := expr.(type) {
    case *ast.Ident:
        id := astindex.TypeSymbolID(file.Package.Path, x.Name)
        if _, ok := idx.Symbols[id]; ok {
            return []facts.SymbolID{id}
        }
    case *ast.SelectorExpr:
        if pkg, ok := x.X.(*ast.Ident); ok {
            if importPath := file.Imports[pkg.Name]; importPath != "" {
                id := astindex.TypeSymbolID(importPath, x.Sel.Name)
                if _, ok := idx.Symbols[id]; ok {
                    return []facts.SymbolID{id}
                }
            }
        }
    case *ast.StarExpr:
        return collectTypeIDs(file, idx, x.X)
    case *ast.ArrayType:
        return collectTypeIDs(file, idx, x.Elt)
    case *ast.MapType:
        return mergeTypeIDs(
            collectTypeIDs(file, idx, x.Key),
            collectTypeIDs(file, idx, x.Value),
        )
    case *ast.ChanType:
        return collectTypeIDs(file, idx, x.Value)
    case *ast.Ellipsis:
        return collectTypeIDs(file, idx, x.Elt)
    case *ast.IndexExpr:
        return mergeTypeIDs(
            collectTypeIDs(file, idx, x.X),
            collectTypeIDs(file, idx, x.Index),
        )
    case *ast.IndexListExpr:
        out := collectTypeIDs(file, idx, x.X)
        for _, item := range x.Indices {
            out = mergeTypeIDs(out, collectTypeIDs(file, idx, item))
        }
        return out
    }
    return nil
}
```

Also walk `StructType`, `InterfaceType` and `FuncType` fields explicitly.

- [ ] **Step 5: Extract references from declarations**

For each `FuncDecl`:

- receiver type.
- every parameter type.
- every result type.
- every `CompositeLit.Type` in the body.

For each `TypeSpec`:

- alias/underlying type.
- struct field and embedded types.
- interface method signatures.

For each `ValueSpec`:

- explicit declaration type.
- composite literal types in values.

Use `ReferenceKindType` and include the referenced AST expression span/raw text.

- [ ] **Step 6: Prevent type conversions from becoming call edges**

Before resolving an identifier call as a function, check whether the identifier resolves to a project type. If it does, emit only the type reference.

- [ ] **Step 7: Run reference tests**

Run:

```bash
go test ./internal/extract/reference -v
```

Expected: PASS.

- [ ] **Step 8: Refresh the facts golden intentionally**

Run:

```bash
UPDATE_GOLDEN=1 go test ./internal/output -run TestMiniBFFGolden -v
git diff -- testdata/golden/mini-bff.facts.json
```

Inspect every new `type` reference and keep only expected additions.

- [ ] **Step 9: Run the full suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/extract/reference internal/facts/reference.go testdata/fixtures/type-impact testdata/golden/mini-bff.facts.json
git commit -m "feat: propagate project type references"
```

## Task 4: Add Selector, Value, Function-Value, and Middleware References

**Files:**

- Create: `internal/extract/reference/values.go`
- Modify: `internal/extract/reference/extractor.go`
- Modify: `internal/extract/reference/extractor_test.go`
- Modify: `internal/facts/reference.go`
- Modify: `internal/link/linker.go`
- Modify: `internal/link/linker_test.go`
- Modify: `testdata/golden/mini-bff.facts.json`

- [ ] **Step 1: Write failing value-reference tests**

Cover:

```go
const Timeout = 5
var DefaultRequest = model.CreateOrderRequest{}

func Build() {
    _ = Timeout
    _ = DefaultRequest
}
```

Expected:

```text
func:Build -> const:Timeout
func:Build -> var:DefaultRequest
```

Add a middleware fixture:

```go
func Auth() any { return nil }

func Init(g *Group) {
    g.Use(Auth())
}
```

Expected: the middleware binding records the `Auth` symbol.

- [ ] **Step 2: Run focused tests and verify failure**

Run:

```bash
go test ./internal/extract/reference ./internal/link -run 'TestExtractValueReferences|TestRunLinksMiddlewareSymbols' -v
```

Expected: FAIL because value selectors and middleware symbol links are absent.

- [ ] **Step 3: Add reference kinds**

Use:

```go
const (
    ReferenceKindCall     ReferenceKind = "call"
    ReferenceKindSelector ReferenceKind = "selector"
    ReferenceKindType     ReferenceKind = "type"
    ReferenceKindValue    ReferenceKind = "value"
)
```

- [ ] **Step 4: Resolve project-local identifiers and selectors**

Resolve:

- same-package var/const identifiers.
- imported package var/const selectors.
- function values passed as arguments.
- wrapper arguments after handler unwrapping.

Only emit edges when the target exists in `idx.Symbols`. Do not emit unresolved pseudo-symbols into the reverse graph.

- [ ] **Step 5: Link middleware symbols**

Extend `MiddlewareBindingFact` with:

```go
MiddlewareSymbols []SymbolID `json:"middleware_symbols,omitempty"`
```

Resolve every middleware argument to project-local function/method symbols where possible. Preserve `MiddlewareRaw` regardless of resolution success.

- [ ] **Step 6: Run tests**

Run:

```bash
go test ./internal/extract/reference ./internal/link -v
```

Expected: PASS.

- [ ] **Step 7: Refresh the facts golden if this fixture gains expected value references**

Run:

```bash
UPDATE_GOLDEN=1 go test ./internal/output -run TestMiniBFFGolden -v
git diff -- testdata/golden/mini-bff.facts.json
```

Inspect changes rather than blindly accepting the regenerated file.

- [ ] **Step 8: Run the full suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/extract/reference internal/facts/reference.go internal/facts/route.go internal/link testdata/golden/mini-bff.facts.json
git commit -m "feat: link value and middleware references"
```

## Task 5: Scope Route Groups by Stable GroupID

**Files:**

- Create: `testdata/fixtures/group-scope/go.mod`
- Create: `testdata/fixtures/group-scope/controller/controller.go`
- Create: `testdata/fixtures/group-scope/router/a.go`
- Create: `testdata/fixtures/group-scope/router/b.go`
- Modify: `internal/facts/route.go`
- Modify: `internal/extract/route/context.go`
- Modify: `internal/extract/route/extractor.go`
- Modify: `internal/extract/route/extractor_test.go`
- Modify: `internal/graph/route.go`
- Modify: `internal/graph/graph_test.go`
- Modify: `internal/output/contract.go`
- Modify: `testdata/golden/mini-bff.facts.json`

- [ ] **Step 1: Create a duplicate-group fixture**

Both route functions must use `g` and `group`:

```go
func InitA(g *Group) {
    group := g.Group("/a")
    group.Use(AuthA())
    group.GET("/one", controller.API.One)
}

func InitB(g *Group) {
    group := g.Group("/b")
    group.GET("/two", controller.API.Two)
}
```

- [ ] **Step 2: Write a failing isolation test**

Change the `InitA` middleware binding and assert only endpoint `/one` is returned:

```go
func TestRouteGraphScopesGroupsByRouteFunction(t *testing.T) {
    store := extractAndLinkFixture(t, "group-scope")
    graph := NewRouteGraph(store)

    binding := findMiddlewareInRouteFunc(t, store, "InitA")
    routes := graph.RoutesAffectedByMiddleware(binding.ID)

    assertRoutePaths(t, routes, "/one")
}
```

- [ ] **Step 3: Run the test and verify failure**

Run:

```bash
go test ./internal/graph -run TestRouteGraphScopesGroupsByRouteFunction -v
```

Expected: FAIL because current graph indexes globally by `GroupVar`.

- [ ] **Step 4: Extend route facts**

Use:

```go
type RouteGroupFact struct {
    ID             string
    GroupVar       string
    ParentGroupID  string
    ParentGroupVar string
    // existing fields
}

type RouteRegistrationFact struct {
    GroupID  string
    GroupVar string
    // existing fields
}

type MiddlewareBindingFact struct {
    GroupID  string
    GroupVar string
    // existing fields
}
```

- [ ] **Step 5: Give root parameters synthetic group IDs**

Build root context after calculating `routeFunc`:

```go
func rootGroupID(routeFunc facts.SymbolID, name string) string {
    return "route_group:" + string(routeFunc) + ":" + name + ":root"
}
```

Derived group facts receive a declaration-based ID and `ParentGroupID`.

- [ ] **Step 6: Replace `RoutesByGroup` with `RoutesByGroupID`**

All group and middleware propagation must use `GroupID`. Keep `GroupVar` only for JSON readability.

- [ ] **Step 7: Run route and graph tests**

Run:

```bash
go test ./internal/extract/route ./internal/graph -v
```

Expected: PASS.

- [ ] **Step 8: Update the facts schema and golden**

Add `group_id` and `parent_group_id` to the relevant schema definitions. Refresh the mini-BFF facts golden:

```bash
UPDATE_GOLDEN=1 go test ./internal/output -run TestMiniBFFGolden -v
git diff -- testdata/golden/mini-bff.facts.json
```

Inspect that every route/middleware points to the expected scoped group.

- [ ] **Step 9: Run the full suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/facts/route.go internal/extract/route internal/graph internal/output/contract.go testdata/fixtures/group-scope testdata/golden/mini-bff.facts.json
git commit -m "fix: scope route groups by stable identity"
```

## Task 6: Build Path-Preserving Impact Trees

**Files:**

- Create: `internal/impact/tree.go`
- Create: `internal/impact/tree_builder.go`
- Modify: `internal/impact/analyzer.go`
- Modify: `internal/impact/analyzer_test.go`

- [ ] **Step 1: Write a failing service-to-endpoint path test**

Expected node kinds and IDs:

```text
service symbol
  -> controller symbol
  -> route
  -> annotation
  -> endpoint
```

Test:

```go
func TestAnalyzeBuildsCompleteSymbolToEndpointTree(t *testing.T) {
    result := AnalyzeTrees(referenceImpactStore(), TreeOptions{})
    root := mustRoot(t, result, "change:service")

    path := firstEndpointPath(t, root.Root)
    assertNodeKinds(t, path, "func", "func", "route", "annotation", "endpoint")
    endpoint := path[len(path)-1]
    if endpoint.Method != "GET" || endpoint.Path != "/api/bff-web/common/checkIn" {
        t.Fatalf("endpoint = %#v", endpoint)
    }
}
```

- [ ] **Step 2: Write failing cycle and multi-endpoint tests**

Verify:

- A → B → A terminates with `Cycle: true`.
- one shared util can produce two endpoint leaves.
- endpoint summary is deduplicated.
- separate changes keep separate roots.

- [ ] **Step 3: Run focused tests and verify failure**

Run:

```bash
go test ./internal/impact -run 'TestAnalyzeBuildsCompleteSymbolToEndpointTree|TestAnalyzeMarksCycles|TestAnalyzeKeepsMultipleEndpoints' -v
```

Expected: FAIL because current evidence skips intermediate reference nodes and has no tree model.

- [ ] **Step 4: Define internal tree models**

Use:

```go
type Node struct {
    ID           string
    Kind         string
    Name         string
    File         string
    Package      string
    Relation     string
    Raw          string
    Span         facts.SourceSpan
    Confidence   facts.Confidence
    Level        int
    Cycle        bool
    StopBoundary bool
    Method       string
    Path         string
    Children     []Node
}

type RootImpact struct {
    Change   facts.ChangeFact
    Root     Node
    Endpoints []EndpointImpact
}

type TreeResult struct {
    Roots       []RootImpact
    Diagnostics []facts.DiagnosticFact
}
```

- [ ] **Step 5: Add the tree API beside the legacy API**

Introduce:

```go
func AnalyzeTrees(store *facts.Store, opts TreeOptions) TreeResult
```

Do not remove or change the existing `Analyze(store) Result` in this task. The app and old output tests still depend on it. Task 9 performs the atomic CLI migration and removes the compatibility path.

- [ ] **Step 6: Implement traversal**

For a symbol node:

1. Read `reverse.ReferencesTo(symbol)`.
2. Append caller/dependent symbols as children.
3. If the symbol is a registered handler, append route children.
4. Append linked annotation children.
5. Append endpoint leaves.

For route-group or middleware roots:

1. Resolve affected routes using `GroupID`.
2. Append route → annotation → endpoint children.

Use a path-local `map[string]bool`, not one global visited set. This preserves distinct paths while terminating cycles.

- [ ] **Step 7: Add deterministic child merging and sorting**

Merge only identical siblings:

```text
same parent + same child ID + same relation
```

Sort children by:

1. level.
2. kind.
3. file.
4. package.
5. ID.
6. relation.

- [ ] **Step 8: Preserve edge evidence**

Populate child `Relation`, `Raw`, `Span`, and `Confidence` from the edge used to reach the child. Do not synthesize raw expressions in the output layer.

- [ ] **Step 9: Run impact tests**

Run:

```bash
go test ./internal/impact -v
```

Expected: PASS.

- [ ] **Step 10: Run the full suite**

Run:

```bash
go test ./...
```

Expected: PASS because the legacy API remains available.

- [ ] **Step 11: Commit**

```bash
git add internal/impact
git commit -m "feat: build complete symbol impact trees"
```

## Task 7: Add Depth and Stop-Propagation Boundaries

**Files:**

- Modify: `internal/config/config.go`
- Modify: `internal/config/defaults.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/impact/tree_builder.go`
- Modify: `internal/impact/analyzer_test.go`
- Modify: `docs/examples/go-analyzer.config.json`

- [ ] **Step 1: Write failing config tests**

Expected config:

```json
{
  "analysis": {
    "maxDepth": 0,
    "stopPropagation": ["internal/generated/**"],
    "includeRawEvidence": true,
    "includeDiff": true
  }
}
```

Assert merge semantics:

- omitted values preserve defaults.
- `maxDepth < 0` is rejected.
- stop patterns append uniquely.

- [ ] **Step 2: Write failing propagation-boundary tests**

Verify:

- boundary node is present.
- boundary node has `StopBoundary: true`.
- children after boundary are absent.
- depth truncation produces `propagation_depth_truncated`.

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
go test ./internal/config ./internal/impact -run 'TestLoadMergesAnalysisConfig|TestAnalyzeStopsAtBoundary|TestAnalyzeHonorsMaxDepth' -v
```

Expected: FAIL because analysis config is absent.

- [ ] **Step 4: Implement `AnalysisConfig`**

```go
type AnalysisConfig struct {
    MaxDepth          int      `json:"maxDepth,omitempty"`
    StopPropagation   []string `json:"stopPropagation,omitempty"`
    IncludeRawEvidence *bool   `json:"includeRawEvidence,omitempty"`
    IncludeDiff        *bool   `json:"includeDiff,omitempty"`
}
```

Use pointers only where `false` must be distinguishable from omitted.

- [ ] **Step 5: Apply boundaries in traversal**

Evaluate stop patterns against project-relative slash paths before expanding children. Depth 0 means unlimited.

- [ ] **Step 6: Run config and impact tests**

Run:

```bash
go test ./internal/config ./internal/impact -v
```

Expected: PASS.

- [ ] **Step 7: Run the full suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/config internal/impact docs/examples/go-analyzer.config.json
git commit -m "feat: configure impact propagation boundaries"
```

## Task 8: Project the Original Reviewable Impact JSON

**Files:**

- Create: `internal/output/impact_tree.go`
- Create: `internal/output/impact_tree_test.go`
- Create: `testdata/golden/type-impact.impact.json`
- Modify: `internal/output/contract.go`
- Modify: `internal/output/contract_test.go`
- Modify: `internal/output/golden_test.go`

- [ ] **Step 1: Write a failing projection test**

Build two root impacts from the same source file and assert:

```go
func TestBuildImpactDocumentGroupsRootsBySourceFile(t *testing.T) {
    doc := BuildImpactDocument(project, fileChanges, result, options)

    if len(doc.FileSources) != 1 {
        t.Fatalf("fileSources = %d", len(doc.FileSources))
    }
    source := doc.FileSources[0]
    if len(source.Symbols) != 2 {
        t.Fatalf("symbols = %d", len(source.Symbols))
    }
    if !strings.Contains(source.Diff, "diff --git") {
        t.Fatalf("diff missing: %q", source.Diff)
    }
}
```

- [ ] **Step 2: Write a deterministic rendering test**

Render the same logical document from shuffled input slices and assert byte equality.

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
go test ./internal/output -run 'TestBuildImpactDocumentGroupsRootsBySourceFile|TestRenderImpactTreeJSONIsDeterministic' -v
```

Expected: FAIL because the tree document does not exist.

- [ ] **Step 4: Define output DTOs**

Define the DTOs from `docs/design/go-symbol-impact-architecture.md`:

- `ImpactDocument`.
- `ImpactMeta`.
- `FileSourceImpact`.
- `ImpactNode`.
- `EndpointSummary`.

Keep these in `internal/output`; do not add JSON tags to traversal-only implementation details in `internal/impact`.

- [ ] **Step 5: Build source documents**

For each changed file:

- attach its raw file diff when enabled.
- attach each changed symbol/domain root.
- collect relative file/package metadata from every node.
- collect and dedupe endpoint leaves.
- preserve diagnostics even when no endpoint is found.

For file fallback roots, use reserved root key `__non_symbol__`.

- [ ] **Step 6: Add `go-impact/v1alpha1` schema**

Schema requirements:

- `meta` and `fileSources` always present.
- `diagnostics`, `children`, and `impactedEndpoints` are arrays, never `null`.
- symbol roots are an object keyed by stable root ID.
- node kind/relation remain strings to allow additive values.
- additional properties are disabled for contract DTOs.

- [ ] **Step 7: Add the golden output**

Generate the fixture output with the implementation, inspect it manually, then store it at:

```text
testdata/golden/type-impact.impact.json
```

The golden must show:

```text
Address type
  -> CreateOrderRequest type
  -> OrderAPI.Create method
  -> POST route
  -> POST /orders annotation
  -> POST /orders endpoint
```

- [ ] **Step 8: Run output tests**

Run:

```bash
go test ./internal/output -v
```

Expected: PASS.

- [ ] **Step 9: Run the full suite**

Run:

```bash
go test ./...
```

Expected: PASS; the new renderer exists beside the legacy impact renderer.

- [ ] **Step 10: Commit**

```bash
git add internal/output testdata/golden/type-impact.impact.json
git commit -m "feat: publish reviewable symbol impact trees"
```

## Task 9: Wire the New Contract Through App and CLI

**Files:**

- Modify: `internal/app/options.go`
- Modify: `internal/app/pipeline.go`
- Modify: `internal/app/pipeline_test.go`
- Modify: `cmd/go-analyzer/main.go`
- Modify: `cmd/go-analyzer/main_test.go`

- [ ] **Step 1: Write a failing end-to-end type-impact test**

Create a diff that changes the `Address.City` struct tag or field line. Run `RunImpact` against `type-impact`.

Assert:

```go
func TestRunImpactMapsStructChangeToEndpointTree(t *testing.T) {
    got, err := RunImpact(typeImpactOptions(t))
    if err != nil {
        t.Fatal(err)
    }

    var doc output.ImpactDocument
    if err := json.Unmarshal(got, &doc); err != nil {
        t.Fatal(err)
    }
    assertSourceRoot(t, doc, "model/model.go", "type:example.com/type-impact/model::Address")
    assertEndpointSummary(t, doc, "POST", "/orders")
}
```

- [ ] **Step 2: Run the test and verify failure**

Run:

```bash
go test ./internal/app -run TestRunImpactMapsStructChangeToEndpointTree -v
```

Expected: FAIL because current CLI returns the old flat endpoint/evidence result and type references do not reach the route.

- [ ] **Step 3: Wire parsed file changes into projection**

`RunImpact` should:

1. build facts.
2. parse unified diff.
3. map changed roots.
4. run tree analysis.
5. project roots plus raw file changes.
6. render `go-impact/v1alpha1`.

Do not let `output` parse the diff again.

Switch the app and CLI to `impact.AnalyzeTrees`. After their tests consume the new contract:

- delete the legacy flat result/projection if no internal caller remains.
- remove the compatibility `Analyze` function.
- remove the temporary old ChangeKind aliases from Task 1.
- migrate or delete tests that assert only the obsolete flat JSON shape.

- [ ] **Step 4: Keep absolute input-path validation**

No CLI behavior change is required for:

- `--project`.
- `--diff`.
- `--config`.
- `--format json`.

- [ ] **Step 5: Update CLI tests**

Assert the output contains:

```json
{
  "meta": {
    "schemaVersion": "go-impact/v1alpha1"
  },
  "fileSources": []
}
```

Do not assert implementation-only `facts.Store` fields in CLI tests.

- [ ] **Step 6: Run app and CLI tests**

Run:

```bash
go test ./internal/app ./cmd/go-analyzer -v
```

Expected: PASS.

- [ ] **Step 7: Run the full suite**

Run:

```bash
go test ./...
```

Expected: PASS with no legacy impact contract callers.

- [ ] **Step 8: Commit**

```bash
git add internal/app cmd/go-analyzer internal/impact internal/output internal/facts/change.go
git commit -m "feat: wire symbol impact tree CLI output"
```

## Task 10: Harden Diagnostics and Partial Analysis

**Files:**

- Modify: `internal/diagnostics/codes.go`
- Modify: `internal/diagnostics/diagnostics_test.go`
- Modify: `internal/project/package.go`
- Modify: `internal/project/loader.go`
- Modify: `internal/project/loader_test.go`
- Modify: `internal/extract/reference/extractor.go`
- Modify: `internal/impact/tree_builder.go`
- Modify: `internal/output/impact_tree_test.go`
- Modify: `internal/app/pipeline.go`

- [ ] **Step 1: Add diagnostic codes**

Add:

```go
const (
    CodeSymbolReferenceUnresolved  Code = "symbol_reference_unresolved"
    CodeTypeReferenceUnresolved    Code = "type_reference_unresolved"
    CodeDeletedSymbolUnresolved    Code = "deleted_symbol_unresolved"
    CodePropagationDepthTruncated  Code = "propagation_depth_truncated"
)
```

- [ ] **Step 2: Write failing partial-analysis tests**

Verify:

- one unresolved reference does not abort other references.
- a changed root with no endpoint remains in `fileSources`.
- diagnostics appear in `meta.diagnostics`.
- diagnostics are deterministically deduplicated.

- [ ] **Step 3: Run focused tests and verify failure**

Run:

```bash
go test ./internal/diagnostics ./internal/impact ./internal/output -run 'Unresolved|Diagnostic|NoEndpoint' -v
```

Expected: FAIL until diagnostics are propagated into the new document.

- [ ] **Step 4: Emit diagnostics at the owning stage**

- extractor emits unresolved reference diagnostics only for recognized project-local patterns.
- diff mapper emits deleted-symbol fallback diagnostics.
- propagator emits depth truncation diagnostics.
- output only sorts and renders diagnostics.

- [ ] **Step 5: Make project parsing failures recoverable**

Add a project-local load diagnostic that does not import `internal/facts`:

```go
type LoadDiagnostic struct {
    Code    string
    File    string
    Message string
}
```

Store `[]LoadDiagnostic` on `project.Project`. When one `.go` file fails to parse:

- append `package_load_failed`.
- skip that file.
- continue walking the remaining project.

Keep missing/unreadable `go.mod` and project-root walk failures as fatal errors.

In `app.buildFactStore`, translate project load diagnostics into `facts.DiagnosticFact` after creating the store.

- [ ] **Step 6: Keep source results without endpoints**

Never drop a source because no route endpoint was found. The raw tree is still required for review.

- [ ] **Step 7: Run focused packages**

Run:

```bash
go test ./internal/diagnostics ./internal/project ./internal/extract/reference ./internal/impact ./internal/output -v
```

Expected: PASS.

- [ ] **Step 8: Run the full suite**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/diagnostics internal/project internal/extract/reference internal/impact internal/output internal/app/pipeline.go
git commit -m "feat: preserve partial impact analysis diagnostics"
```

## Task 11: Update Contracts and Developer Documentation

**Files:**

- Modify: `docs/contracts/output-contract.md`
- Modify: `README.md`
- Modify: `HANDOFF.md`
- Modify: `docs/validation/real-project-validation.md`

- [ ] **Step 1: Document the new impact contract**

Document:

- `go-impact/v1alpha1`.
- `fileSources`.
- symbol-root map.
- recursive node fields.
- endpoint leaves and summary.
- diagnostics.
- single-snapshot deletion limitation.
- facts versus impact responsibilities.

- [ ] **Step 2: Update README examples**

Keep:

```bash
go run ./cmd/go-analyzer impact \
  --project /absolute/path/to/project \
  --diff /absolute/path/to/change.diff \
  --format json
```

Explain that output is grouped by diff source file and changed symbol.

- [ ] **Step 3: Update HANDOFF only with verified implementation state**

Do not mark type propagation, GroupID, or tree output complete until their tests and real-project validation pass.

- [ ] **Step 4: Update validation metrics**

Record:

- changed source count.
- changed root count.
- impact tree node count.
- endpoint count.
- unresolved-reference diagnostic count.
- runtime.

- [ ] **Step 5: Check documentation formatting**

Run:

```bash
git diff --check
rg -n '/Users/' README.md HANDOFF.md docs \
  --glob '!**/2026-06-27-symbol-impact-tree.md'
```

Expected:

- `git diff --check` exits 0.
- `rg` returns no machine-specific absolute paths.

- [ ] **Step 6: Commit**

```bash
git add README.md HANDOFF.md docs
git commit -m "docs: publish symbol impact tree contract"
```

## Task 12: Full Verification and Real-Project Smoke

**Files:**

- Modify: `scripts/smoke-real-projects.sh`
- Modify: `docs/validation/real-project-validation.md`

- [ ] **Step 1: Fix sibling project discovery**

Allow the smoke script to resolve the current sibling names:

```text
sl-sc1-bff-service
sl-sc1-admin-bff
```

Keep analyzer input paths absolute.

- [ ] **Step 2: Add a facts smoke and impact fixture smoke**

Facts smoke:

- parse both real projects.
- validate JSON.
- record facts counts.

Impact smoke:

- run the checked-in `type-impact` diff.
- validate `go-impact/v1alpha1`.
- assert `POST /orders`.

- [ ] **Step 3: Run all unit and integration tests**

Run:

```bash
go test ./...
```

Expected: PASS for every package.

- [ ] **Step 4: Run vet**

Run:

```bash
go vet ./...
```

Expected: no output and exit 0.

- [ ] **Step 5: Validate schemas**

Run:

```bash
go run ./cmd/go-analyzer schema --type facts >/tmp/go-analyzer-facts.schema.json
go run ./cmd/go-analyzer schema --type impact >/tmp/go-analyzer-impact.schema.json
python3 -m json.tool /tmp/go-analyzer-facts.schema.json >/dev/null
python3 -m json.tool /tmp/go-analyzer-impact.schema.json >/dev/null
```

Expected: all commands exit 0.

- [ ] **Step 6: Run real-project smoke**

Run:

```bash
bash scripts/smoke-real-projects.sh
```

Expected:

- both real projects produce valid facts JSON.
- type-impact produces a valid impact tree.
- no analyzer panic.

- [ ] **Step 7: Check repository diff**

Run:

```bash
git diff --check
git status --short
```

Expected: only intended implementation, fixture, schema, golden, and documentation changes.

- [ ] **Step 8: Commit verification updates**

```bash
git add scripts/smoke-real-projects.sh docs/validation/real-project-validation.md
git commit -m "test: validate symbol impact trees on real projects"
```

## Completion Criteria

The plan is complete only when:

- A diff inside a struct body maps to the owner type symbol.
- Type references propagate through nested types and handler signatures.
- A service/type change produces a complete symbol → route → annotation → endpoint tree.
- Route groups with identical local variable names remain isolated by `GroupID`.
- Every source file remains in output even when it has no endpoint.
- `impactedEndpoints` exactly matches deduplicated endpoint leaves.
- output contains relative files and package paths for all intermediate nodes.
- cycles and boundaries terminate deterministically.
- `facts` remains valid and `impact` publishes `go-impact/v1alpha1`.
- all unit, golden, schema, vet, and smoke checks pass.
