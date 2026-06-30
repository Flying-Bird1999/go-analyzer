# Strict Value and Interface Resolution

## Context

`go-analyzer` currently misses three call edges that exist in the supported Lego BFF repositories:

1. A package variable is declared as an interface and assigned one concrete implementation:
   `var Client OAClient`, followed by `Client = new(oaClient)`.
2. A package variable is initialized with the Go builtin `new(T)`:
   `var AuthCache = new(AuthRedis)`.
3. A named constant invokes a method declared on its named type:
   `MER_SET_ORDER_STATUS_SWITCH.String()`.

Real post-change-source diff verification produced the same result for all three cases:
the changed method was selected as the file-source root, but it had no propagation
children and the report contained zero impacted endpoints.

The required correctness policy is strict: a missing edge is preferable to an edge
that cannot be proven from production source. The analyzer must not connect an
interface call to every type that happens to implement the interface.

## Goals

- Resolve `new(T)` to `T` for package and local value inference.
- Resolve methods called on typed constants.
- Resolve calls through a package-level interface variable when production source
  proves that the variable can hold exactly one concrete project type.
- Preserve deterministic output.
- Keep the implementation inside the existing AST index and reference extraction
  architecture.

## Non-goals

- General interface dispatch for parameters, struct fields, container elements, or
  returned interface values.
- Whole-program points-to analysis.
- SSA call-graph construction.
- Guessing implementations from method-set compatibility or method names.
- Reading test-only mock assignments as production dispatch evidence.
- Resolving reflection, plugin loading, unsafe mutation, or runtime dependency
  injection not represented by project source.

## Correctness Invariants

An interface call edge may be created only when all of the following are true:

1. The receiver is a package-level variable indexed by symbol identity.
2. Its declared type is an interface declared in the analyzed project.
3. Every production-source assignment discovered for that variable has a receiver
   type that can be resolved exactly.
4. All resolved assignments produce the same concrete package path and type name.
5. No assignment to the variable has an unresolved right-hand side.
6. The concrete type declares the called method in the symbol index.
7. The assignment target is proven to be the package variable, not a shadowing
   local identifier or import alias.

If any invariant fails, no call edge is created. Existing unresolved-reference
diagnostics remain the signal that propagation stopped.

The analysis uses the loaded project as its closed production-source world.
`_test.go` files are excluded by the project loader and therefore test mocks do not
make a production binding ambiguous.

## Index Model

### Value types

Generalize the current variable-only receiver type index into a value receiver type
index keyed by the complete `SymbolID`. It stores exact declared or initialized
types for both:

- `var` symbols;
- typed `const` symbols.

The resolver must look up the correct symbol kind instead of assuming `var`.

### Builtin allocation

Both package-level and scoped expression inference recognize:

```go
new(T)
```

as the exact type `T`. This case must be checked before treating a call expression
as a project function or constructor heuristic. Because `new` is a Go builtin and
the type argument is explicit, its inferred confidence is high.

Existing handling of `T{}` and `&T{}` remains unchanged.

### Interface bindings

Add an interface binding record keyed by the package-level variable `SymbolID`:

```text
declared interface type
concrete candidate set
has unknown assignment
```

Indexing runs in two conceptual passes:

1. Index declarations, named interfaces, methods, fields, return types, variables,
   and constants.
2. Inspect production function bodies and package initializers for assignments to
   indexed package variables.

An assignment contributes:

- one exact concrete type when the RHS is `new(T)`, `T{}`, `&T{}`, or another
  value whose declared concrete type is already indexed with high confidence;
- an unknown-assignment marker when the target is known but the RHS type is not
  exact.

Repeated assignments of the same concrete type do not create ambiguity. Two
different concrete types do.

Constructor-name inference, inferred interface return values, unresolved function
results, and any medium- or low-confidence value type count as unknown assignments.
They must not participate in strict interface dispatch.

## Assignment Target Resolution

Assignment collection must respect lexical scope. An identifier assignment such as
`Client = value` is attributed to a package variable only when no active local
declaration shadows `Client`.

The local scope model must account for:

- receiver, parameter, and named-result declarations;
- `var` declarations;
- short declarations;
- range variables;
- nested block scopes.

A selector assignment such as `oa.Client = value` is attributed to the imported
package variable only when `oa` resolves to the file import and is not shadowed by
a local declaration.

Assignments include both declaration initializers and later `=` assignments.
Compound assignments are irrelevant because interface values and receiver objects
cannot be meaningfully rebound through them.

## Call Resolution

Resolution order remains deterministic and moves from strongest local evidence to
broader indexed evidence:

1. Direct package function.
2. Scoped concrete value type.
3. Package variable or typed constant receiver type.
4. Strict unique interface binding.

For step 4, the resolver accepts a binding only when the candidate set has exactly
one concrete type and `has unknown assignment` is false. It then constructs the
concrete method `SymbolID` and verifies that the symbol exists.

No fallback searches all interface implementers. No edge is emitted for an
ambiguous binding.

## Confidence

- Explicit type declarations, typed constants, composite literals, and `new(T)`:
  high confidence.
- A unique interface binding satisfying every correctness invariant: high
  confidence, because it is based on complete assignment evidence in the loaded
  production project.
- Existing constructor-name and callable-return heuristics retain their current
  confidence rules.

Confidence does not relax the correctness invariants. A low- or medium-confidence
interface candidate must not be used to create a strict dispatch edge.

## Diagnostics

No new fields are added to the impact JSON.

When strict interface resolution rejects a call because assignments are unknown or
ambiguous, the existing `symbol_reference_unresolved` diagnostic remains available
in facts output. Diagnostics must not be promoted into endpoint impact results.

## Verification

### Unit tests

Add focused index and reference extraction coverage for:

- package variable initialized by `new(T)`;
- local variable initialized by `new(T)`;
- typed constant method call;
- interface variable with one concrete assignment;
- repeated assignments of the same concrete type;
- interface variable with two concrete assignments;
- interface variable with one exact and one unknown assignment;
- package variable shadowed by a local identifier;
- import alias shadowed by a local identifier;
- test mock assignments excluded from production indexing;
- deterministic reference facts across repeated extraction.

Negative tests must assert that ambiguous and unknown interface bindings produce no
call edge.

### Real BFF diff tests

Re-run post-change-source diffs for:

1. `sl-sc1-bff-service/remote/oa/oa.go`
   - changed root: `(*oaClient).GetMerchant`;
   - expected endpoints:
     `GET /api/bff-web/live/view/:salesId/redirect`,
     `GET /api/bff-web/mc/form/redirect_info/:formId`, and
     `GET /sc1-internal/app-proxy/api/merchant/info`.
2. `sl-sc1-admin-bff/pkg/auth/cache/auth_redis.go`
   - changed root: `(*AuthRedis).GetRedirectData`;
   - expected endpoint:
     `GET /admin/api/bff-web/auth/oauth/callback`.
3. `sl-sc1-admin-bff/service/uc/merchant_setting_code.go`
   - changed root: `(MerchantSettingCode).String`;
   - expected endpoint:
     `POST /admin/api/bff-web/uc/merchant/setting/get`.

For every fixture:

- run against the modified working tree that produced the diff;
- compare two runs byte-for-byte;
- inspect the complete propagation chain, not only the endpoint summary;
- reject unexpected endpoints;
- restore the BFF repository and verify a clean worktree.

Finally run all Go tests, `go vet ./...`, `git diff --check`, and the complete real
project smoke suite.

## Rollout

Implement in three independently testable increments:

1. Exact `new(T)` and typed-constant receiver inference.
2. Strict package-variable assignment collection and unique interface binding.
3. Real BFF regression fixtures and architecture documentation updates.

Do not introduce `go/types`, SSA, or a general implementation search unless future
real BFF cases cannot be represented by the strict assignment model.
