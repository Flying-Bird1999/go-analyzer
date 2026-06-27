# Go Impact Next Iteration Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add deleted route registration impact, go.mod diff-to-endpoint propagation, and lightweight receiver/type inference for common BFF selector patterns.

**Architecture:** Keep the current fact-first, single-snapshot impact pipeline. Extend diff parsing to preserve deleted blocks and go.mod module changes, append synthetic facts only inside the impact pipeline, and enhance reference/middleware resolution with a small project-local type index instead of go/types/SSA.

**Tech Stack:** Go 1.24 standard library (`go/ast`, `go/parser`, `go/token`), existing `internal/*` packages, fixture-driven tests, golden JSON, smoke script.

---

## Scope

Included:

- Deleted route registration recovery from unified diff.
- go.mod require/replace diff to local import usage to endpoint propagation.
- Lightweight project-local selector method resolution.
- New diagnostics and fixtures.
- Contract/docs/golden updates.

Excluded:

- Full base/head dual snapshots.
- Full deleted symbol recovery.
- External package API diff.
- go/types / SSA.
- Natural-language report generation.

## File Map

### Create

- `internal/diff/deleted.go`
  - Deleted block model helpers and tests support.
- `internal/extract/gomod/diff.go`
  - go.mod diff extraction from unified diff raw hunks.
- `internal/impact/deleted_route.go`
  - Deleted route synthetic fact creation and impact pipeline integration helpers.
- `internal/extract/route/call.go`
  - Reusable route call parsing helper shared by normal extraction and deleted-route recovery.
- `internal/extract/reference/type_index.go`
  - Lightweight type index.
- `testdata/fixtures/deleted-route/go.mod`
- `testdata/fixtures/deleted-route/controller/controller.go`
- `testdata/fixtures/deleted-route/router/router.go`
- `testdata/diffs/deleted-route.diff`
- `testdata/fixtures/gomod-impact/go.mod`
- `testdata/fixtures/gomod-impact/service/service.go`
- `testdata/fixtures/gomod-impact/controller/controller.go`
- `testdata/fixtures/gomod-impact/router/router.go`
- `testdata/diffs/gomod-impact.diff`
- `testdata/fixtures/middleware-selector/go.mod`
- `testdata/fixtures/middleware-selector/middleware/auth.go`
- `testdata/fixtures/middleware-selector/router/router.go`

### Modify

- `internal/diff/range.go`
  - Add `DeletedBlock` to `FileChange`.
- `internal/diff/parser.go`
  - Capture old/new hunk starts and deleted blocks.
- `internal/diff/parser_test.go`
  - Verify deleted block preservation.
- `internal/extract/gomod/extractor.go`
  - Add module diff extraction from go.mod patch lines.
- `internal/extract/gomod/extractor_test.go`
  - Add go.mod diff parser tests.
- `internal/facts/change.go`
  - Add `ChangeKindRouteDeleted`.
- `internal/diagnostics/codes.go`
  - Add deleted route and module diff diagnostics.
- `internal/app/pipeline.go`
  - Wire deleted routes and go.mod module changes into `RunImpact`.
- `internal/extract/route/extractor.go`
  - Delegate route call extraction to the reusable helper.
- `internal/extract/reference/extractor.go`
  - Use lightweight type index for selector method resolution.
- `internal/extract/reference/values.go`
  - Resolve `pkg.Var.Method` method values.
- `internal/link/middleware.go`
  - Reuse selector method resolver for middleware bindings.
- `internal/graph/route.go`
  - Index synthetic deleted routes without cross-contaminating normal facts.
- `internal/impact/tree_builder.go`
  - Render deleted route roots and fallback endpoints.
- `internal/output/contract.go`
  - Add impact top-level `module_changes` / `module_usages` and keep schemas in sync.
- `internal/output/golden_test.go`
  - Add deleted route and go.mod impact golden cases if needed.
- `scripts/smoke-real-projects.sh`
  - Add fixture smoke for deleted route and go.mod impact.
- `docs/design/go-impact-next-iteration-architecture.md`
  - Keep in sync with final implementation decisions.
- `HANDOFF.md`
  - Update current state and known boundaries after implementation.

---

## Task 1: Preserve Deleted Blocks in Unified Diff

**Files:**

- Modify: `internal/diff/range.go`
- Modify: `internal/diff/parser.go`
- Modify: `internal/diff/parser_test.go`

- [ ] **Step 1: Write failing deleted block parser test**

Add test:

```go
func TestParseUnifiedPreservesDeletedBlocks(t *testing.T) {
    patch := []byte("diff --git a/router/router.go b/router/router.go\n" +
        "--- a/router/router.go\n" +
        "+++ b/router/router.go\n" +
        "@@ -10,3 +10,2 @@ func Init(g *gin.RouterGroup) {\n" +
        "-\tg.GET(\"/orders\", controller.API.List)\n" +
        " }\n")
    changes, err := ParseUnified(patch)
    if err != nil {
        t.Fatal(err)
    }
    if len(changes) != 1 || len(changes[0].DeletedBlocks) != 1 {
        t.Fatalf("deleted blocks = %#v", changes)
    }
    block := changes[0].DeletedBlocks[0]
    if block.OldStartLine != 10 || block.NewAnchorLine != 10 {
        t.Fatalf("block = %#v", block)
    }
    if got := strings.Join(block.Lines, "\n"); !strings.Contains(got, `g.GET("/orders"`) {
        t.Fatalf("deleted lines = %q", got)
    }
}
```

- [ ] **Step 2: Run focused test and verify failure**

Run:

```bash
GOCACHE=/private/tmp/go-build-go-analyzer go test ./internal/diff -run TestParseUnifiedPreservesDeletedBlocks -v
```

Expected: FAIL because `DeletedBlocks` does not exist.

- [ ] **Step 3: Add `DeletedBlock` model**

In `internal/diff/range.go`:

```go
type DeletedBlock struct {
    OldStartLine int      `json:"old_start_line"`
    NewAnchorLine int     `json:"new_anchor_line"`
    Lines []string        `json:"lines"`
}

type FileChange struct {
    ...
    DeletedBlocks []DeletedBlock `json:"deleted_blocks,omitempty"`
}
```

- [ ] **Step 4: Capture old/new hunk starts**

Change hunk parser regex to capture both sides:

```go
var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)
```

Track `oldLine` and `newLine`.

- [ ] **Step 5: Append deleted blocks**

When a contiguous deletion sequence ends, append:

```go
current.DeletedBlocks = append(current.DeletedBlocks, DeletedBlock{
    OldStartLine: deletedOldStart,
    NewAnchorLine: deletedNewAnchor,
    Lines: append([]string(nil), deletedLines...),
})
```

Keep existing deletion anchor range behavior unchanged.

- [ ] **Step 6: Run diff tests**

Run:

```bash
GOCACHE=/private/tmp/go-build-go-analyzer go test ./internal/diff -v
```

Expected: PASS.

---

## Task 2: Recover Deleted Route Registrations

**Files:**

- Create: `internal/impact/deleted_route.go`
- Modify: `internal/facts/change.go`
- Modify: `internal/diagnostics/codes.go`
- Modify: `internal/app/pipeline.go`
- Test: `internal/app/pipeline_test.go`
- Fixture: `testdata/fixtures/deleted-route/*`
- Diff: `testdata/diffs/deleted-route.diff`

- [ ] **Step 1: Create deleted-route fixture**

Create a small fixture with:

```go
// controller/controller.go
// @Get /orders
func (OrderAPI) List(ctx context.Context) {}
```

```go
// router/router.go
func Init(g *gin.RouterGroup) {
    g.GET("/orders", controller.API.List)
}
```

Create `testdata/diffs/deleted-route.diff` that deletes only the route registration line.

- [ ] **Step 2: Write failing end-to-end deleted route impact test**

In `internal/app/pipeline_test.go`:

```go
func TestRunImpactMapsDeletedRouteRegistrationToEndpoint(t *testing.T) {
    root := absFixture(t, "deleted-route")
    diffPath := absTestdata(t, "diffs/deleted-route.diff")
    got, err := RunImpact(ImpactOptions{ProjectPath: root, DiffPath: diffPath, Format: "json"})
    if err != nil {
        t.Fatal(err)
    }
    var doc output.ImpactDocument
    if err := json.Unmarshal(got, &doc); err != nil {
        t.Fatal(err)
    }
    assertEndpointSummary(t, doc, "GET", "/orders")
    assertAnyNodeKind(t, doc, "route")
}
```

Expected initially: FAIL because deleted route is not recovered.

- [ ] **Step 3: Add `ChangeKindRouteDeleted`**

In `internal/facts/change.go`:

```go
ChangeKindRouteDeleted ChangeKind = "route_deleted"
```

- [ ] **Step 4: Add diagnostics**

In `internal/diagnostics/codes.go`:

```go
CodeDeletedRouteUnresolved Code = "deleted_route_unresolved"
CodeDeletedRouteHandlerUnresolved Code = "deleted_route_handler_unresolved"
CodeDeletedRouteEndpointFallback Code = "deleted_route_endpoint_fallback"
```

- [ ] **Step 5: Extract reusable route call parser**

Create `internal/extract/route/call.go` with an exported helper:

```go
type ParsedRouteCall struct {
    Method string
    LocalPath string
    PathRaw string
    HandlerRaw string
    Wrappers []facts.WrapperFact
}

func ParseRouteCall(call *ast.CallExpr, cfg config.Config) (ParsedRouteCall, bool)
```

Update existing `routeCall` to use this helper before filling group, route function,
statement index, span and source-family fields.

- [ ] **Step 6: Implement deleted route recovery helper**

In `internal/impact/deleted_route.go`, expose:

```go
func RecoverDeletedRoutes(fileChanges []diff.FileChange, p *project.Project, idx *astindex.Index, store *facts.Store, cfg config.Config) []facts.ChangeFact
```

Behavior:

1. Iterate `.go` file changes.
2. For each `DeletedBlock`, parse deleted text as temporary function body.
3. Use `route.ParseRouteCall` to detect route methods and handler wrappers.
4. Resolve handler symbol using existing linker logic where possible.
5. Append synthetic `RouteRegistrationFact` to `store.Routes`.
6. Return `route_deleted` changes targeting the synthetic route ID.

- [ ] **Step 7: Wire into `RunImpact`**

In `internal/app/pipeline.go`, after `ParseUnified` and before normal `MapChanges`:

```go
deletedRouteChanges := impact.RecoverDeletedRoutes(fileChanges, p, idx, store, cfg)
store.Changes = append(store.Changes, deletedRouteChanges...)
store.Changes = append(store.Changes, diff.MapChanges(fileChanges, store, "git_diff")...)
```

This may require splitting `buildFactStore` so `RunImpact` can access `project.Project` and `astindex.Index`.

- [ ] **Step 8: Teach tree builder deleted route fallback**

If a synthetic deleted route has no annotation but has method/path, emit fallback endpoint with medium confidence and diagnostic.

- [ ] **Step 9: Run focused app test**

Run:

```bash
GOCACHE=/private/tmp/go-build-go-analyzer go test ./internal/app -run TestRunImpactMapsDeletedRouteRegistrationToEndpoint -v
```

Expected: PASS.

---

## Task 3: Parse go.mod Diff Module Changes

**Files:**

- Create: `internal/extract/gomod/diff.go`
- Modify: `internal/extract/gomod/extractor.go`
- Modify: `internal/extract/gomod/extractor_test.go`
- Modify: `internal/diagnostics/codes.go`

- [ ] **Step 1: Write failing go.mod diff parser tests**

In `internal/extract/gomod/extractor_test.go` add:

```go
func TestDiffModulesFromPatchDetectsRequireUpgrade(t *testing.T) {
    patch := "diff --git a/go.mod b/go.mod\n" +
        "--- a/go.mod\n+++ b/go.mod\n" +
        "@@ -3,3 +3,3 @@\n" +
        "-require example.com/jsonx v1.0.0\n" +
        "+require example.com/jsonx v1.1.0\n"
    changes, err := DiffModulesFromPatch([]byte(patch))
    if err != nil {
        t.Fatal(err)
    }
    assertModuleChange(t, changes, "example.com/jsonx", facts.ModuleChangeUpgraded)
}
```

Also cover block require and replace.

- [ ] **Step 2: Run focused gomod test and verify failure**

Run:

```bash
GOCACHE=/private/tmp/go-build-go-analyzer go test ./internal/extract/gomod -run TestDiffModulesFromPatch -v
```

Expected: FAIL because function does not exist.

- [ ] **Step 3: Implement `DiffModulesFromPatch`**

Implementation:

1. Iterate patch raw lines.
2. Collect deleted require/replace module records.
3. Collect added require/replace module records.
4. Compare by module path.
5. Reuse existing `moduleChange` and version comparison logic.

Do not parse go.sum in this task.

- [ ] **Step 4: Add unresolved diagnostic**

If go.mod has changed lines but no module record can be extracted, emit `module_diff_unresolved`.

- [ ] **Step 5: Run gomod tests**

Run:

```bash
GOCACHE=/private/tmp/go-build-go-analyzer go test ./internal/extract/gomod -v
```

Expected: PASS.

---

## Task 4: Propagate go.mod Changes to Endpoints

**Files:**

- Modify: `internal/app/pipeline.go`
- Modify: `internal/app/pipeline_test.go`
- Fixture: `testdata/fixtures/gomod-impact/*`
- Diff: `testdata/diffs/gomod-impact.diff`
- Optional golden: `testdata/golden/gomod-impact.impact.json`

- [ ] **Step 1: Create gomod-impact fixture**

Fixture shape:

```text
go.mod requires example.com/jsonx v1.1.0
service.Encode imports jsonx
controller.OrderAPI.Create calls service.Encode
router registers OrderAPI.Create
annotation exposes POST /orders
```

- [ ] **Step 2: Create go.mod diff**

`testdata/diffs/gomod-impact.diff` changes:

```diff
-require example.com/jsonx v1.0.0
+require example.com/jsonx v1.1.0
```

- [ ] **Step 3: Write failing end-to-end test**

In `internal/app/pipeline_test.go`:

```go
func TestRunImpactMapsGoModChangeToEndpoint(t *testing.T) {
    root := absFixture(t, "gomod-impact")
    diffPath := absTestdata(t, "diffs/gomod-impact.diff")
    got, err := RunImpact(ImpactOptions{ProjectPath: root, DiffPath: diffPath, Format: "json"})
    if err != nil {
        t.Fatal(err)
    }
    var doc output.ImpactDocument
    if err := json.Unmarshal(got, &doc); err != nil {
        t.Fatal(err)
    }
    assertEndpointSummary(t, doc, "POST", "/orders")
}
```

Expected: FAIL before pipeline wiring.

- [ ] **Step 4: Wire module changes into impact pipeline**

In `RunImpact`:

1. Detect `go.mod` file change.
2. Call `gomod.DiffModulesFromPatch(fileChange.Raw)`.
3. Append module changes to `store.ModuleChanges`.
4. Call `gomod.MapModuleUsage(p, idx, store, moduleChanges)`.
5. Append usages to `store.ModuleUsages`.
6. Convert usages to `ChangeFact`.

Suggested helper:

```go
func mapModuleUsagesToChanges(usages []facts.ModuleUsageFact) []facts.ChangeFact
```

- [ ] **Step 5: Implement usage-to-change mapping**

Mapping:

```go
if usage.SymbolID != "" {
    kind = facts.ChangeKindSymbolChanged
    symbolID = usage.SymbolID
    targetID = string(usage.SymbolID)
} else if usage.File != "" {
    kind = facts.ChangeKindFileChanged
    targetID = usage.File
}
source = "go_mod_diff"
confidence = usage.Confidence
```

- [ ] **Step 6: Run focused test**

Run:

```bash
GOCACHE=/private/tmp/go-build-go-analyzer go test ./internal/app -run TestRunImpactMapsGoModChangeToEndpoint -v
```

Expected: PASS.

---

## Task 5: Add Lightweight Type Index

**Files:**

- Create: `internal/extract/reference/type_index.go`
- Modify: `internal/extract/reference/extractor_test.go`
- Modify: `internal/extract/reference/extractor.go`
- Modify: `internal/extract/reference/values.go`

- [ ] **Step 1: Write failing type index tests**

Add tests for:

```go
var AuthOptional = Auth{}
var AuthRequired = &Auth{}

func (a Auth) Middleware() {}
```

Assert resolver can map:

```text
auth.AuthOptional.Middleware -> method:<auth pkg>:Auth:Middleware
```

- [ ] **Step 2: Run focused reference test and verify failure**

Run:

```bash
GOCACHE=/private/tmp/go-build-go-analyzer go test ./internal/extract/reference -run TestResolveSelectorMethodThroughPackageVarType -v
```

Expected: FAIL.

- [ ] **Step 3: Implement `TypeIndex`**

Create:

```go
type TypeIndex struct {
    Values map[facts.SymbolID]ValueType
    StructFields map[facts.SymbolID]map[string]ValueType
}

type ValueType struct {
    PackagePath string
    TypeName string
    Confidence facts.Confidence
}
```

Build from project AST:

- `var X T`
- `var X = T{}`
- `var X = &T{}`
- `type API struct { Svc service.OrderService }`

- [ ] **Step 4: Add selector chain resolver**

Add resolver:

```go
func ResolveSelectorMethod(file *project.File, idx *astindex.Index, types *TypeIndex, expr ast.Expr) (facts.SymbolID, bool)
```

Support first version:

```text
pkg.Var.Method
```

- [ ] **Step 5: Emit value references for resolved selector methods**

When expression resolves to a method symbol, emit `ReferenceKindValue` or `ReferenceKindSelector` edge with `ToSymbol` set to resolved method.

- [ ] **Step 6: Run reference tests**

Run:

```bash
GOCACHE=/private/tmp/go-build-go-analyzer go test ./internal/extract/reference -v
```

Expected: PASS.

---

## Task 6: Use Lightweight Type Resolution for Middleware Selectors

**Files:**

- Modify: `internal/link/middleware.go`
- Modify: `internal/link/linker.go`
- Modify: `internal/link/linker_test.go`
- Fixture: `testdata/fixtures/middleware-selector/*`

- [ ] **Step 1: Create middleware-selector fixture**

Fixture:

```go
package middleware

var Optional = Auth{}

type Auth struct{}

func (Auth) Middleware(ctx context.Context) {}
```

```go
package router

func Init(g *gin.RouterGroup) {
    g.Use(middleware.Optional.Middleware)
    g.GET("/orders", controller.API.List)
}
```

- [ ] **Step 2: Write failing linker test**

Assert `MiddlewareBindingFact.MiddlewareSymbols` contains:

```text
method:example.com/middleware-selector/middleware:Auth:Middleware
```

- [ ] **Step 3: Run focused test and verify failure**

Run:

```bash
GOCACHE=/private/tmp/go-build-go-analyzer go test ./internal/link -run TestRunLinksMiddlewareSelectorMethodSymbols -v
```

Expected: FAIL.

- [ ] **Step 4: Pass type index into middleware resolver**

Either:

1. Build type index inside `link.Run`, or
2. Build once in app pipeline and pass to extract/link helpers.

Prefer option 1 initially to keep public app pipeline smaller.

- [ ] **Step 5: Resolve middleware selector methods**

Update `linkMiddlewareSymbols`:

```text
Auth() call
Auth function value
pkg.Var.Method selector method
```

- [ ] **Step 6: Run link tests**

Run:

```bash
GOCACHE=/private/tmp/go-build-go-analyzer go test ./internal/link -v
```

Expected: PASS.

---

## Task 7: Verify Middleware Method Change Propagates to Endpoint

**Files:**

- Modify: `internal/app/pipeline_test.go`
- Add diff: `testdata/diffs/middleware-selector.diff`

- [ ] **Step 1: Add middleware method diff**

Create diff that changes the body or signature span of:

```go
func (Auth) Middleware(...)
```

- [ ] **Step 2: Write failing end-to-end impact test**

Assert impact reaches route endpoint:

```go
func TestRunImpactMapsMiddlewareSelectorMethodChangeToEndpoint(t *testing.T) {
    root := absFixture(t, "middleware-selector")
    diffPath := absTestdata(t, "diffs/middleware-selector.diff")
    got, err := RunImpact(ImpactOptions{ProjectPath: root, DiffPath: diffPath, Format: "json"})
    if err != nil {
        t.Fatal(err)
    }
    var doc output.ImpactDocument
    if err := json.Unmarshal(got, &doc); err != nil {
        t.Fatal(err)
    }
    assertEndpointSummary(t, doc, "GET", "/orders")
}
```

- [ ] **Step 3: Run focused test**

Run:

```bash
GOCACHE=/private/tmp/go-build-go-analyzer go test ./internal/app -run TestRunImpactMapsMiddlewareSelectorMethodChangeToEndpoint -v
```

Expected: PASS after Tasks 5-6.

---

## Task 8: Output Contract, Golden, and Docs

**Files:**

- Modify: `internal/output/contract.go`
- Modify: `docs/contracts/output-contract.md`
- Modify: `docs/validation/real-project-validation.md`
- Modify: `HANDOFF.md`
- Optional: `testdata/golden/*.impact.json`

- [ ] **Step 1: Update schema docs**

Document new change kinds and diagnostics:

- `route_deleted`
- `deleted_route_unresolved`
- `deleted_route_handler_unresolved`
- `deleted_route_endpoint_fallback`
- `module_diff_unresolved`

- [ ] **Step 2: Add golden output if JSON shape changes**

If output only uses existing shape with new node kinds, update or add fixture golden tests.

- [ ] **Step 3: Update HANDOFF**

Record:

- deleted route registration supported.
- go.mod diff to endpoint supported for local import usage.
- lightweight receiver/type inference supported for package-level var selector methods.
- double snapshot and SSA still deferred.

- [ ] **Step 4: Run output tests**

Run:

```bash
GOCACHE=/private/tmp/go-build-go-analyzer go test ./internal/output -v
```

Expected: PASS.

---

## Task 9: Smoke and Full Verification

**Files:**

- Modify: `scripts/smoke-real-projects.sh`
- Modify: `docs/validation/real-project-validation.md`

- [ ] **Step 1: Extend smoke script**

Add fixture smoke for:

- deleted-route
- gomod-impact
- middleware-selector

Assertions:

```bash
jq -e '.fileSources[].impactedEndpoints[] | select(.method=="GET" and .path=="/orders")'
```

- [ ] **Step 2: Run full test suite**

Run:

```bash
GOCACHE=/private/tmp/go-build-go-analyzer go test -count=1 ./...
```

Expected: PASS.

- [ ] **Step 3: Run vet**

Run:

```bash
GOCACHE=/private/tmp/go-build-go-analyzer go vet ./...
```

Expected: PASS with no output.

- [ ] **Step 4: Run formatting and whitespace checks**

Run:

```bash
gofmt -l .
git diff --check
```

Expected: no output.

- [ ] **Step 5: Run smoke**

Run:

```bash
bash scripts/smoke-real-projects.sh
```

Expected:

- real project facts smoke still succeeds.
- deleted-route fixture reports endpoint.
- gomod-impact fixture reports endpoint.
- middleware-selector fixture reports endpoint.

- [ ] **Step 6: Commit**

Commit only after all verification passes:

```bash
git add -A
git commit -m "feat: extend impact analysis coverage"
```
