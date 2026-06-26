# Real Project Validation And Diagnostics Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Validate `go-analyzer` against `sc1-bff-service` and `sc1-admin-bff`, add diagnostics for unsupported patterns, and stabilize smoke/golden workflows.

**Architecture:** This module hardens the analyzer after core extraction and propagation exist. It does not add broad new analysis algorithms unless needed to prevent crashes or silent data loss. Unsupported code should produce diagnostics, not disappear quietly.

**Tech Stack:** Existing analyzer packages, Go tests, optional shell smoke script, JSON golden snapshots.

---

## Context

Read first:

- `docs/design/go-analyzer-mvp-architecture.md`
- `sc1-bff-service/router/router.go`
- `sc1-admin-bff/router/router.go`
- `sc1-admin-bff/router/live/sale.go`
- `sc1-admin-bff/util/guard/guard.go`

## File Structure

Create:

- `internal/diagnostics/diagnostics.go`
- `internal/diagnostics/codes.go`
- `internal/diagnostics/collector.go`
- `testdata/golden/README.md`
- `scripts/smoke-real-projects.sh`
- `docs/validation/real-project-validation.md`

Modify:

- `internal/facts/store.go`
- `internal/output/schema.go`
- extract/link/impact modules to emit diagnostics instead of silent drops

Tests:

- `internal/diagnostics/diagnostics_test.go`
- `internal/output/golden_test.go`
- `internal/app/real_project_smoke_test.go` if runtime is acceptable

## Tasks

### Task 1: Diagnostics Model

**Files:**

- Create: `internal/diagnostics/diagnostics.go`
- Create: `internal/diagnostics/codes.go`
- Create: `internal/diagnostics/collector.go`
- Test: `internal/diagnostics/diagnostics_test.go`

- [ ] **Step 1: Write failing diagnostics test**

Assert diagnostic includes:

- code
- severity
- message
- source span
- related fact IDs

- [ ] **Step 2: Implement codes**

Initial codes:

- `route_dynamic_path`
- `route_unresolved_handler`
- `route_wrapper_unsupported`
- `middleware_order_uncertain`
- `annotation_missing_for_handler`
- `package_load_failed`
- `module_usage_file_fallback`
- `module_unreferenced`

- [ ] **Step 3: Implement collector**

Collector should dedupe by code + source span + message.

- [ ] **Step 4: Run tests**

```bash
cd go-analyzer
go test ./internal/diagnostics -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/diagnostics
git commit -m "feat: add diagnostics model"
```

### Task 2: Wire Diagnostics Into Extractors

**Files:**

- Modify: `internal/extract/route/extractor.go`
- Modify: `internal/extract/route/handler.go`
- Modify: `internal/extract/gomod/usage.go`
- Modify: `internal/facts/store.go`
- Test: existing extractor tests

- [ ] **Step 1: Write failing unsupported route tests**

Cover:

- dynamic route path expression
- handler stored in map before registration
- unsupported wrapper call

Assert diagnostics are emitted and raw facts are preserved where possible.

- [ ] **Step 2: Wire route diagnostics**

Do not fail extraction for unsupported route patterns.

- [ ] **Step 3: Wire module diagnostics**

Emit:

- `module_usage_file_fallback`
- `module_unreferenced`

- [ ] **Step 4: Run extraction tests**

```bash
cd go-analyzer
go test ./internal/extract/... ./internal/diagnostics -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/extract internal/facts internal/diagnostics
git commit -m "feat: emit diagnostics for unsupported facts"
```

### Task 3: Stable Golden Output

**Files:**

- Create: `testdata/golden/README.md`
- Create: `internal/output/golden_test.go`
- Modify: `internal/output/json.go`

- [ ] **Step 1: Write failing golden test**

Use `mini-bff` and assert facts JSON matches golden file.

- [ ] **Step 2: Implement golden helper**

Test should support:

```bash
UPDATE_GOLDEN=1 go test ./internal/output -run TestMiniBFFGolden
```

- [ ] **Step 3: Normalize volatile paths**

Golden output should use stable relative fixture paths, or test helper should normalize temp absolute paths.

- [ ] **Step 4: Run golden tests**

```bash
cd go-analyzer
go test ./internal/output -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/output testdata/golden
git commit -m "test: add golden fact output"
```

### Task 4: Real Project Smoke Script

**Files:**

- Create: `scripts/smoke-real-projects.sh`
- Create: `docs/validation/real-project-validation.md`

- [ ] **Step 1: Write smoke script**

Script should run:

```bash
go run ./cmd/go-analyzer facts --project ../sc1-bff-service --format json
go run ./cmd/go-analyzer facts --project ../sc1-admin-bff --format json
```

Write outputs to an ignored temp directory such as `.analyzer-smoke/`.

- [ ] **Step 2: Validate JSON**

Use `go run` and a tiny Go or shell check to ensure outputs are valid JSON.

- [ ] **Step 3: Document expected counts**

In `docs/validation/real-project-validation.md`, record:

- annotation count range
- route registration count range
- diagnostic count
- top unsupported patterns

- [ ] **Step 4: Run smoke manually**

```bash
cd go-analyzer
bash scripts/smoke-real-projects.sh
```

Expected: both projects produce parseable JSON and no panic.

- [ ] **Step 5: Commit**

```bash
git add scripts/smoke-real-projects.sh docs/validation/real-project-validation.md
git commit -m "test: add real project smoke validation"
```

### Task 5: Real Project Fix Pass

**Files:**

- Modify only files needed by smoke failures.
- Update validation doc.

- [ ] **Step 1: Run smoke and inspect diagnostics**

```bash
cd go-analyzer
bash scripts/smoke-real-projects.sh
```

- [ ] **Step 2: Fix crashers only**

If the analyzer panics or fails JSON output, fix that issue with a focused regression test.

- [ ] **Step 3: Add diagnostics for silent skips**

If a common unsupported pattern is silently skipped, add a diagnostic.

- [ ] **Step 4: Do not chase every precision issue**

Precision improvements such as annotation-route mismatch validation belong to future diagnostics work, not this MVP hardening pass.

- [ ] **Step 5: Run full tests**

```bash
cd go-analyzer
go test ./...
bash scripts/smoke-real-projects.sh
```

Expected: tests pass and smoke outputs parseable JSON.

- [ ] **Step 6: Commit**

```bash
git add .
git commit -m "fix: harden analyzer on real bff projects"
```

## Completion Criteria

- Unsupported patterns produce diagnostics instead of silent loss.
- Golden output is stable.
- `sc1-bff-service` and `sc1-admin-bff` facts smoke runs produce valid JSON.
- Validation documentation captures current coverage and known unsupported patterns.
