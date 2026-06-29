# Wrapped Route Group Assignment Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Resolve routes registered on a route group assigned through a built-in Lego route-group wrapper.

**Architecture:** Extend only the route group assignment parser. Recursively unwrap calls accepted by the existing built-in route-group wrapper policy, then reuse the current direct `Group` parsing and route extraction flow.

**Tech Stack:** Go, `go/ast`, existing route extractor and pipeline tests.

---

### Task 1: Add the extractor regression

**Files:**
- Modify: `internal/extract/route/extractor_test.go`

- [x] Add a test with `AddStaffFlowControl(oldPathGroup.Group("/officialmsg/v1/admin"))`.
- [x] Assert the route resolves to `/officialmsg/v1/admin/conversations`.
- [x] Run the focused test and verify it fails because no route is extracted.

### Task 2: Parse wrapped group assignments

**Files:**
- Modify: `internal/extract/route/extractor.go`

- [x] Pass the analyzer config into group assignment parsing.
- [x] Recursively inspect the first argument only for built-in route-group wrappers.
- [x] Keep direct `Group` assignment behavior unchanged.
- [x] Run route extractor tests and verify they pass.

### Task 3: Validate the real BFF impact report

**Files:**
- Update: `.analyzer-smoke/sl-sc1-admin-bff-current-branch.impact.json`

- [x] Generate the exact `master...HEAD` forward diff.
- [x] Analyze against the current branch source twice.
- [x] Verify both JSON outputs are byte-identical.
- [x] Verify the summary contains exactly 20 endpoints and the expected officialmsg routes.
- [x] Run `go test ./...` and `go vet ./...`.
- [x] Review the final diff and commit the verified changes.
