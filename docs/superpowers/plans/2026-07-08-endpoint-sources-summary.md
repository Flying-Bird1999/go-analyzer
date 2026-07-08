# Endpoint Sources Summary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `endpointSourcesSummary` to impact JSON so consumers can see which changed file or module source caused each endpoint to be reported.

**Architecture:** Build the summary as a pure projection inside `internal/output`, after `fileSources` and `moduleSources` are finalized. Reuse existing recursive `ImpactNode` trees to derive root symbols and shortest chains without copying full evidence into the new summary.

**Tech Stack:** Go 1.24, existing `internal/output` impact projection, JSON schema builder in `internal/output/contract.go`, Go table tests, real-project smoke commands.

## Global Constraints

- `endpointSourcesSummary` is a top-level field rendered after `fileSources` and after `moduleSources` when module sources exist.
- The field is required and renders as an empty array when no endpoint is impacted.
- It is a lightweight projection; full evidence remains in `fileSources[].symbols` and `moduleSources[].sourceFiles[].symbols`.
- Paths in docs and output are project-relative; do not write workspace-specific absolute paths into project docs.
- Ordering is deterministic by endpoint, source, root symbol, and chain labels.

---

## File Structure

- Modify `internal/output/impact_tree.go`: add public summary structs, build helpers, normalization, and JSON ordering.
- Modify `internal/output/impact_tree_test.go`: add red/green tests for file source aggregation, module source aggregation, JSON field order, and empty summary.
- Modify `internal/output/contract.go`: extend impact schema definitions.
- Modify `internal/output/contract_test.go`: assert schema exposes `endpointSourcesSummary`.
- Modify `docs/contracts/output-contract.md`: document the new field.
- Modify `ARCHITECTURE.md`: update impact output contract section.

### Task 1: Output Contract Types And File Source Summary

**Files:**
- Modify: `internal/output/impact_tree.go`
- Test: `internal/output/impact_tree_test.go`

**Interfaces:**
- Produces: `EndpointSourcesSummary []EndpointSourceSummary` on `ImpactDocument`.
- Produces: `EndpointSourceSummary`, `EndpointImpactSource`, and `EndpointRootSymbolSummary` structs.
- Produces helper behavior through `BuildImpactDocument`.

- [ ] **Step 1: Write failing test for file source endpoint summary**

Add a test to `internal/output/impact_tree_test.go`:

```go
func TestBuildImpactDocumentAddsEndpointSourcesSummaryForFileSources(t *testing.T) {
	fileChanges := []diff.FileChange{{
		NewPath: "service/order.go",
		Raw:     "diff --git a/service/order.go b/service/order.go\n",
	}}
	root := impact.RootImpact{
		Change: facts.ChangeFact{
			SourceFile: "service/order.go",
			SymbolID:   "func:example.com/app/service::UpdateOrder",
		},
		Root: impact.Node{
			ID:         "func:example.com/app/service::UpdateOrder",
			Kind:       "func",
			Name:       "UpdateOrder",
			File:       "service/order.go",
			Confidence: facts.ConfidenceHigh,
			Children: []impact.Node{{
				ID:         "func:example.com/app/controller::UpdateOrder",
				Kind:       "func",
				Name:       "UpdateOrder",
				File:       "controller/order.go",
				Relation:   "call",
				Confidence: facts.ConfidenceHigh,
				Children: []impact.Node{{
					ID:         "endpoint:POST:/orders",
					Kind:       "endpoint",
					Name:       "POST /orders",
					File:       "router/order.go",
					Relation:   "route_endpoint",
					Confidence: facts.ConfidenceHigh,
					Method:     "POST",
					Path:       "/orders",
				}},
			}},
		},
		Endpoints: []impact.EndpointImpact{{Method: "POST", Path: "/orders"}},
	}

	doc := BuildImpactDocument(fileChanges, impact.TreeResult{Roots: []impact.RootImpact{root}}, ImpactDocumentOptions{})

	if len(doc.EndpointSourcesSummary) != 1 {
		t.Fatalf("endpointSourcesSummary = %#v", doc.EndpointSourcesSummary)
	}
	got := doc.EndpointSourcesSummary[0]
	if got.Method != "POST" || got.Path != "/orders" {
		t.Fatalf("endpoint summary = %#v", got)
	}
	if len(got.Sources) != 1 {
		t.Fatalf("sources = %#v", got.Sources)
	}
	source := got.Sources[0]
	if source.SourceType != "file" || source.SourceFile != "service/order.go" {
		t.Fatalf("source = %#v", source)
	}
	if len(source.RootSymbols) != 1 || source.RootSymbols[0].ID != "func:example.com/app/service::UpdateOrder" {
		t.Fatalf("root symbols = %#v", source.RootSymbols)
	}
	wantChain := []string{"func UpdateOrder", "func UpdateOrder", "POST /orders"}
	if len(source.Chains) != 1 || !reflect.DeepEqual(source.Chains[0], wantChain) {
		t.Fatalf("chains = %#v, want %#v", source.Chains, wantChain)
	}
	if source.Confidence != facts.ConfidenceHigh {
		t.Fatalf("confidence = %q", source.Confidence)
	}
}
```

- [ ] **Step 2: Run test and verify red**

Run:

```bash
go test ./internal/output -run TestBuildImpactDocumentAddsEndpointSourcesSummaryForFileSources -count=1
```

Expected: compile failure because `EndpointSourcesSummary` does not exist.

- [ ] **Step 3: Implement minimal file source projection**

In `internal/output/impact_tree.go`, add structs and build `EndpointSourcesSummary` after existing sources are finalized. The helper should walk each root tree, find the shortest path to each endpoint, and append one source entry per owning file source.

- [ ] **Step 4: Run test and verify green**

Run:

```bash
go test ./internal/output -run TestBuildImpactDocumentAddsEndpointSourcesSummaryForFileSources -count=1
```

Expected: PASS.

### Task 2: Module Sources, Empty Output, And JSON Field Order

**Files:**
- Modify: `internal/output/impact_tree.go`
- Test: `internal/output/impact_tree_test.go`

**Interfaces:**
- Consumes: `EndpointSourcesSummary` structs from Task 1.
- Produces: module source projection with `sourceType: "module"` and module metadata.

- [ ] **Step 1: Write failing tests for module source and JSON ordering**

Add tests to `internal/output/impact_tree_test.go`:

```go
func TestBuildImpactDocumentAddsEndpointSourcesSummaryForModuleSources(t *testing.T) {
	moduleChange := facts.ModuleChangeFact{
		Path:          "example.com/lib",
		Kind:          facts.ModuleChangeUpgraded,
		VersionBefore: "v1.0.0",
		VersionAfter:  "v1.1.0",
	}
	moduleUsage := facts.ModuleUsageFact{
		ModulePath: "example.com/lib",
		SourceFile: "service/payment.go",
		SymbolID:   "func:example.com/app/service::Pay",
		Basis:      facts.ModuleUsagePrecise,
	}
	root := impact.RootImpact{
		Change: facts.ChangeFact{
			SourceFile: "go.mod",
			SymbolID:   moduleUsage.SymbolID,
			Kind:       facts.ChangeKindGoModChanged,
		},
		Root: impact.Node{
			ID:         moduleUsage.SymbolID,
			Kind:       "func",
			Name:       "Pay",
			File:       "service/payment.go",
			Confidence: facts.ConfidenceMedium,
			Children: []impact.Node{{
				ID:         "endpoint:POST:/pay",
				Kind:       "endpoint",
				Name:       "POST /pay",
				Relation:   "route_endpoint",
				Confidence: facts.ConfidenceMedium,
				Method:     "POST",
				Path:       "/pay",
			}},
		},
		Endpoints: []impact.EndpointImpact{{Method: "POST", Path: "/pay"}},
	}

	doc := BuildImpactDocument(nil, impact.TreeResult{Roots: []impact.RootImpact{root}}, ImpactDocumentOptions{
		ModuleChanges: []facts.ModuleChangeFact{moduleChange},
		ModuleUsages:  []facts.ModuleUsageFact{moduleUsage},
	})

	got := doc.EndpointSourcesSummary[0].Sources[0]
	if got.SourceType != "module" || got.ModulePath != "example.com/lib" || got.SourceFile != "service/payment.go" {
		t.Fatalf("module endpoint source = %#v", got)
	}
	if got.ChangeType != facts.ModuleChangeUpgraded || got.VersionBefore != "v1.0.0" || got.VersionAfter != "v1.1.0" {
		t.Fatalf("module metadata = %#v", got)
	}
}

func TestRenderImpactTreeJSONPlacesEndpointSourcesSummaryLast(t *testing.T) {
	doc := ImpactDocument{
		Summary: ImpactSummary{ImpactedEndpoints: []EndpointSummary{{Method: "GET", Path: "/x"}}},
		FileSources: []FileSourceImpact{{
			SourceFile:          "a.go",
			Symbols:             map[string]ImpactNode{},
			ImpactedEndpoints:   []EndpointSummary{{Method: "GET", Path: "/x"}},
			ImpactedIMEvents:    []string{},
		}},
		ModuleSources: []ModuleSourceImpact{{
			ModulePath: "example.com/lib",
			ChangeType: facts.ModuleChangeUpgraded,
			Basis:      string(facts.ModuleUsagePrecise),
		}},
		EndpointSourcesSummary: []EndpointSourceSummary{{Method: "GET", Path: "/x"}},
	}
	payload, err := RenderImpactTreeJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	fileIdx := strings.Index(text, `"fileSources"`)
	moduleIdx := strings.Index(text, `"moduleSources"`)
	summaryIdx := strings.Index(text, `"endpointSourcesSummary"`)
	if !(fileIdx >= 0 && moduleIdx > fileIdx && summaryIdx > moduleIdx) {
		t.Fatalf("unexpected field order: %s", text)
	}
}
```

- [ ] **Step 2: Run tests and verify red**

Run:

```bash
go test ./internal/output -run 'TestBuildImpactDocumentAddsEndpointSourcesSummaryForModuleSources|TestRenderImpactTreeJSONPlacesEndpointSourcesSummaryLast' -count=1
```

Expected: fail before module metadata and field order are fully implemented.

- [ ] **Step 3: Implement module metadata and field order**

Extend the projection to read finalized `ModuleSourceImpact` entries. Ensure the `ImpactDocument` struct field order is `Summary`, `FileSources`, `ModuleSources`, `EndpointSourcesSummary`.

- [ ] **Step 4: Run tests and verify green**

Run:

```bash
go test ./internal/output -run 'TestBuildImpactDocumentAddsEndpointSourcesSummaryForModuleSources|TestRenderImpactTreeJSONPlacesEndpointSourcesSummaryLast' -count=1
```

Expected: PASS.

### Task 3: Schema And Documentation

**Files:**
- Modify: `internal/output/contract.go`
- Modify: `internal/output/contract_test.go`
- Modify: `docs/contracts/output-contract.md`
- Modify: `ARCHITECTURE.md`

**Interfaces:**
- Consumes: `endpointSourcesSummary` shape from Tasks 1 and 2.
- Produces: impact schema and documentation that match rendered JSON.

- [ ] **Step 1: Write failing schema test**

Extend `TestImpactSchemaExposesModuleSources` or add:

```go
func TestImpactSchemaExposesEndpointSourcesSummary(t *testing.T) {
	schema := ImpactSchema()
	properties := schema["properties"].(map[string]any)
	if _, ok := properties["endpointSourcesSummary"]; !ok {
		t.Fatalf("endpointSourcesSummary property missing: %#v", properties)
	}
	required := schema["required"].([]string)
	if !slices.Contains(required, "endpointSourcesSummary") {
		t.Fatalf("endpointSourcesSummary missing from required: %#v", required)
	}
	defs := schema["$defs"].(map[string]any)
	for _, name := range []string{"endpoint_source_summary", "endpoint_impact_source", "endpoint_root_symbol_summary"} {
		if _, ok := defs[name]; !ok {
			t.Fatalf("%s definition missing: %#v", name, defs)
		}
	}
}
```

- [ ] **Step 2: Run schema test and verify red**

Run:

```bash
go test ./internal/output -run TestImpactSchemaExposesEndpointSourcesSummary -count=1
```

Expected: FAIL because schema lacks the field.

- [ ] **Step 3: Implement schema and docs**

Update `internal/output/contract.go`, `docs/contracts/output-contract.md`, and `ARCHITECTURE.md` to describe the new field. Keep all docs paths relative.

- [ ] **Step 4: Run schema test and docs path check**

Run:

```bash
go test ./internal/output -run TestImpactSchemaExposesEndpointSourcesSummary -count=1
workspace_root="$(git rev-parse --show-toplevel)"
rg -n -F "$workspace_root" docs/contracts/output-contract.md ARCHITECTURE.md docs/superpowers/specs docs/superpowers/plans
```

Expected: test PASS; `rg` prints nothing and exits 1.

### Task 4: Full Verification And Real Project Validation

**Files:**
- No production files unless verification reveals a defect.
- Generated outputs under ignored `.analyzer-smoke/`.

**Interfaces:**
- Consumes: all prior tasks.
- Produces: verified analyzer output on real BFF projects.

- [ ] **Step 1: Run focused and full tests**

Run:

```bash
go test -count=1 ./internal/output
go test -count=1 ./...
go vet ./...
git diff --check
```

Expected: all commands pass.

- [ ] **Step 2: Build analyzer**

Run:

```bash
mkdir -p .analyzer-smoke
GOCACHE="${GOCACHE:-/private/tmp/go-build-go-analyzer-smoke}" \
GOMODCACHE="${GOMODCACHE:-/private/tmp/go-mod-go-analyzer-smoke}" \
go build -o .analyzer-smoke/go-analyzer ./cmd/go-analyzer
```

Expected: exit 0.

- [ ] **Step 3: Validate `sl-sc1-admin-bff` with proto filtering**

Run:

```bash
git -C ../sl-sc1-admin-bff diff master...HEAD > .analyzer-smoke/sl-sc1-admin-bff-feature-jira-SC1-3352-issues-3.diff
.analyzer-smoke/go-analyzer impact \
  --project "$(cd ../sl-sc1-admin-bff && pwd)" \
  --diff "$(pwd)/.analyzer-smoke/sl-sc1-admin-bff-feature-jira-SC1-3352-issues-3.diff" \
  --impact-config "$(pwd)/.analyzer-smoke/sl-sc1-admin-bff-ignore-proto-impact.config.json" \
  --format json \
  --timings \
  > .analyzer-smoke/sl-sc1-admin-bff-feature-jira-SC1-3352-issues-3.ignore-proto.impact.json \
  2> .analyzer-smoke/sl-sc1-admin-bff-feature-jira-SC1-3352-issues-3.ignore-proto.timings.txt
python3 -m json.tool .analyzer-smoke/sl-sc1-admin-bff-feature-jira-SC1-3352-issues-3.ignore-proto.impact.json > /dev/null
```

Expected: exit 0 and impact JSON contains `endpointSourcesSummary`.

- [ ] **Step 4: Validate `sl-sc1-bff-service` with and without proto filtering**

Run:

```bash
git -C ../sl-sc1-bff-service diff master...HEAD > .analyzer-smoke/sl-sc1-bff-service-feature-SC1-3278-buying-notice-topic.diff
.analyzer-smoke/go-analyzer impact \
  --project "$(cd ../sl-sc1-bff-service && pwd)" \
  --diff "$(pwd)/.analyzer-smoke/sl-sc1-bff-service-feature-SC1-3278-buying-notice-topic.diff" \
  --impact-config "$(pwd)/.analyzer-smoke/sl-sc1-bff-service-ignore-proto-impact.config.json" \
  --format json \
  > .analyzer-smoke/sl-sc1-bff-service-feature-SC1-3278-buying-notice-topic.ignore-proto.impact.json
.analyzer-smoke/go-analyzer impact \
  --project "$(cd ../sl-sc1-bff-service && pwd)" \
  --diff "$(pwd)/.analyzer-smoke/sl-sc1-bff-service-feature-SC1-3278-buying-notice-topic.diff" \
  --format json \
  > .analyzer-smoke/sl-sc1-bff-service-feature-SC1-3278-buying-notice-topic.impact.json
python3 -m json.tool .analyzer-smoke/sl-sc1-bff-service-feature-SC1-3278-buying-notice-topic.ignore-proto.impact.json > /dev/null
python3 -m json.tool .analyzer-smoke/sl-sc1-bff-service-feature-SC1-3278-buying-notice-topic.impact.json > /dev/null
```

Expected: both commands exit 0 and both impact JSON files contain `endpointSourcesSummary`.

- [ ] **Step 5: Commit implementation**

Run:

```bash
git status --short
git add internal/output/impact_tree.go internal/output/impact_tree_test.go internal/output/contract.go internal/output/contract_test.go docs/contracts/output-contract.md ARCHITECTURE.md docs/superpowers/plans/2026-07-08-endpoint-sources-summary.md
git commit -m "feat: add endpoint sources summary"
```

Expected: commit succeeds.
