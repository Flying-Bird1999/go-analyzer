# Reference Traversal Fusion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor function-body reference extraction to use one pre-scan context and one emission walk without changing analyzer output.

**Architecture:** `extractFuncReferences` remains the function-level coordinator. A new package-private `functionBodyContext` owns precomputed scoped receiver types and expression-position metadata; `extractFunctionBodyReferences` performs the single body emission walk and delegates symbol resolution to the existing `resolver`.

**Tech Stack:** Go AST (`go/ast`, `go/token`), existing `internal/astindex`, `internal/facts`, `internal/project`, and `internal/extract/reference` resolver helpers.

## Global Constraints

- Do not change `ReferenceFact` schema, IDs, confidence, evidence, or ordering semantics.
- Do not change interface binding diagnostics.
- Do not change resolver algorithms or selector resolution rules.
- Do not introduce `go/types`, SSA, concurrency, or new dependencies.
- Do not attempt flow-sensitive local variable reassignment.
- Do not refactor package-level initializer extraction beyond small helper reuse.
- Keep documentation paths relative; do not write absolute workspace paths into project docs.

---

## File Structure

- Modify `internal/extract/reference/extractor.go`: add function-body context and single emission walk; simplify `extractFuncReferences`.
- Modify `internal/extract/reference/values.go`: remove function-body-only `extractValueReferences` after its logic moves into the unified body walk; keep initializer helpers and resolver value helpers.
- Modify `internal/extract/reference/scoped_types.go`: add `collectFunctionBodyContext` or delegate from it to existing scoped-type collection.
- Modify `internal/extract/reference/extractor_test.go`: add a focused context seam test before implementation.
- Modify `ARCHITECTURE.md`: document the reference traversal boundary if implementation adds the context seam.

---

### Task 1: Capture Baseline And Add Context Seam Test

**Files:**
- Modify: `internal/extract/reference/extractor_test.go`
- Read: `internal/extract/reference/extractor.go`
- Read: `internal/extract/reference/values.go`
- Read: `internal/extract/reference/scoped_types.go`

**Interfaces:**
- Consumes existing test helpers:
  - `func findReferenceTestFile(t *testing.T, p *project.Project, rel string) *project.File`
- Produces a failing test for:
  - `func collectFunctionBodyContext(file *project.File, idx *astindex.Index, fn *ast.FuncDecl) functionBodyContext`
  - `type functionBodyContext struct { scopedTypes scopedValueTypes; ignored map[token.Pos]bool; callFuns map[token.Pos]bool }`

- [ ] **Step 1: Confirm clean worktree**

Run:

```bash
git status --short
```

Expected: no output, or only this plan file before it is committed.

- [ ] **Step 2: Run reference baseline tests**

Run:

```bash
go test -count=1 ./internal/extract/reference
```

Expected: package passes before refactor.

- [ ] **Step 3: Add failing context seam test**

Append this test near the other extractor tests in `internal/extract/reference/extractor_test.go`:

```go
func TestCollectFunctionBodyContextIndexesScopedTypesAndPositions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/reference-context\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(`package sample

type Payload struct{ ID string }
type Service struct{}

func (Service) Handle(Payload) {}

var Default Service

func NewService() Service { return Service{} }

func Use() {
	local := NewService()
	local.Handle(Payload{ID: "x"})
	Default.Handle(Payload{ID: "y"})
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	file := findReferenceTestFile(t, p, "main.go")
	var fn *ast.FuncDecl
	for _, decl := range file.AST.Decls {
		candidate, ok := decl.(*ast.FuncDecl)
		if ok && candidate.Name.Name == "Use" {
			fn = candidate
			break
		}
	}
	if fn == nil {
		t.Fatal("Use function not found")
	}

	ctx := collectFunctionBodyContext(file, idx, fn)
	var (
		localIdent *ast.Ident
		callFun    ast.Expr
		fieldKey   *ast.Ident
	)
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.AssignStmt:
			if len(x.Lhs) == 1 {
				localIdent, _ = x.Lhs[0].(*ast.Ident)
			}
		case *ast.CallExpr:
			if selector, ok := x.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Handle" && callFun == nil {
				callFun = x.Fun
			}
		case *ast.KeyValueExpr:
			if key, ok := x.Key.(*ast.Ident); ok && key.Name == "ID" && fieldKey == nil {
				fieldKey = key
			}
		}
		return true
	})
	if localIdent == nil || callFun == nil || fieldKey == nil {
		t.Fatalf("test fixture did not expose expected nodes: local=%v call=%v field=%v", localIdent, callFun, fieldKey)
	}
	valueTypes, ok := ctx.scopedTypes.resolveAll(localIdent, callFun.Pos())
	if !ok || len(valueTypes) != 1 || valueTypes[0].TypeName != "Service" {
		t.Fatalf("scoped type for local = %#v ok=%v, want Service", valueTypes, ok)
	}
	if !ctx.callFuns[callFun.Pos()] {
		t.Fatalf("call function position %v was not indexed", callFun.Pos())
	}
	if !ctx.ignored[fieldKey.Pos()] {
		t.Fatalf("composite literal key position %v was not ignored", fieldKey.Pos())
	}
}
```

- [ ] **Step 4: Run test to verify it fails before implementation**

Run:

```bash
go test -count=1 ./internal/extract/reference -run TestCollectFunctionBodyContextIndexesScopedTypesAndPositions -v
```

Expected: FAIL to compile with `undefined: collectFunctionBodyContext` or `undefined: functionBodyContext`.

---

### Task 2: Implement Function Body Context

**Files:**
- Modify: `internal/extract/reference/scoped_types.go`

**Interfaces:**
- Consumes:
  - `func collectScopedValueTypes(file *project.File, idx *astindex.Index, fn *ast.FuncDecl) scopedValueTypes`
  - `func ignoredValuePositions(root ast.Node) map[token.Pos]bool`
  - `func callFunPositions(root ast.Node) map[token.Pos]bool`
- Produces:
  - `type functionBodyContext struct`
  - `func collectFunctionBodyContext(file *project.File, idx *astindex.Index, fn *ast.FuncDecl) functionBodyContext`

- [ ] **Step 1: Add the context type and collector**

In `internal/extract/reference/scoped_types.go`, after `type scopedValueTypes struct`, add:

```go
type functionBodyContext struct {
	scopedTypes scopedValueTypes
	ignored     map[token.Pos]bool
	callFuns    map[token.Pos]bool
}

func collectFunctionBodyContext(file *project.File, idx *astindex.Index, fn *ast.FuncDecl) functionBodyContext {
	ctx := functionBodyContext{
		scopedTypes: collectScopedValueTypes(file, idx, fn),
		ignored:     map[token.Pos]bool{},
		callFuns:    map[token.Pos]bool{},
	}
	if fn == nil || fn.Body == nil {
		return ctx
	}
	ctx.ignored = ignoredValuePositions(fn.Body)
	ctx.callFuns = callFunPositions(fn.Body)
	return ctx
}
```

- [ ] **Step 2: Run the new test**

Run:

```bash
go test -count=1 ./internal/extract/reference -run TestCollectFunctionBodyContextIndexesScopedTypesAndPositions -v
```

Expected: PASS.

---

### Task 3: Fuse Function Body Emission Walk

**Files:**
- Modify: `internal/extract/reference/extractor.go`
- Modify: `internal/extract/reference/values.go`

**Interfaces:**
- Consumes:
  - `type functionBodyContext`
  - `func collectFunctionBodyContext(file *project.File, idx *astindex.Index, fn *ast.FuncDecl) functionBodyContext`
  - `func addCallReference(p *project.Project, file *project.File, idx *astindex.Index, store *facts.Store, from facts.SymbolID, scopedTypes scopedValueTypes, call *ast.CallExpr)`
  - `func addValueReferenceFacts(p *project.Project, file *project.File, store *facts.Store, from facts.SymbolID, expr ast.Expr, targets []facts.SymbolID)`
- Produces:
  - `func extractFunctionBodyReferences(p *project.Project, file *project.File, idx *astindex.Index, store *facts.Store, from facts.SymbolID, fn *ast.FuncDecl, ctx functionBodyContext)`

- [ ] **Step 1: Update `extractFuncReferences` to use the context**

In `internal/extract/reference/extractor.go`, replace:

```go
scopedTypes := collectScopedValueTypes(file, idx, fn)
```

with:

```go
bodyContext := collectFunctionBodyContext(file, idx, fn)
```

Then replace `scopedTypes` usages in the body extraction path with `bodyContext.scopedTypes`.

- [ ] **Step 2: Add unified body extraction function**

In `internal/extract/reference/extractor.go`, add this function below `extractFuncReferences`:

```go
func extractFunctionBodyReferences(p *project.Project, file *project.File, idx *astindex.Index, store *facts.Store, from facts.SymbolID, fn *ast.FuncDecl, ctx functionBodyContext) {
	if fn.Body == nil {
		return
	}
	resolver := newResolver(file, idx, scopedValueTypes{})
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.CallExpr:
			for _, typeArgument := range genericTypeArguments(x.Fun) {
				addTypeReferences(p, file, idx, store, from, typeArgument)
			}
			callee := unwrapGenericCallee(x.Fun)
			if len(collectTypeIDs(file, idx, callee)) > 0 {
				addTypeReferences(p, file, idx, store, from, callee)
			} else {
				addCallReference(p, file, idx, store, from, ctx.scopedTypes, x)
			}
		case *ast.CompositeLit:
			addTypeReferences(p, file, idx, store, from, x.Type)
		case *ast.SelectorExpr:
			if ctx.ignored[x.Pos()] {
				return false
			}
			var targets []facts.SymbolID
			if ctx.callFuns[x.Pos()] {
				targets = resolver.ResolveReceiverValueIDs(x)
			} else {
				targets = resolver.ResolveValueIDs(x)
			}
			addValueReferenceFacts(p, file, store, from, x, targets)
			return false
		case *ast.Ident:
			if ctx.ignored[x.Pos()] || ctx.callFuns[x.Pos()] || isLocalIdentifier(idx, x) {
				return true
			}
			addValueReferenceFacts(p, file, store, from, x, resolver.ResolveValueIDs(x))
		}
		return true
	})
}
```

- [ ] **Step 3: Replace the old function body walks**

In `extractFuncReferences`, remove:

```go
extractValueReferences(p, file, idx, store, from, fn)
ast.Inspect(fn.Body, func(node ast.Node) bool {
    ...
})
```

Replace it with:

```go
extractFunctionBodyReferences(p, file, idx, store, from, fn, bodyContext)
```

- [ ] **Step 4: Remove obsolete `extractValueReferences`**

Delete `func extractValueReferences(...)` from `internal/extract/reference/values.go`.

Keep these helpers in `values.go` because initializer extraction and unified body extraction still use them:

```text
func ignoredValuePositions(root ast.Node) map[token.Pos]bool
func callFunPositions(root ast.Node) map[token.Pos]bool
func addValueReferenceFacts(...)
func isLocalIdentifier(...)
func (r resolver) ResolveValueIDs(...)
func (r resolver) ResolveReceiverValueIDs(...)
```

- [ ] **Step 5: Format and run reference tests**

Run:

```bash
gofmt -w internal/extract/reference/extractor.go internal/extract/reference/values.go internal/extract/reference/scoped_types.go internal/extract/reference/extractor_test.go
go test -count=1 ./internal/extract/reference
```

Expected: PASS.

---

### Task 4: Update Architecture Documentation

**Files:**
- Modify: `ARCHITECTURE.md`

**Interfaces:**
- Consumes final extractor structure from Task 3.
- Produces documentation describing the traversal/resolver boundary.

- [ ] **Step 1: Locate the reference architecture section**

Run:

```bash
rg -n "internal/extract/reference|resolver|selector/call/value" ARCHITECTURE.md
```

Expected: output includes section `5.8 internal/extract/reference`.

- [ ] **Step 2: Add traversal boundary note**

Add this sentence to section `5.8 internal/extract/reference`:

```markdown
函数体提取先构建 `functionBodyContext`，再用一次 emission walk 同时处理 call/type/value 引用；`resolver` 仍然只负责候选符号解析和接口绑定诊断。
```

- [ ] **Step 3: Verify docs avoid absolute workspace paths**

Run:

```bash
workspace_root="$(git rev-parse --show-toplevel)"
workspace_parent="$(dirname "$workspace_root")"
rg -n "$workspace_root|$workspace_parent" ARCHITECTURE.md docs/superpowers/specs/2026-07-08-reference-traversal-fusion-design.md docs/superpowers/plans/2026-07-08-reference-traversal-fusion.md
```

Expected: no output.

---

### Task 5: Final Verification And Commit

**Files:**
- Verify all modified files.

**Interfaces:**
- Consumes Tasks 1-4.
- Produces one behavior-preserving refactor commit.

- [ ] **Step 1: Run formatting check**

Run:

```bash
gofmt -l $(git ls-files '*.go')
```

Expected: no output.

- [ ] **Step 2: Run focused reference tests**

Run:

```bash
go test -count=1 ./internal/extract/reference
```

Expected: PASS.

- [ ] **Step 3: Run full test suite**

Run:

```bash
go test -count=1 ./...
```

Expected: all packages report `ok` or `[no test files]`.

- [ ] **Step 4: Run vet**

Run:

```bash
go vet ./...
```

Expected: no output.

- [ ] **Step 5: Check diff whitespace**

Run:

```bash
git diff --check
git diff --cached --check
```

Expected: no output.

- [ ] **Step 6: Review diff shape**

Run:

```bash
git diff --stat
git diff -- internal/extract/reference/extractor.go internal/extract/reference/values.go internal/extract/reference/scoped_types.go internal/extract/reference/extractor_test.go ARCHITECTURE.md
```

Expected: diff shows a new function body context, one unified body extraction walk, removed obsolete function-body value walk, a focused test, and a short architecture note.

- [ ] **Step 7: Commit**

Run:

```bash
git add ARCHITECTURE.md internal/extract/reference/extractor.go internal/extract/reference/values.go internal/extract/reference/scoped_types.go internal/extract/reference/extractor_test.go
git commit -m "refactor: fuse reference body traversal"
```

Expected: commit succeeds with one refactor commit.
