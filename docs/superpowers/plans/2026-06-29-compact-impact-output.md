# Compact Impact Output Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace repeated recursive impact JSON with a compact source and node graph projection that omits spans and debug evidence.

**Architecture:** Keep extraction, facts, graph, and impact analysis unchanged. Rewrite `internal/output` to merge tree instances into a document-wide node graph, and make file/module sources reference root IDs.

**Tech Stack:** Go, encoding/json, JSON Schema, Go tests, shell smoke tests.

---

### Task 1: Define The Compact Contract

**Files:**
- Modify: `internal/output/impact_tree_test.go`
- Modify: `internal/output/contract_test.go`

- [x] Add a failing test for source roots and globally deduplicated nodes.
- [x] Add a failing test proving spans, raw evidence, and endpoint nodes are absent while source diffs remain.
- [x] Add a failing test proving only medium/low confidence is serialized.
- [x] Add failing schema assertions for the compact contract.
- [x] Run targeted output tests and confirm failures describe the old model.

### Task 2: Implement DAG Projection

**Files:**
- Modify: `internal/output/impact_tree.go`
- Modify: `internal/app/pipeline.go`
- Modify: `internal/app/pipeline_test.go`

- [x] Replace recursive output nodes with unique node and edge records.
- [x] Replace source `symbols` with root references.
- [x] Flatten relevant diagnostics without spans.
- [x] Remove project metadata and raw evidence options while always retaining ordinary file diffs.
- [x] Run output and app tests.

### Task 3: Update Schema And Configuration

**Files:**
- Modify: `internal/output/contract.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/defaults.go`
- Modify: `internal/config/config_test.go`
- Modify: `docs/examples/go-analyzer.config.json`

- [x] Publish compact source/root/node/edge/diagnostic definitions.
- [x] Remove obsolete impact definitions.
- [x] Remove `includeDiff` and `includeRawEvidence`; diff output is unconditional.
- [x] Run schema and config tests.

### Task 4: Update Documentation And Fixtures

**Files:**
- Modify: `README.md`
- Modify: `ARCHITECTURE.md`
- Modify: `docs/contracts/output-contract.md`
- Modify: `docs/validation/real-project-validation.md`
- Modify: `scripts/smoke-real-projects.sh`
- Modify: `testdata/golden/type-impact.impact.json`

- [x] Document the DAG traversal contract.
- [x] Update golden output.
- [x] Update smoke assertions from `symbols` to `roots` and `nodes`.
- [x] Assert forbidden fields are absent recursively.
- [x] Record compact report size.

### Task 5: Verify

- [x] Run `gofmt` on changed Go files.
- [x] Run `go test ./...`.
- [x] Run `go vet ./...`.
- [x] Run `bash scripts/smoke-real-projects.sh`.
- [x] Run `git diff --check`.
- [x] Confirm both real BFF repositories remain clean.
