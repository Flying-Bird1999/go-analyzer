# Graph And Impact Propagation Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build reverse reference, route domain, and evidence graphs, then propagate change facts to impacted annotation endpoints.

**Architecture:** This module consumes facts and links produced by earlier modules. It builds graph indexes without mutating source facts, then uses explicit propagation rules to produce impacted endpoints with evidence chains. Endpoint truth remains annotation-first.

**Tech Stack:** Go standard library maps/slices, existing facts/change/reference/route/link models, golden tests.

---

## Context

Read first:

- `docs/design/go-analyzer-mvp-architecture.md`
- `docs/design/go-bff-impact-analysis-design.md` sections 6-10

## File Structure

Create:

- `internal/graph/reverse.go`
- `internal/graph/route.go`
- `internal/graph/evidence.go`
- `internal/impact/analyzer.go`
- `internal/impact/propagation.go`
- `internal/impact/endpoint.go`
- `internal/output/impact.go`

Fixtures:

- `testdata/fixtures/reference-chain`
- `testdata/fixtures/middleware-order`
- `testdata/fixtures/route-group-prefix`
- `testdata/fixtures/gomod-precise`

Tests:

- `internal/graph/graph_test.go`
- `internal/impact/analyzer_test.go`
- `internal/output/impact_test.go`

## Tasks

### Task 1: Reverse Reference Graph

**Files:**

- Create: `internal/graph/reverse.go`
- Test: `internal/graph/graph_test.go`

- [ ] **Step 1: Write failing reverse graph test**

Input references:

```text
controller.CheckIn -> service.WebApiForwardGray
route.registration -> controller.CheckIn
```

Assert reverse lookup of `service.WebApiForwardGray` returns `controller.CheckIn`.

- [ ] **Step 2: Implement graph**

Implement:

```go
type ReverseGraph struct {
	ByTarget map[facts.SymbolID][]facts.ReferenceFact
}
```

Add stable sorting by source ID and span.

- [ ] **Step 3: Run graph tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./internal/graph -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/graph
git commit -m "feat: build reverse reference graph"
```

### Task 2: Route Domain Graph

**Files:**

- Create: `internal/graph/route.go`
- Test: `internal/graph/graph_test.go`

- [ ] **Step 1: Write failing route graph test**

Input:

- one route group
- one middleware binding at statement 2
- routes at statements 1 and 3

Assert middleware binding affects only route at statement 3.

- [ ] **Step 2: Implement route graph**

Index:

- route group -> route registrations
- route group -> middleware bindings
- handler symbol -> route registrations
- handler symbol -> annotations
- middleware symbol -> bindings

- [ ] **Step 3: Implement statement order application**

For same group:

```text
binding.StatementIndex < route.StatementIndex
```

means binding affects route.

- [ ] **Step 4: Run tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./internal/graph -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/graph/route.go
git commit -m "feat: build route domain graph"
```

### Task 3: Evidence Graph

**Files:**

- Create: `internal/graph/evidence.go`
- Test: `internal/graph/graph_test.go`

- [ ] **Step 1: Write failing evidence chain test**

Expected chain:

```text
changed service method
  -> controller reference
  -> route registration
  -> annotation endpoint
```

- [ ] **Step 2: Implement evidence model**

Include:

- nodes
- edges
- source fact IDs
- source spans
- reason strings

- [ ] **Step 3: Run tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./internal/graph -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/graph/evidence.go
git commit -m "feat: model impact evidence chains"
```

### Task 4: Symbol Change Propagation

**Files:**

- Create: `internal/impact/analyzer.go`
- Create: `internal/impact/propagation.go`
- Create: `internal/impact/endpoint.go`
- Test: `internal/impact/analyzer_test.go`

- [ ] **Step 1: Write failing service change test**

Change fact targets service method. Assert impacted endpoint from controller annotation.

- [ ] **Step 2: Write failing controller change test**

Change fact targets controller method. Assert direct route registration and annotation endpoint.

- [ ] **Step 3: Implement propagation BFS**

Rules:

- start from changed symbol
- follow reverse references
- stop when reaching route handler or route registration
- resolve handler annotation endpoint
- collect evidence chain

- [ ] **Step 4: Run impact tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./internal/impact -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/impact
git commit -m "feat: propagate symbol changes to endpoints"
```

### Task 5: Route And Middleware Propagation

**Files:**

- Modify: `internal/impact/propagation.go`
- Test: `internal/impact/analyzer_test.go`

- [ ] **Step 1: Write failing route registration change test**

Change fact targets a route registration. Assert impacted endpoint is handler annotation.

- [ ] **Step 2: Write failing route group prefix test**

Change fact targets route group. Assert all route registrations under group are impacted.

- [ ] **Step 3: Write failing middleware binding test**

Change fact targets middleware binding. Assert only later routes are impacted.

- [ ] **Step 4: Implement route-domain propagation**

Map:

- route registration -> handler -> annotation
- route group -> route registrations
- middleware binding -> affected route registrations
- middleware function -> binding sites -> affected route registrations

- [ ] **Step 5: Run tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./internal/impact -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/impact testdata/fixtures/route-group-prefix
git commit -m "feat: propagate route context changes"
```

### Task 6: Module Change Propagation And Output

**Files:**

- Modify: `internal/impact/propagation.go`
- Create: `internal/output/impact.go`
- Test: `internal/impact/analyzer_test.go`
- Test: `internal/output/impact_test.go`

- [ ] **Step 1: Write failing module precise impact test**

Module usage maps to local declaration. Assert propagation reaches endpoint through reverse graph.

- [ ] **Step 2: Write failing unreferenced module test**

Unreferenced module change produces no endpoint but keeps diagnostic or module result with basis.

- [ ] **Step 3: Implement module propagation**

For `ModuleUsageFact`:

- precise: start from usage symbol
- file fallback: start from declarations in file
- unreferenced: output no impacted endpoints with basis

- [ ] **Step 4: Implement impact JSON output**

Shape:

```json
{
  "impactedEndpoints": [],
  "evidenceChains": [],
  "diagnostics": []
}
```

- [ ] **Step 5: Run full tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/impact internal/output
git commit -m "feat: output impacted endpoints with evidence"
```

## Completion Criteria

- Service, controller, route registration, route group, middleware binding/function, and go.mod changes can produce endpoint impacts.
- Every impacted endpoint has an evidence chain.
- Endpoint method/path comes from annotation facts.
- Route facts remain evidence, not endpoint truth.
