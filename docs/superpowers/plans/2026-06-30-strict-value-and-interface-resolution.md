# Strict Value and Interface Resolution Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents are explicitly authorized) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add exact `new(T)`, typed-constant method, and uniquely proven package-interface dispatch without introducing speculative call edges.

**Architecture:** Extend `astindex` with exact value types and a second-pass package-variable assignment index. Reference extraction consumes only high-confidence, unique interface bindings; ambiguous or unknown bindings remain unresolved. Keep lexical assignment-target resolution isolated from call resolution.

**Tech Stack:** Go AST (`go/ast`, `go/token`), existing `project`, `astindex`, `facts`, and reference extraction packages; Go unit tests and real-project smoke scripts.

---

## File Map

- Modify `internal/astindex/index.go`
  - Index named interfaces, exact variable/constant value types, and invoke the second indexing pass.
  - Resolve methods on variables, constants, and strict interface bindings.
- Create `internal/astindex/interface_bindings.go`
  - Collect package-variable assignments with lexical-scope awareness.
  - Represent concrete candidates and unknown/ambiguous state.
- Create `internal/astindex/interface_bindings_test.go`
  - Verify unique, repeated, ambiguous, unknown, and shadowed assignments.
- Modify `internal/astindex/index_test.go`
  - Verify package-level `new(T)` and typed-constant receiver types.
- Modify `internal/extract/reference/scoped_types.go`
  - Infer local `new(T)` as an exact concrete type.
- Modify `internal/extract/reference/callee.go`
  - Consume generalized value receiver types and strict interface binding results.
- Modify `internal/extract/reference/extractor_test.go`
  - Verify emitted and rejected call edges.
- Modify `scripts/smoke-real-projects.sh`
  - Add the three real post-change-source regression cases and exact endpoint assertions.
- Modify `ARCHITECTURE.md`
  - Document the strict interface-dispatch boundary and remaining unsupported dynamic cases.
- Modify `docs/validation/real-project-validation.md`
  - Record the real BFF evidence and expected endpoint sets.

### Task 1: Exact `new(T)` and Typed-Constant Types

**Files:**
- Modify: `internal/astindex/index_test.go`
- Modify: `internal/astindex/index.go`
- Modify: `internal/extract/reference/extractor_test.go`
- Modify: `internal/extract/reference/scoped_types.go`
- Modify: `internal/extract/reference/callee.go`

- [ ] **Step 1: Add failing package-value tests**

Create temporary fixture source in `index_test.go` containing:

```go
type Cache struct{}
func (*Cache) Read() {}

type Code string
func (Code) String() string { return "" }

var DefaultCache = new(Cache)
const DefaultCode Code = "default"
```

Assert:

```go
cacheType := idx.ValueReceiverTypes[string(ValueSymbolID("var", modulePath, "DefaultCache"))]
if cacheType.TypeName != "Cache" || cacheType.Confidence != facts.ConfidenceHigh {
    t.Fatalf("DefaultCache type = %#v", cacheType)
}
codeType := idx.ValueReceiverTypes[string(ValueSymbolID("const", modulePath, "DefaultCode"))]
if codeType.TypeName != "Code" || codeType.Confidence != facts.ConfidenceHigh {
    t.Fatalf("DefaultCode type = %#v", codeType)
}
```

- [ ] **Step 2: Run the focused index tests and verify RED**

Run:

```bash
go test ./internal/astindex -run 'TestBuildIndexes(NewBuiltin|TypedConst)ReceiverType' -count=1
```

Expected: FAIL because `new(T)` is treated as a constructor call and constants are not indexed in the receiver-type map.

- [ ] **Step 3: Generalize value receiver indexing**

In `Index`, replace `VarReceiverTypes` with:

```go
ValueReceiverTypes map[string]ValueType
```

Index exact receiver types for both `var` and `const` symbols. In `valueTypeFromExpr`, recognize:

```go
case *ast.CallExpr:
    if ident, ok := x.Fun.(*ast.Ident); ok && ident.Name == "new" && len(x.Args) == 1 {
        return valueTypeFromTypeExpr(file, x.Args[0])
    }
```

Keep constructor-name inference after the builtin check.

- [ ] **Step 4: Resolve variable and constant receiver kinds**

Refactor selector receiver lookup to try exact value symbols deterministically:

```go
for _, kind := range []string{"var", "const"} {
    id := ValueSymbolID(kind, packagePath, valueName)
    if valueType, ok := idx.ValueReceiverTypes[string(id)]; ok {
        return resolveFields(valueType, selectors)
    }
}
```

Do not search by method name or compatible type.

- [ ] **Step 5: Add failing reference tests**

Add tests proving:

```go
var Cache = new(cache)
Cache.Read()

const Current Code = "current"
Current.String()

func Handle() {
    local := new(cache)
    local.Read()
}
```

Each call must reference the exact concrete method with high confidence.

- [ ] **Step 6: Run reference tests and verify RED**

Run:

```bash
go test ./internal/extract/reference -run 'TestExtract(NewBuiltin|TypedConst)MethodCall' -count=1
```

Expected: package `new(T)`, local `new(T)`, or typed-constant cases fail before implementation.

- [ ] **Step 7: Add scoped `new(T)` inference**

In `scopedTypeFromValueExpr`, recognize the builtin before `scopedCallableID`:

```go
if ident, ok := unwrapGenericCallee(x.Fun).(*ast.Ident); ok &&
    ident.Name == "new" && len(x.Args) == 1 {
    return scopedTypeFromTypeExpr(file, x.Args[0])
}
```

Return the explicit type's existing high confidence.

- [ ] **Step 8: Run focused tests and verify GREEN**

Run:

```bash
go test ./internal/astindex ./internal/extract/reference -count=1
```

Expected: PASS.

### Task 2: Strict Package-Interface Binding Index

**Files:**
- Create: `internal/astindex/interface_bindings.go`
- Create: `internal/astindex/interface_bindings_test.go`
- Modify: `internal/astindex/index.go`

- [ ] **Step 1: Add failing unique-binding tests**

Use a temporary project with:

```go
type Client interface{ Fetch() }
type realClient struct{}
func (*realClient) Fetch() {}

var Default Client
func Init() { Default = new(realClient) }
```

Assert `ResolveSelectorMethodWithConfidence` resolves `Default.Fetch` to
`method:<module>:realClient:Fetch` with high confidence.

- [ ] **Step 2: Add failing rejection tests**

Add table-driven cases:

```go
Default = new(realClient)
Default = new(otherClient) // reject: two concrete types

Default = new(realClient)
Default = buildClient()    // reject: unknown RHS

func Configure() {
    Default := new(otherClient)
    Default.Fetch()        // local declaration must not mutate package binding
}

func Configure(Default Client) {
    Default = new(otherClient) // parameter must not mutate package binding
}
```

Assertions:

- unique same-type assignments resolve;
- multiple concrete types do not resolve;
- exact plus unknown does not resolve;
- local shadowing does not alter the package binding.

- [ ] **Step 3: Run binding tests and verify RED**

Run:

```bash
go test ./internal/astindex -run 'TestInterfaceBinding' -count=1
```

Expected: FAIL because interface assignments are not indexed.

- [ ] **Step 4: Add interface metadata**

Extend `Index` with:

```go
InterfaceTypes    map[facts.SymbolID]struct{}
InterfaceBindings map[facts.SymbolID]InterfaceBinding
```

Define:

```go
type InterfaceBinding struct {
    DeclaredType      ValueType
    ConcreteTypes     map[string]ValueType
    HasUnknownBinding bool
}
```

Use stable `packagePath + "\x00" + typeName` candidate keys. Do not rely on map
iteration order when resolving.

- [ ] **Step 5: Add a second indexing pass**

After all declarations are indexed:

```go
for _, pkg := range p.Packages {
    for _, file := range pkg.Files {
        idx.indexPackageVariableAssignments(file)
    }
}
```

Only create interface binding records for package variables whose declared type is
an indexed project interface.

- [ ] **Step 6: Implement lexical assignment-target tracking**

In `interface_bindings.go`, maintain nested lexical scopes with declaration sets.
Seed function scope with receivers, parameters, and named results. Register:

- block-local `var` declarations;
- names introduced by `:=`;
- range key/value variables;
- nested function literals as independent function scopes.

For `name = rhs`, treat `name` as a package variable only when no active local
scope declares it. For `pkg.Name = rhs`, require an unshadowed project import alias.

Every known target assignment must add one high-confidence concrete type or set
`HasUnknownBinding`.

- [ ] **Step 7: Resolve only a strict unique binding**

Add:

```go
func (idx *Index) resolveUniqueInterfaceBinding(id facts.SymbolID) (ValueType, bool)
```

It returns a value only when:

```go
!binding.HasUnknownBinding &&
len(binding.ConcreteTypes) == 1 &&
candidate.Confidence == facts.ConfidenceHigh
```

Use it only after direct variable/constant concrete-type lookup. Verify the target
method symbol exists before returning a call edge.

- [ ] **Step 8: Run binding tests and verify GREEN**

Run:

```bash
go test ./internal/astindex -run 'TestInterfaceBinding' -count=1
```

Expected: PASS.

### Task 3: Reference Edges and No-False-Positive Cases

**Files:**
- Modify: `internal/extract/reference/extractor_test.go`
- Modify: `internal/extract/reference/callee.go`
- Modify: `internal/extract/reference/extractor.go`

- [ ] **Step 1: Add a failing interface propagation reference test**

Build a temporary multi-package fixture:

```go
// remote
var Client ClientAPI
func Init() { Client = new(client) }
func (*client) Fetch() {}

// controller
func Handle() { remote.Client.Fetch() }
```

Assert the call reference is exactly:

```text
func:<module>/controller::Handle
  -> method:<module>/remote:client:Fetch
```

- [ ] **Step 2: Add negative call-edge tests**

For ambiguous and unknown bindings, assert:

- no reference targets either concrete implementation;
- `symbol_reference_unresolved` is retained;
- ordinary external SDK receiver calls remain free of project-symbol diagnostics.

- [ ] **Step 3: Run tests and verify RED**

Run:

```bash
go test ./internal/extract/reference -run 'TestExtractStrictInterface' -count=1
```

Expected: unique binding fails to resolve before call-resolution integration.

- [ ] **Step 4: Integrate strict binding resolution**

Update selector resolution to consume the index result without adding a separate
implementation scan. Update unresolved-project-call classification so a rejected
project interface binding still produces the existing diagnostic.

- [ ] **Step 5: Run focused and package tests**

Run:

```bash
go test ./internal/astindex ./internal/extract/reference -count=1
```

Expected: PASS with ambiguous cases producing zero speculative edges.

### Task 4: Real BFF Regression Cases

**Files:**
- Modify: `scripts/smoke-real-projects.sh`
- Modify: `docs/validation/real-project-validation.md`

- [ ] **Step 1: Add the client interface-dispatch fixture**

Temporarily change logic inside:

```text
sl-sc1-bff-service/remote/oa/oa.go
(*oaClient).GetMerchant
```

Generate the diff after modification and run against the modified tree. Assert:

```text
method:.../remote/oa:oaClient:GetMerchant
  -> direct controller helper and service::GetMerchantInfo
  -> GET /api/bff-web/live/view/:salesId/redirect
  -> GET /api/bff-web/mc/form/redirect_info/:formId
  -> GET /sc1-internal/app-proxy/api/merchant/info
```

Reject every unexpected endpoint.

- [ ] **Step 2: Add the admin `new(T)` fixture**

Temporarily change logic inside:

```text
sl-sc1-admin-bff/pkg/auth/cache/auth_redis.go
(*AuthRedis).GetRedirectData
```

Assert the tree reaches `web.OauthCallback`, then `controller/auth.OauthCallback`,
and exactly:

```text
GET /admin/api/bff-web/auth/oauth/callback
```

- [ ] **Step 3: Add the typed-constant fixture**

Temporarily change:

```text
sl-sc1-admin-bff/service/uc/merchant_setting_code.go
(MerchantSettingCode).String
```

Assert the tree includes `merchantSettingController.Get` and exactly:

```text
POST /admin/api/bff-web/uc/merchant/setting/get
```

- [ ] **Step 4: Verify determinism and cleanup**

For every fixture:

```bash
cmp first.impact.json second.impact.json
git -C <bff-project> status --short
```

Expected: byte-identical reports and clean BFF worktrees.

- [ ] **Step 5: Record validation evidence**

Update `docs/validation/real-project-validation.md` with:

- source diff;
- root symbol;
- expected propagation path;
- exact expected endpoint set;
- rejected unexpected endpoints;
- repeated-run determinism result.

### Task 5: Full Verification and Architecture Documentation

**Files:**
- Modify: `ARCHITECTURE.md`

- [ ] **Step 1: Document the supported boundary**

Document that package-level interface dispatch is supported only with a unique,
high-confidence, closed production-source assignment set. Explicitly list ambiguous
bindings, interface parameters, interface fields, and runtime injection as
unsupported.

- [ ] **Step 2: Format changed Go files**

Run:

```bash
gofmt -w internal/astindex/index.go \
  internal/astindex/index_test.go \
  internal/astindex/interface_bindings.go \
  internal/astindex/interface_bindings_test.go \
  internal/extract/reference/callee.go \
  internal/extract/reference/scoped_types.go \
  internal/extract/reference/extractor_test.go
```

- [ ] **Step 3: Run all verification**

Run:

```bash
go test ./...
go vet ./...
git diff --check
./scripts/smoke-real-projects.sh
```

Expected: all commands exit zero.

- [ ] **Step 4: Inspect final worktrees and diff**

Run:

```bash
git status --short
git diff --stat
git -C ../sl-sc1-bff-service status --short
git -C ../sl-sc1-admin-bff status --short
```

Expected: only intended `go-analyzer` files are changed; both BFF repositories are
clean.

No commit is included in this plan because the current task explicitly requires
reviewing the completed implementation before committing.
