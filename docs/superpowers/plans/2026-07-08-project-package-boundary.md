# Project Package Boundary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move project package membership checks into `astindex.Index`.

**Architecture:** `astindex.Index` already owns `Project.ModulePath`, so it becomes the single boundary for checking whether a package path belongs to the current module. Reference extraction delegates to `idx.IsProjectPackage` instead of carrying its own helper or inline prefix checks.

**Tech Stack:** Go standard `strings`, existing `internal/astindex`, `internal/extract/reference`, and `internal/project` packages.

## Global Constraints

- Do not change package membership semantics.
- Do not change reference diagnostics behavior.
- Do not change symbol resolution rules.
- Do not introduce dependencies.
- Do not change public output contracts.
- Keep documentation paths relative; do not write absolute workspace paths into project docs.

---

### Task 1: Add Failing astindex Test

**Files:**
- Modify: `internal/astindex/index_test.go`

**Interfaces:**
- Produces failing expectation for:
  - `func (idx *Index) IsProjectPackage(packagePath string) bool`

- [ ] **Step 1: Confirm baseline**

Run:

```bash
git status --short
go test -count=1 ./internal/astindex
```

Expected: clean worktree and astindex tests pass before code changes.

- [ ] **Step 2: Add test**

Add this test to `internal/astindex/index_test.go`:

```go
func TestIndexIsProjectPackage(t *testing.T) {
	idx := &Index{Project: &project.Project{ModulePath: "example.com/app"}}
	cases := []struct {
		name string
		path string
		want bool
	}{
		{name: "module root", path: "example.com/app", want: true},
		{name: "child package", path: "example.com/app/service", want: true},
		{name: "similar prefix", path: "example.com/application", want: false},
		{name: "external", path: "example.com/other", want: false},
		{name: "empty", path: "", want: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := idx.IsProjectPackage(tt.path); got != tt.want {
				t.Fatalf("IsProjectPackage(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 3: Verify red**

Run:

```bash
go test -count=1 ./internal/astindex -run TestIndexIsProjectPackage -v
```

Expected: FAIL to compile with `idx.IsProjectPackage undefined`.

---

### Task 2: Implement astindex Method

**Files:**
- Modify: `internal/astindex/index.go`

**Interfaces:**
- Produces:
  - `func (idx *Index) IsProjectPackage(packagePath string) bool`

- [ ] **Step 1: Add method**

Add this method near the `Index` type helpers in `internal/astindex/index.go`:

```go
// IsProjectPackage 判断 packagePath 是否落在当前项目 module 下。
func (idx *Index) IsProjectPackage(packagePath string) bool {
	if idx == nil || idx.Project == nil || idx.Project.ModulePath == "" || packagePath == "" {
		return false
	}
	modulePath := idx.Project.ModulePath
	return packagePath == modulePath || strings.HasPrefix(packagePath, modulePath+"/")
}
```

- [ ] **Step 2: Verify green**

Run:

```bash
gofmt -w internal/astindex/index.go internal/astindex/index_test.go
go test -count=1 ./internal/astindex -run TestIndexIsProjectPackage -v
```

Expected: PASS.

---

### Task 3: Replace Reference Package Logic

**Files:**
- Modify: `internal/extract/reference/resolver.go`
- Modify: `internal/extract/reference/types.go`

**Interfaces:**
- Consumes:
  - `func (idx *astindex.Index) IsProjectPackage(packagePath string) bool`

- [ ] **Step 1: Update resolver**

Replace calls like:

```go
isProjectPackage(r.idx.Project.ModulePath, packagePath)
```

with:

```go
r.idx.IsProjectPackage(packagePath)
```

Delete the local `isProjectPackage` helper from `resolver.go`. Remove `strings` import from `resolver.go` if it becomes unused.

- [ ] **Step 2: Update type diagnostics**

In `internal/extract/reference/types.go`, replace inline module prefix checks with:

```go
if !idx.IsProjectPackage(importPath) {
	return false
}
```

Remove `strings` import from `types.go` if it becomes unused.

- [ ] **Step 3: Run focused tests**

Run:

```bash
gofmt -w internal/extract/reference/resolver.go internal/extract/reference/types.go
go test -count=1 ./internal/extract/reference
```

Expected: PASS.

---

### Task 4: Final Verification And Commit

**Files:**
- Verify all modified files.

- [ ] **Step 1: Run full checks**

Run:

```bash
gofmt -l $(git ls-files '*.go')
go test -count=1 ./...
go vet ./...
git diff --check
git diff --cached --check
```

Expected: no formatting output, tests pass, vet has no output, diff checks pass.

- [ ] **Step 2: Commit**

Run:

```bash
git add internal/astindex/index.go internal/astindex/index_test.go internal/extract/reference/resolver.go internal/extract/reference/types.go
git commit -m "refactor: centralize project package checks"
```

Expected: commit succeeds.
