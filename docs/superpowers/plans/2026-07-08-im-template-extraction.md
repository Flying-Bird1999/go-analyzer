# IM Template Extraction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the IM value-template subsystem from `internal/extract/im/summary.go` into `internal/extract/im/template.go` without changing analyzer behavior.

**Architecture:** `summary.go` remains the IM summary propagation engine. `template.go` owns the package-private value-template model and template operations, while methods that need `summaryEngine` state remain methods on `*summaryEngine`.

**Tech Stack:** Go AST (`go/ast`, `go/token`, `go/printer`), existing `internal/astindex`, `internal/facts`, and `internal/project` packages.

## Global Constraints

- Preserve existing IM extraction behavior, output IDs, dependency semantics, diagnostics, and ordering.
- Do not add new IM parsing capability.
- Do not change protocol discovery, SDK adapter matching, direct summary discovery, reachability, or fact projection.
- Do not introduce `go/types`, SSA, interface dispatch, flow-sensitive reassignment, or new external dependencies.
- Keep all moved APIs package-private.
- Use `apply_patch` for manual source edits.
- Keep documentation paths relative; do not write absolute workspace paths into project docs.

---

## File Structure

- Create `internal/extract/im/template.go`: package-private template model and operations.
- Modify `internal/extract/im/summary.go`: remove moved template code and unused imports; keep propagation, direct summary, control dependency, local call resolution, and symbol dependency logic.
- Modify `ARCHITECTURE.md`: mention that IM template logic now lives in `template.go`.

---

### Task 1: Capture A Clean Behavior Baseline

**Files:**
- Read: `internal/extract/im/summary.go`
- Read: `internal/extract/im/summary_test.go`
- Read: `internal/extract/im/flow_test.go`
- Read: `internal/extract/im/expr_test.go`

**Interfaces:**
- Consumes: current committed IM extractor.
- Produces: verified baseline before moving code.

- [ ] **Step 1: Confirm worktree state**

Run:

```bash
git status --short
```

Expected: only this plan file is present if it has not been committed yet, or no output after the plan commit.

- [ ] **Step 2: Run focused IM tests**

Run:

```bash
go test -count=1 ./internal/extract/im
```

Expected: `ok  	gopkg.inshopline.com/bff/go-analyzer/internal/extract/im`.

- [ ] **Step 3: Run full tests**

Run:

```bash
go test -count=1 ./...
```

Expected: all packages report `ok` or `[no test files]`.

---

### Task 2: Move Template Model And Operations

**Files:**
- Create: `internal/extract/im/template.go`
- Modify: `internal/extract/im/summary.go`

**Interfaces:**
- Consumes:
  - `type summaryEngine struct`
  - `type functionInfo struct`
  - `func (e *summaryEngine) fieldTypeIDs(parents []facts.SymbolID, fieldName string) []facts.SymbolID`
  - `func (e *summaryEngine) resolveLocalCall(file *project.File, call *ast.CallExpr) (facts.SymbolID, bool)`
  - `func (e *summaryEngine) symbolDependencies(file *project.File, expr ast.Expr) []facts.SymbolID`
- Produces:
  - `type templateKind string`
  - `type valueTemplate struct`
  - `func (e *summaryEngine) templateFromExpr(info *functionInfo, expr ast.Expr, event bool, visiting map[string]bool) *valueTemplate`
  - `func (e *summaryEngine) substitute(info *functionInfo, template *valueTemplate, args []ast.Expr, event bool) *valueTemplate`
  - `func concreteTemplateValue(template *valueTemplate) (string, bool)`
  - `func templateKey(template *valueTemplate) string`
  - `func cloneTemplate(in *valueTemplate) *valueTemplate`
  - `func renderExpr(expr ast.Expr) string`
  - `func templatePrimaryParam(template *valueTemplate) int`

- [ ] **Step 1: Create `template.go` with moved code**

Create `internal/extract/im/template.go` with package `im` and these imports:

```go
package im

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"
	"sort"
	"strconv"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)
```

Move these declarations from `summary.go` into `template.go` without changing their bodies:

```text
type templateKind string
const (
    templateUnknown
    templateLiteral
    templateParam
    templateField
    templateConcat
    templateString
    templateCallback
    templateComposite
)
type valueTemplate struct
func (e *summaryEngine) templateFromExpr(...)
func (e *summaryEngine) substitute(...)
func concreteTemplateValue(...)
func templateKey(...)
func cloneTemplate(...)
func renderExpr(...)
func templatePrimaryParam(...)
```

The moved methods should keep the same receivers and signatures. Do not introduce a new resolver interface in this task.

- [ ] **Step 2: Remove moved declarations from `summary.go`**

Delete the exact declarations moved in Step 1 from `summary.go`.

Keep these functions in `summary.go`:

```text
func (e *summaryEngine) fieldTypeIDs(...)
func (e *summaryEngine) factForSummary(...)
func (e *summaryEngine) controlDependencies(...)
func (e *summaryEngine) controlExpressions(...)
func spanContainsNode(...)
func (e *summaryEngine) resolveLocalCall(...)
func (e *summaryEngine) symbolDependencies(...)
func appendUniqueSymbols(...)
func uniqueSortedSymbols(...)
func symbolListKey(...)
func sortedFunctionIDs(...)
func symbolSliceSet(...)
func copyStringSet(...)
func argumentAt(...)
```

- [ ] **Step 3: Clean imports**

After moving the code, `summary.go` should no longer import template-only packages. Its import block should only keep packages still referenced in `summary.go`, expected to include:

```go
import (
	"go/ast"
	"go/token"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)
```

If `go test` reports an unused or missing import, resolve it by following actual compiler feedback.

- [ ] **Step 4: Format and run focused tests**

Run:

```bash
gofmt -w internal/extract/im/summary.go internal/extract/im/template.go
go test -count=1 ./internal/extract/im
```

Expected: focused IM tests pass.

---

### Task 3: Update Architecture Documentation

**Files:**
- Modify: `ARCHITECTURE.md`

**Interfaces:**
- Consumes: final file layout from Task 2.
- Produces: architecture documentation that names the new IM template file.

- [ ] **Step 1: Find the IM extraction architecture section**

Run:

```bash
rg -n "internal/extract/im|IM|summary.go|template" ARCHITECTURE.md
```

Expected: output includes the section describing IM event extraction.

- [ ] **Step 2: Add a short module note**

Add a sentence to the IM extraction section using relative paths:

```markdown
`internal/extract/im/summary.go` owns IM summary propagation, while `internal/extract/im/template.go` owns the package-private value-template model, expression-to-template conversion, template substitution, and template keys.
```

Do not describe any new behavior or capability.

- [ ] **Step 3: Verify documentation has no absolute workspace paths**

Run:

```bash
workspace_root="$(git rev-parse --show-toplevel)"
workspace_parent="$(dirname "$workspace_root")"
rg -n "$workspace_root|$workspace_parent" ARCHITECTURE.md docs/superpowers/specs/2026-07-08-im-template-extraction-design.md docs/superpowers/plans/2026-07-08-im-template-extraction.md
```

Expected: no output.

---

### Task 4: Final Verification And Commit

**Files:**
- Verify: all modified files.

**Interfaces:**
- Consumes: Tasks 1-3.
  - `internal/extract/im/template.go`
  - `internal/extract/im/summary.go`
  - `ARCHITECTURE.md`
- Produces: one behavior-preserving refactor commit.

- [ ] **Step 1: Run formatting check**

Run:

```bash
gofmt -l $(git ls-files '*.go')
```

Expected: no output.

- [ ] **Step 2: Run full test suite**

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
git diff -- internal/extract/im/summary.go internal/extract/im/template.go
```

Expected: template declarations and operations are moved to `template.go`; no unrelated IM behavior changes are present.

- [ ] **Step 6: Commit**

Run:

```bash
git add ARCHITECTURE.md internal/extract/im/summary.go internal/extract/im/template.go
git commit -m "refactor: extract im value templates"
```

Expected: commit succeeds with one refactor commit.
