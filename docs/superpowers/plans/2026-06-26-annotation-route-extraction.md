# Annotation And Route Extraction Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract controller endpoint annotations, route registrations, route groups, middleware bindings, and wrapper stacks as code facts.

**Architecture:** This module consumes `project.Project`, `astindex.Index`, and `facts.FactStore` from the foundation module. It adds extractor packages for annotations and routes, but it does not perform handler linking, reference extraction, diff mapping, or impact propagation.

**Tech Stack:** Go AST standard library, existing `internal/project`, `internal/astindex`, `internal/facts`, golden tests with `go test`.

---

## Context

Read first:

- `docs/design/go-analyzer-mvp-architecture.md`
- `../nexus/internal/transform/bff/openapi/controller_route.go`
- `../nexus/internal/transform/bff/openapi/router.go`
- `../nexus/internal/transform/bff/openapi/astutil.go`

## File Structure

Create:

- `internal/extract/annotation/parser.go`
- `internal/extract/annotation/extractor.go`
- `internal/extract/route/extractor.go`
- `internal/extract/route/context.go`
- `internal/extract/route/handler.go`
- `internal/extract/route/wrapper.go`
- `internal/extract/route/astutil.go`
- `internal/config/config.go`
- `internal/config/defaults.go`
- `internal/facts/annotation.go`
- `internal/facts/route.go`

Fixtures:

- `testdata/fixtures/annotation-only`
- `testdata/fixtures/controller-wrapper`
- `testdata/fixtures/route-wrapper`
- `testdata/fixtures/generated-nexus`
- `testdata/fixtures/middleware-order`
- `testdata/fixtures/dynamic-route-path`

Tests:

- `internal/extract/annotation/extractor_test.go`
- `internal/extract/route/extractor_test.go`

## Tasks

### Task 1: Annotation Parser

**Files:**

- Create: `internal/extract/annotation/parser.go`
- Create: `internal/facts/annotation.go`
- Test: `internal/extract/annotation/extractor_test.go`

- [ ] **Step 1: Write failing parser tests**

Cover:

- `// @Get /path`
- `// @Post path-without-leading-slash`
- multiple annotations on one function
- non-endpoint annotations like `@Refactor` ignored

- [ ] **Step 2: Implement parser**

Implement:

```go
func ParseAPIAnnotations(doc *ast.CommentGroup) []ParsedAnnotation
```

Rules:

- method normalized to uppercase
- path minimally normalized by adding `/` if missing
- raw comment line preserved
- no route consistency checks

- [ ] **Step 3: Run annotation tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./internal/extract/annotation -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/extract/annotation internal/facts/annotation.go
git commit -m "feat: parse endpoint annotations"
```

### Task 2: Annotation Extractor

**Files:**

- Create: `internal/extract/annotation/extractor.go`
- Modify: `internal/facts/store.go`
- Modify: `internal/output/schema.go`
- Test: `internal/extract/annotation/extractor_test.go`

- [ ] **Step 1: Write failing extraction fixture test**

Use `testdata/fixtures/annotation-only` with:

```go
// @Get /api/bff-web/common/checkIn
func CheckIn(...) (...) {}
```

Assert one `AnnotationFact` with `HandlerSymbol`.

- [ ] **Step 2: Implement extractor**

`Extract(project, index, store)` should:

- traverse all functions and methods
- parse annotations
- map enclosing function/method to symbol ID
- append `AnnotationFact`

- [ ] **Step 3: Add output support**

Include annotations in JSON root output, sorted by ID.

- [ ] **Step 4: Run tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./internal/extract/annotation ./internal/output -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/extract/annotation internal/facts internal/output testdata/fixtures/annotation-only
git commit -m "feat: extract annotation facts"
```

### Task 3: Route Registration Extractor

**Files:**

- Create: `internal/extract/route/astutil.go`
- Create: `internal/extract/route/extractor.go`
- Create: `internal/extract/route/context.go`
- Create: `internal/facts/route.go`
- Test: `internal/extract/route/extractor_test.go`

- [ ] **Step 1: Write failing direct route test**

Fixture:

```go
func InitRouter(g *lego.RouterGroup) {
	group := g.Group("/api/bff-web/common")
	group.POST("/checkIn", common.CheckIn)
}
```

Assert:

- one `RouteGroupFact`
- one `RouteRegistrationFact`
- method `POST`
- local path `/checkIn`
- resolved path `/api/bff-web/common/checkIn`
- handler raw `common.CheckIn`

- [ ] **Step 2: Implement route AST utilities**

Implement:

- `exprString`
- `stringLiteral`
- `selectorParts`
- `callName`
- `joinPath`
- `isHTTPMethod`

- [ ] **Step 3: Implement group context tracking**

Track:

- first route function parameter as root group
- `g.Group("/prefix")`
- `group.Group("/child")`
- statement index

- [ ] **Step 4: Implement HTTP method call extraction**

Generate `RouteRegistrationFact` when seeing:

```go
group.GET/POST/PUT/DELETE/PATCH(path, handler)
```

- [ ] **Step 5: Run route tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./internal/extract/route -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/extract/route internal/facts/route.go testdata/fixtures/controller-wrapper
git commit -m "feat: extract route registration facts"
```

### Task 4: Wrapper And Middleware Facts

**Files:**

- Create: `internal/extract/route/handler.go`
- Create: `internal/extract/route/wrapper.go`
- Modify: `internal/extract/route/context.go`
- Test: `internal/extract/route/extractor_test.go`

- [ ] **Step 1: Write failing wrapper tests**

Cover:

```go
sa2.ControllerWithReqResp(common.CheckIn)
sa2.ControllerWithResp(common.CheckIn)
lego.MiddlewareController([]lego.MiddlewareFunc{m}, sa2.ControllerWithResp(common.CheckIn))
AddLiveReadGuard(group).GET("/statistics", handler)
```

Assert wrapper stack and final handler raw expression.

- [ ] **Step 2: Write failing middleware-order test**

Fixture:

```go
group.GET("/a", h1)
group.Use(middleware.Auth())
group.GET("/b", h2)
```

Assert middleware binding statement index is after route `/a` and before route `/b`.

- [ ] **Step 3: Implement wrapper unwrapping**

Rules:

- for `MiddlewareController`, handler is last argument
- for wrappers with middleware slice as first arg, handler is last arg
- preserve wrapper stack in outer-to-inner order

- [ ] **Step 4: Implement middleware binding facts**

Generate `MiddlewareBindingFact` for:

- `group.Use(...)`
- middleware args in `Group(...)` if present
- guard wrapper summary when simple

- [ ] **Step 5: Run tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./internal/extract/route -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/extract/route testdata/fixtures/route-wrapper testdata/fixtures/middleware-order
git commit -m "feat: extract wrappers and middleware bindings"
```

### Task 5: Generated Routes And Config

**Files:**

- Create: `internal/config/config.go`
- Create: `internal/config/defaults.go`
- Modify: `internal/extract/route/extractor.go`
- Test: `internal/extract/route/extractor_test.go`

- [ ] **Step 1: Write failing generated route test**

Fixture shape:

```go
func InitRouter(g *lego.RouterGroup) {
	apis.RegisterRouters(g)
}
```

`apis.RegisterRouters` calls generated package `RegisterRouter(g)`.

Assert generated route registration is extracted and source family is marked.

- [ ] **Step 2: Implement default config**

Include:

- route entry `router/router.go#InitRouter`
- HTTP methods
- handler wrappers
- generated route calls `RegisterRouters`, `RegisterRouter`
- skip dirs

- [ ] **Step 3: Implement nested router call traversal**

When route function calls another function with a known route group arg, visit callee with inherited route context.

- [ ] **Step 4: Run full extraction tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./internal/extract/annotation ./internal/extract/route ./internal/output -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config internal/extract/route testdata/fixtures/generated-nexus
git commit -m "feat: support configured route extraction"
```

## Completion Criteria

- Annotation facts and route facts render in facts JSON.
- `go test ./internal/extract/...` passes.
- Extractor records route group, middleware binding, wrapper stack, local path, resolved path when reliable, and raw expressions when not reliable.
- No handler symbol linking beyond raw package alias resolution is required here.
- No impact propagation is implemented here.
