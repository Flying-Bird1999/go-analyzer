# Analyzer Hardening And Cleanup Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove obsolete architecture and make diff-to-endpoint analysis fail closed, route-aware, and reproducible on real BFF projects.

**Architecture:** Validate the post-change snapshot before AST analysis, derive route-local dependency edges from existing references, remove truncating public config and discarded diagnostics, then repair smoke and documentation contracts.

**Tech Stack:** Go 1.24 standard library, Go AST, Bash/Python smoke harness.

---

### Task 1: Validate Diff Against Current Source

**Files:**
- Modify: `internal/diff/parser.go`
- Modify: `internal/diff/range.go`
- Create: `internal/diff/validate.go`
- Modify: `internal/diff/parser_test.go`
- Create: `internal/diff/validate_test.go`
- Modify: `internal/app/pipeline.go`
- Modify: `internal/app/pipeline_test.go`

- [ ] Add failing tests for restored source, unsafe paths, empty diff, and changed-file parse errors.
- [ ] Retain post-change hunk lines and validate them against project files.
- [ ] Move diff parsing/validation before fact construction.
- [ ] Fail impact analysis when a changed Go file cannot be parsed.
- [ ] Run focused diff and app tests.

### Task 2: Propagate Route-Scoped Dependencies

**Files:**
- Modify: `internal/graph/route.go`
- Modify: `internal/graph/graph_test.go`
- Modify: `internal/impact/tree_builder.go`
- Modify: `internal/impact/analyzer_test.go`
- Modify: `internal/app/pipeline_test.go`

- [ ] Add a failing test where changing `Guard` produces the guarded endpoint.
- [ ] Index references contained by route registration spans.
- [ ] Expand changed symbols to dependency routes with a distinct relation.
- [ ] Verify unrelated routes in the same initializer are excluded.
- [ ] Run graph, impact, and app tests.

### Task 3: Remove Obsolete Configuration And Diagnostics

**Files:**
- Delete: `internal/config/`
- Delete: `docs/examples/go-analyzer.config.json`
- Delete: `testdata/fixtures/configurable-rules/`
- Modify: `cmd/go-analyzer/main.go`
- Modify: `cmd/go-analyzer/main_test.go`
- Modify: `internal/app/options.go`
- Modify: annotation, route, impact, and output packages and tests

- [ ] Remove config flags and config plumbing.
- [ ] Keep Lego syntax rules as package-local built-ins.
- [ ] Remove max-depth/stop-propagation behavior and `stopBoundary`.
- [ ] Remove impact diagnostics and unused impact schema definitions.
- [ ] Keep facts diagnostics.
- [ ] Run all package tests.

### Task 4: Clean Facts And Route Contracts

**Files:**
- Modify: `internal/output/schema.go`
- Modify: `internal/output/json.go`
- Modify: `internal/output/contract.go`
- Modify: output tests and golden files
- Modify: `internal/facts/route.go`
- Modify: route/deleted-route extraction

- [ ] Remove always-empty diff-only fields from facts JSON.
- [ ] Remove `source_family` and generated-only metadata.
- [ ] Keep recovered deleted-route state internal.
- [ ] Remove unused parameters and helpers found by lint.
- [ ] Run schema, output, and golden tests.

### Task 5: Follow Go Loader Ignore Rules

**Files:**
- Modify: `internal/project/loader.go`
- Modify: `internal/project/loader_test.go`

- [ ] Add failing tests for `_fixtures`, hidden directories, and ignored Go files.
- [ ] Skip names beginning with `.` or `_`.
- [ ] Run project and pipeline tests.

### Task 6: Repair Real-Project Smoke

**Files:**
- Modify: `scripts/smoke-real-projects.sh`

- [ ] Keep modified files in place while analysis runs.
- [ ] Restore changes on success and failure.
- [ ] run each real impact case twice and compare bytes.
- [ ] Assert exact endpoint sets for isolated cases.
- [ ] Add a Nexus route-helper propagation case.
- [ ] Run the complete smoke suite and verify both BFF worktrees are restored.

### Task 7: Synchronize Architecture And Contracts

**Files:**
- Modify: `README.md`
- Modify: `ARCHITECTURE.md`
- Modify: `docs/contracts/output-contract.md`
- Modify: `docs/validation/real-project-validation.md`

- [ ] Document validation-before-analysis and fail-closed behavior.
- [ ] Remove obsolete config/impact diagnostics claims.
- [ ] Document route-scoped helper propagation and revised smoke semantics.
- [ ] Update current real-project counts and capability limits.

### Task 8: Final Verification

- [ ] Run `gofmt -l .`.
- [ ] Run `go test -count=1 ./...`.
- [ ] Run `go vet ./...`.
- [ ] Run `golangci-lint run --no-config --go 1.24 ./...`.
- [ ] Run `git diff --check`.
- [ ] Review the final diff and leave all changes uncommitted.
