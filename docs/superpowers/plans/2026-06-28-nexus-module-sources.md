# Nexus Reference And Module Sources Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Resolve receiver/local method calls and project module-driven impacts as `moduleSources`.

**Architecture:** Extend function-scoped AST type inference without adopting `go/types`. Keep module changes and usages as internal facts, then classify impact roots by change source in the output projection.

**Tech Stack:** Go AST, existing fact/graph/impact pipeline, Go tests, shell smoke tests.

---

### Task 1: Function-Scoped Method Resolution

**Files:**
- Modify: `internal/extract/reference/extractor.go`
- Modify: `internal/extract/reference/callee.go`
- Create or modify: `internal/extract/reference/locals.go`
- Modify: `internal/extract/reference/extractor_test.go`
- Add fixture files under: `testdata/`

- [ ] Add a failing extractor test for receiver calls between methods.
- [ ] Add a failing extractor test for a constructor-inferred local method call.
- [ ] Run `go test ./internal/extract/reference -run 'Receiver|ConstructorLocal' -v` and confirm both fail because call facts are absent.
- [ ] Build a function-scoped value-type map from the receiver, typed locals, and supported constructor initializers.
- [ ] Pass scoped types into selector call resolution.
- [ ] Run the targeted tests and the full reference extractor suite.

### Task 2: Module Sources Projection

**Files:**
- Modify: `internal/app/pipeline.go`
- Modify: `internal/app/pipeline_test.go`
- Modify: `internal/output/impact_tree.go`
- Modify: `internal/output/impact_tree_test.go`
- Modify: `internal/output/contract.go`
- Modify: `internal/output/contract_test.go`
- Modify: `testdata/golden/type-impact.impact.json`

- [ ] Add failing output tests for `moduleSources`, endpoint union, and absence of public module fact arrays.
- [ ] Add a failing pipeline test proving a resolved `go.mod` change is absent from `fileSources`.
- [ ] Run targeted tests and confirm failures describe the old output shape.
- [ ] Add a module-source projection model grouped by module path and usage source file.
- [ ] Preserve ordinary diff roots under `fileSources` and project module usage roots under `moduleSources`.
- [ ] Update JSON schema and normalization.
- [ ] Run targeted output and pipeline tests.

### Task 3: Relevant Diagnostics

**Files:**
- Modify: `internal/impact/tree_builder.go`
- Modify: `internal/impact/analyzer_test.go`
- Modify: `internal/output/impact_tree.go`
- Modify: `internal/output/impact_tree_test.go`

- [ ] Add a failing test with one related and one unrelated extraction diagnostic.
- [ ] Confirm the impact result currently includes both diagnostics.
- [ ] Filter extraction diagnostics against changed and reachable facts while retaining current propagation diagnostics.
- [ ] Omit empty diagnostics from the impact JSON.
- [ ] Run impact and output tests.

### Task 4: Documentation And Real BFF Validation

**Files:**
- Modify: `README.md`
- Modify: `ARCHITECTURE.md`
- Modify: `docs/contracts/output-contract.md`
- Modify: `docs/validation/real-project-validation.md`
- Modify: `scripts/smoke-real-projects.sh`

- [ ] Update current output documentation from module fact arrays to `moduleSources`.
- [ ] Extend the decimal smoke assertion to require its Nexus endpoints.
- [ ] Assert `CheckIn` remains in `fileSources` and decimal usage remains in `moduleSources`.
- [ ] Assert unrelated diagnostics do not appear in this impact result.
- [ ] Run `gofmt` on changed Go files.
- [ ] Run `go test ./...`.
- [ ] Run `bash scripts/smoke-real-projects.sh`.
- [ ] Run `git diff --check`.
- [ ] Confirm both real BFF repositories are clean after smoke restoration.
