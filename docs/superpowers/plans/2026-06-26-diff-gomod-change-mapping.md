# Diff And go.mod Change Mapping Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Parse git diffs, map changed line ranges to semantic facts, parse `go.mod` dependency changes, and map changed modules to local usage facts.

**Architecture:** This module creates change facts but does not decide impacted endpoints. It maps raw diff input to existing symbols/routes/annotations/middleware/module usage facts so the graph and impact module can consume a uniform `ChangeFact` stream.

**Tech Stack:** Go standard library, `golang.org/x/mod/modfile` if added to `go.mod`, existing facts/index/link/reference modules, fixture-based tests.

---

## Context

Read first:

- `docs/design/go-analyzer-mvp-architecture.md`
- `visanal/todo.md` for native Go module APIs
- `../visanal/pkg/modanal/graph/graph.go` for module dependency product context

## File Structure

Create:

- `internal/diff/parser.go`
- `internal/diff/range.go`
- `internal/diff/mapper.go`
- `internal/extract/gomod/extractor.go`
- `internal/extract/gomod/usage.go`
- `internal/facts/module.go`
- `internal/facts/change.go`

Fixtures:

- `testdata/fixtures/gomod-change`
- `testdata/fixtures/gomod-precise`
- `testdata/fixtures/gomod-file-fallback`
- `testdata/fixtures/gomod-unreferenced`

Tests:

- `internal/diff/parser_test.go`
- `internal/diff/mapper_test.go`
- `internal/extract/gomod/extractor_test.go`

## Tasks

### Task 1: Unified Diff Parser

**Files:**

- Create: `internal/diff/parser.go`
- Create: `internal/diff/range.go`
- Test: `internal/diff/parser_test.go`

- [ ] **Step 1: Write failing parser test**

Use a diff with:

```diff
diff --git a/controller/common.go b/controller/common.go
@@ -10,6 +10,8 @@
```

Assert changed file path and new-file line range.

- [ ] **Step 2: Implement parser**

Implement:

```go
func ParseUnified(input []byte) ([]FileChange, error)
```

Return:

- old path
- new path
- added/modified/deleted status
- changed new-file ranges

- [ ] **Step 3: Run tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./internal/diff -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/diff
git commit -m "feat: parse unified diff ranges"
```

### Task 2: ChangeFact Model And Range Mapping

**Files:**

- Create: `internal/facts/change.go`
- Create: `internal/diff/mapper.go`
- Modify: `internal/facts/store.go`
- Modify: `internal/output/schema.go`
- Test: `internal/diff/mapper_test.go`

- [ ] **Step 1: Write failing mapper tests**

Create changes hitting:

- function body
- route registration line
- `group.Use(...)` line
- annotation comment line

Assert `ChangeFact.Kind`:

- `method_body_changed`
- `route_registration_changed`
- `middleware_binding_changed`
- `annotation_changed`

- [ ] **Step 2: Implement `ChangeFact`**

Fields:

- ID
- Kind
- TargetID
- SymbolID
- File
- Ranges
- Source
- Confidence

- [ ] **Step 3: Implement range-to-fact mapper**

Use source spans from facts:

- exact containing span wins
- route/middleware/annotation facts are checked before enclosing function
- fallback to file-level diagnostic if no fact contains the range

- [ ] **Step 4: Run tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./internal/diff ./internal/facts ./internal/output -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/diff internal/facts internal/output
git commit -m "feat: map diff ranges to change facts"
```

### Task 3: go.mod Dependency Parser

**Files:**

- Create: `internal/extract/gomod/extractor.go`
- Create: `internal/facts/module.go`
- Test: `internal/extract/gomod/extractor_test.go`

- [ ] **Step 1: Write failing go.mod parser test**

Fixture with:

```go
require (
	github.com/gin-gonic/gin v1.10.0
	gopkg.inshopline.com/commons/lego/core v1.4.4
)
```

Assert `ModuleDependencyFact` for direct/indirect dependencies.

- [ ] **Step 2: Add dependency if needed**

If using `golang.org/x/mod/modfile`, add it to `go.mod`.

- [ ] **Step 3: Implement current go.mod extraction**

Extract:

- module path
- version
- indirect flag
- replace target if present

- [ ] **Step 4: Run tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./internal/extract/gomod -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/extract/gomod internal/facts/module.go testdata/fixtures/gomod-change
git commit -m "feat: extract go module dependencies"
```

### Task 4: go.mod Diff Change Detection

**Files:**

- Modify: `internal/extract/gomod/extractor.go`
- Test: `internal/extract/gomod/extractor_test.go`

- [ ] **Step 1: Write failing module change tests**

Cover:

- added
- removed
- upgraded
- downgraded
- replaced

- [ ] **Step 2: Implement old/new go.mod comparison**

Implement:

```go
func DiffModules(oldMod, newMod []byte) ([]facts.ModuleChangeFact, error)
```

- [ ] **Step 3: Run tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./internal/extract/gomod -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/extract/gomod
git commit -m "feat: detect go module dependency changes"
```

### Task 5: Module Usage Mapping

**Files:**

- Create: `internal/extract/gomod/usage.go`
- Test: `internal/extract/gomod/extractor_test.go`

- [ ] **Step 1: Write failing precise usage test**

Changed module:

```text
gopkg.inshopline.com/sc1/commons/utils
```

Local import:

```go
import "gopkg.inshopline.com/sc1/commons/utils/jsonx"
```

Assert `ModuleUsageFact.Basis == "module_reference_precise"` when a declaration references the import alias.

- [ ] **Step 2: Write failing file fallback test**

If imported module exists but symbol-level usage cannot be resolved, assert:

```text
module_reference_file_fallback
```

and map to declarations in that file.

- [ ] **Step 3: Write failing unreferenced test**

Changed module with no local imports produces:

```text
module_unreferenced
```

- [ ] **Step 4: Implement usage mapper**

For each changed module:

- find imports equal to module or with `module + "/"` prefix
- try alias usage within declarations
- fallback to declarations in importing file
- emit unreferenced when no imports match

- [ ] **Step 5: Run tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./internal/extract/gomod -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/extract/gomod testdata/fixtures/gomod-precise testdata/fixtures/gomod-file-fallback testdata/fixtures/gomod-unreferenced
git commit -m "feat: map module changes to local usage"
```

## Completion Criteria

- Unified diff parser produces stable file ranges.
- Changed ranges map to `ChangeFact`.
- `go.mod` changes produce `ModuleChangeFact`.
- Module changes map to local usage facts with explicit basis.
- No impact endpoint decision is implemented here.
