# Source-Local Impact Tree Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore complete recursive propagation chains inside file and module sources.

**Architecture:** Keep internal impact analysis unchanged. Replace only the compact output projection with source-local recursive trees matching the ts-analyzer readable raw report.

**Tech Stack:** Go, encoding/json, JSON Schema, Go tests, shell smoke tests.

---

### Task 1: Lock The Raw Report Contract

**Files:**
- Modify: `internal/output/impact_tree_test.go`
- Modify: `internal/output/contract_test.go`

- [x] Replace DAG assertions with `fileSources[].symbols` recursive-chain assertions.
- [x] Assert `moduleSources[].sourceFiles[].symbols` contains recursive chains.
- [x] Assert endpoint leaves and review evidence remain while span stays absent.
- [x] Run targeted tests and confirm they fail against the compact projection.

### Task 2: Restore Source-Local Projection

**Files:**
- Modify: `internal/output/impact_tree.go`

- [x] Remove the public graph/node/edge projection.
- [x] Project each internal root to a recursive impact node.
- [x] Merge duplicate roots deterministically within their owning source.
- [x] Reuse the same source tree shape for module usage files.
- [x] Run output and app tests.

### Task 3: Synchronize Contract And Validation

**Files:**
- Modify: `internal/output/contract.go`
- Modify: `testdata/golden/type-impact.impact.json`
- Modify: `scripts/smoke-real-projects.sh`
- Modify: `README.md`
- Modify: `ARCHITECTURE.md`
- Modify: `docs/contracts/output-contract.md`
- Modify: `docs/validation/real-project-validation.md`

- [x] Publish recursive impact-node schema without span.
- [x] Update golden output and smoke traversal.
- [x] Assert decimal module tree contains `ParseStringToFloat64 -> ConvertPrice`.
- [x] Remove compact DAG documentation.

### Task 4: Verify

- [x] Run `gofmt` and `git diff --check`.
- [x] Run `go test ./...`.
- [x] Run `go vet ./...`.
- [x] Run `bash scripts/smoke-real-projects.sh`.
- [x] Inspect the retained real diff JSON.
- [x] Confirm both real BFF repositories remain clean.
