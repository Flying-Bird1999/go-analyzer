# Handler Linking And Reference Extraction Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Link route handler expressions to stable function/method symbols and extract source-level references for reverse dependency analysis.

**Architecture:** This module sits between raw facts and graph construction. It resolves handler expressions such as `uc.MerchantSettingApi.UpdateSubMerchantSettingByCode`, creates semantic links between route and annotation facts, and extracts call/selector/type/value reference facts from function bodies.

**Tech Stack:** Go AST standard library, existing project/astindex/facts/extract packages, deterministic JSON output, unit and fixture tests.

---

## Context

Read first:

- `docs/design/go-analyzer-mvp-architecture.md`
- `../nexus/internal/transform/bff/openapi/project.go`
- `../nexus/internal/transform/bff/openapi/grpc_dependency.go`

## File Structure

Create:

- `internal/link/symbol.go`
- `internal/link/handler.go`
- `internal/link/route.go`
- `internal/link/linker.go`
- `internal/extract/reference/extractor.go`
- `internal/extract/reference/callee.go`
- `internal/extract/reference/localvars.go`
- `internal/facts/reference.go`
- `internal/facts/link.go`

Fixtures:

- `testdata/fixtures/handler-method-var`
- `testdata/fixtures/reference-chain`
- `testdata/fixtures/utility-fanout`

Tests:

- `internal/link/linker_test.go`
- `internal/extract/reference/extractor_test.go`

## Tasks

### Task 1: Link Models And Store Support

**Files:**

- Create: `internal/facts/link.go`
- Create: `internal/facts/reference.go`
- Modify: `internal/facts/store.go`
- Modify: `internal/output/schema.go`

- [ ] **Step 1: Write failing JSON output test**

Construct a `FactStore` with one `ReferenceFact` and one `LinkFact`, render JSON, and assert deterministic ordering.

- [ ] **Step 2: Implement models**

Add:

- `ReferenceFact`
- `ReferenceKind`
- `Confidence`
- `LinkFact`
- `LinkKind`

- [ ] **Step 3: Update output schema**

Include references and links in root JSON.

- [ ] **Step 4: Run tests**

```bash
cd go-analyzer
go test ./internal/facts ./internal/output -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/facts internal/output
git commit -m "feat: add reference and link fact models"
```

### Task 2: Handler Symbol Linking

**Files:**

- Create: `internal/link/symbol.go`
- Create: `internal/link/handler.go`
- Test: `internal/link/linker_test.go`

- [ ] **Step 1: Write failing function handler test**

Fixture:

```go
group.POST("/checkIn", common.CheckIn)
```

Assert handler raw `common.CheckIn` links to:

```text
func:example.com/fixture/controller/common::CheckIn
```

- [ ] **Step 2: Write failing receiver var handler test**

Fixture:

```go
var MerchantSettingApi = &merchantSettingApi{}
group.POST("/x", uc.MerchantSettingApi.UpdateSubMerchantSettingByCode)
```

Assert handler links to:

```text
method:<pkg>:merchantSettingApi:UpdateSubMerchantSettingByCode
```

- [ ] **Step 3: Implement package alias resolution**

Use file imports to resolve `common` / `uc` aliases to package paths.

- [ ] **Step 4: Implement package-level var receiver inference**

Use AST index vars:

- composite literal `&merchantSettingApi{}`
- explicit type `var X *merchantSettingApi`
- constructor return type when available

- [ ] **Step 5: Run link tests**

```bash
cd go-analyzer
go test ./internal/link -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/link testdata/fixtures/handler-method-var
git commit -m "feat: link route handlers to symbols"
```

### Task 3: Route And Annotation Linking

**Files:**

- Create: `internal/link/route.go`
- Create: `internal/link/linker.go`
- Test: `internal/link/linker_test.go`

- [ ] **Step 1: Write failing route-to-annotation test**

Fixture has:

- route registration referencing handler
- annotation on same handler

Assert links:

- `route_to_handler`
- `handler_to_annotation`

- [ ] **Step 2: Implement linker orchestration**

`link.Run(index, store)` should:

- resolve handler symbol for route facts
- update route `HandlerSymbol`
- create route-to-handler links
- create handler-to-annotation links

- [ ] **Step 3: Preserve no-judgment rule**

If route method and annotation method differ, still link by handler and do not emit mismatch diagnostic in MVP.

- [ ] **Step 4: Run tests**

```bash
cd go-analyzer
go test ./internal/link ./internal/extract/... -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/link
git commit -m "feat: link routes handlers and annotations"
```

### Task 4: Reference Extraction

**Files:**

- Create: `internal/extract/reference/extractor.go`
- Create: `internal/extract/reference/callee.go`
- Create: `internal/extract/reference/localvars.go`
- Test: `internal/extract/reference/extractor_test.go`

- [ ] **Step 1: Write failing service reference test**

Fixture:

```go
func CheckIn(...) (...) {
	return commonService.WebApiForwardGray(ctx, merchantID)
}
```

Assert a `ReferenceFact` from controller symbol to service function symbol.

- [ ] **Step 2: Write failing method call reference test**

Fixture:

```go
uc.MerchantSettingService.UpdateSubMerchantSettingByCode(ctx, id, req)
```

Assert method call resolves to receiver method symbol when package-level var receiver is known.

- [ ] **Step 3: Implement call extraction**

Walk function bodies and extract:

- `pkg.Func(...)`
- `var.Method(...)`
- `receiver.Method(...)`
- function identifier calls in same package

- [ ] **Step 4: Implement selector and type refs**

Add low-confidence refs when precise symbol resolution is unavailable.

- [ ] **Step 5: Run reference tests**

```bash
cd go-analyzer
go test ./internal/extract/reference -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/extract/reference testdata/fixtures/reference-chain
git commit -m "feat: extract source reference facts"
```

### Task 5: Fan-Out Fixture And Pipeline Integration

**Files:**

- Modify: `internal/app/pipeline.go`
- Test: `internal/app/pipeline_test.go`
- Create: `testdata/fixtures/utility-fanout`

- [ ] **Step 1: Write failing pipeline test**

Run full facts pipeline and assert:

- route facts have linked handler symbols
- annotation links exist
- reference facts exist

- [ ] **Step 2: Wire link and reference extraction into facts pipeline**

Pipeline order:

```text
load project
build ast index
extract symbols
extract annotations
extract routes
link route/handler/annotation
extract references
render JSON
```

- [ ] **Step 3: Run full tests**

```bash
cd go-analyzer
go test ./...
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/app testdata/fixtures/utility-fanout
git commit -m "feat: integrate handler linking and references"
```

## Completion Criteria

- Route handler raw expressions link to stable function/method symbols.
- Annotation facts link to handler symbols.
- Reference facts express controller -> service -> remote/util calls.
- JSON output includes symbols, annotations, routes, links, and references.
- No graph traversal or impact propagation is implemented here.
