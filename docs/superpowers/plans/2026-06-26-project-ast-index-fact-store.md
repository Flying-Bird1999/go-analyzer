# Project AST Index And Fact Store Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `go-analyzer` project skeleton, project loader, lightweight AST index, fact store, and stable JSON facts output.

**Architecture:** This module is the foundation for every later module. It loads a Go module from disk, parses Go files with comments, builds package/file/symbol indexes, stores facts in a central `FactStore`, and renders deterministic JSON. It must not contain annotation, route, reference, diff, or impact-specific logic.

**Tech Stack:** Go 1.24+, standard library `go/ast`, `go/parser`, `go/token`, `encoding/json`, golden tests with `go test`.

---

## Context

Read first:

- `docs/design/go-analyzer-mvp-architecture.md`
- `docs/design/go-bff-impact-analysis-design.md`
- `../nexus/internal/transform/bff/openapi/project.go`
- `../nexus/internal/transform/go-sdk/collector/scanner.go`

This plan creates the minimal runnable analyzer. Later plans depend on the stable APIs created here.

## File Structure

Create:

- `go.mod`: module definition for `gopkg.inshopline.com/bff/go-analyzer`.
- `cmd/go-analyzer/main.go`: CLI entrypoint.
- `internal/app/options.go`: pipeline options.
- `internal/app/pipeline.go`: top-level orchestration for facts mode.
- `internal/project/module.go`: `go.mod` module path reader.
- `internal/project/loader.go`: project loader and file scanner.
- `internal/project/package.go`: project/package/file data types.
- `internal/astindex/index.go`: declaration indexing.
- `internal/astindex/symbol.go`: symbol ID creation.
- `internal/astindex/position.go`: `token.Pos` to source span helpers.
- `internal/facts/id.go`: fact and symbol ID types.
- `internal/facts/source.go`: source location types.
- `internal/facts/symbol.go`: `SymbolFact` model.
- `internal/facts/store.go`: central fact store.
- `internal/output/schema.go`: root JSON output schema.
- `internal/output/json.go`: deterministic JSON renderer.
- `testdata/fixtures/mini-bff/go.mod`: minimal fixture module.
- `testdata/fixtures/mini-bff/router/router.go`: fixture route shell.
- `testdata/fixtures/mini-bff/controller/common.go`: fixture controller shell.
- `testdata/fixtures/mini-bff/service/common.go`: fixture service shell.

Test:

- `internal/project/module_test.go`
- `internal/project/loader_test.go`
- `internal/astindex/index_test.go`
- `internal/output/json_test.go`

## Tasks

### Task 1: Module And CLI Skeleton

**Files:**

- Create: `go.mod`
- Create: `cmd/go-analyzer/main.go`
- Create: `internal/app/options.go`
- Create: `internal/app/pipeline.go`

- [ ] **Step 1: Write a failing CLI smoke test**

Create `internal/app/pipeline_test.go`:

```go
package app

import "testing"

func TestRunFactsOnMiniBFFReturnsProjectMetadata(t *testing.T) {
	t.Skip("unskip after fixture and loader exist")
}
```

- [ ] **Step 2: Create `go.mod`**

Use:

```go
module gopkg.inshopline.com/bff/go-analyzer

go 1.24
```

- [ ] **Step 3: Create minimal CLI**

`cmd/go-analyzer/main.go` should parse:

- `facts`
- `--project`
- `--format json`

For this task, return a clear error for unsupported commands.

- [ ] **Step 4: Run smoke build**

Run:

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./...
```

Expected: PASS or only skipped placeholder tests.

- [ ] **Step 5: Commit**

```bash
git add go.mod cmd/go-analyzer internal/app
git commit -m "chore: scaffold go analyzer cli"
```

### Task 2: Project Loader

**Files:**

- Create: `internal/project/module.go`
- Create: `internal/project/loader.go`
- Create: `internal/project/package.go`
- Test: `internal/project/module_test.go`
- Test: `internal/project/loader_test.go`
- Create fixture files under `testdata/fixtures/mini-bff`

- [ ] **Step 1: Write failing module path test**

```go
func TestReadModulePath(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "mini-bff")
	got, err := ReadModulePath(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != "example.com/mini-bff" {
		t.Fatalf("module path = %q", got)
	}
}
```

- [ ] **Step 2: Create mini fixture**

Create `testdata/fixtures/mini-bff/go.mod`:

```go
module example.com/mini-bff

go 1.24
```

Add small router/controller/service files with valid packages.

- [ ] **Step 3: Implement module reader**

Implement `ReadModulePath(root string) (string, error)` using `os.Open`, `bufio.Scanner`, and `strings.TrimSpace`.

- [ ] **Step 4: Write failing loader test**

Assert:

- module path is `example.com/mini-bff`
- `_test.go` files are skipped
- package import path is module path plus relative directory
- imports are captured by alias

- [ ] **Step 5: Implement loader**

`Load(root string, opts Options) (*Project, error)` should:

- resolve absolute root
- read module path
- walk files
- skip `.git`, `vendor`, `node_modules`, `testdata` below module root
- skip `_test.go`
- parse with `parser.ParseComments`

- [ ] **Step 6: Run tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./internal/project -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/project testdata/fixtures/mini-bff
git commit -m "feat: load go project files"
```

### Task 3: AST Symbol Index

**Files:**

- Create: `internal/astindex/index.go`
- Create: `internal/astindex/symbol.go`
- Create: `internal/astindex/position.go`
- Create: `internal/facts/id.go`
- Create: `internal/facts/source.go`
- Create: `internal/facts/symbol.go`
- Test: `internal/astindex/index_test.go`

- [ ] **Step 1: Write failing symbol index test**

Test a fixture with:

- package-level const
- package-level var
- named type
- function
- receiver method

Assert stable symbol IDs such as:

```text
func:example.com/mini-bff/service::CheckIn
method:example.com/mini-bff/controller:CommonController:CheckIn
```

- [ ] **Step 2: Implement source span conversion**

`SourceSpanFor(fset *token.FileSet, start, end token.Pos) facts.SourceSpan`.

- [ ] **Step 3: Implement symbol ID helpers**

Create helpers:

- `FunctionSymbolID(pkgPath, name string)`
- `MethodSymbolID(pkgPath, receiver, name string)`
- `TypeSymbolID(pkgPath, name string)`
- `ValueSymbolID(kind, pkgPath, name string)`

- [ ] **Step 4: Implement index builder**

`Build(project *project.Project) (*Index, error)` should populate:

- packages
- files
- funcs
- methods
- types
- vars
- consts
- symbol facts

- [ ] **Step 5: Run tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./internal/astindex ./internal/facts -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/astindex internal/facts
git commit -m "feat: index go declarations as symbols"
```

### Task 4: Fact Store And Deterministic JSON Output

**Files:**

- Create: `internal/facts/store.go`
- Create: `internal/output/schema.go`
- Create: `internal/output/json.go`
- Test: `internal/output/json_test.go`
- Modify: `internal/app/pipeline.go`

- [ ] **Step 1: Write failing output test**

Load `mini-bff`, build symbols, render JSON twice, and assert:

- both outputs are byte-identical
- project metadata exists
- symbols are sorted by ID

- [ ] **Step 2: Implement `FactStore`**

Include:

- `Project`
- `Symbols`
- placeholders for `Annotations`, `Routes`, `References`, `Modules`, `Links`, `Diagnostics`

Only `Symbols` needs real data in this module.

- [ ] **Step 3: Implement JSON renderer**

Use `json.MarshalIndent` after sorting every slice.

- [ ] **Step 4: Wire pipeline**

`app.RunFacts(opts)` should:

- load project
- build AST index
- create fact store
- render JSON

- [ ] **Step 5: Run full tests**

```bash
cd /Users/bird/Desktop/go-analyzer-factory/go-analyzer
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/app internal/facts internal/output
git commit -m "feat: output deterministic fact inventory"
```

## Completion Criteria

- `go test ./...` passes.
- `go run ./cmd/go-analyzer facts --project testdata/fixtures/mini-bff --format json` prints valid JSON.
- JSON includes project metadata and symbol facts.
- No annotation, route, reference, diff, or impact logic is implemented in this module.
