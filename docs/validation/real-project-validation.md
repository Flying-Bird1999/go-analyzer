# Real Project Validation

This document tracks smoke validation for the first two target BFF projects:

- `sl-sc1-bff-service`
- `sl-sc1-admin-bff`

Run:

```bash
bash scripts/smoke-real-projects.sh
```

The CLI accepts absolute paths at the command boundary. The smoke script
resolves the sibling demo projects to absolute paths, writes JSON outputs to
`.analyzer-smoke/`, and validates that each output is parseable JSON.
Optional `--config` files are JSON and must also be passed as absolute paths.

## Current Expectations

The MVP validation target is stability rather than perfect precision:

- Both projects should run without panic.
- Facts JSON should be parseable.
- Annotation, route, symbol, and diagnostic counts should be recorded from each
  smoke run.
- Impact smoke should record changed source count, changed root count, impact
  tree node count and endpoint count.
- Unsupported patterns should appear as diagnostics instead of being silently
  lost where the analyzer can identify them.

## Latest Facts Smoke Snapshot

Local smoke run on 2026-06-27:

| Project | Symbols | Annotations | Routes | Diagnostics |
| --- | ---: | ---: | ---: | ---: |
| `sl-sc1-bff-service` | 781 | 32 | 32 | 20 |
| `sl-sc1-admin-bff` | 5137 | 463 | 490 | 213 |

All current diagnostics are `symbol_reference_unresolved`. The inspected
examples are project-local generated clients, package-level service clients and
structured error values whose concrete receiver type cannot yet be inferred by
the AST-only resolver. The analyzer keeps the resolved portions of each file.

## Impact Fixture Snapshot

The checked-in `type-impact` fixture validates the post-change single-snapshot
path:

```text
Address
  -> CreateOrderRequest
  -> OrderAPI.Create
  -> POST route
  -> POST /orders annotation
  -> POST /orders endpoint
```

The smoke script records:

- changed source count.
- changed root count.
- impact tree node count.
- endpoint count.
- unresolved-reference diagnostics.
- runtime.

Latest fixture result:

| Schema | Changed sources | Changed roots | Tree nodes | Endpoints | Unresolved diagnostics | Runtime |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `go-impact/v1alpha1` | 1 | 1 | 9 | 1 (`POST /orders`) | 0 | <1s |

## Specialized Impact Fixtures

The smoke script also validates the three post-MVP propagation paths:

| Fixture | Scenario | Expected endpoint |
| --- | --- | --- |
| `deleted-route` | multiline route registration removed from post-change source | `POST /public/orders` |
| `gomod-impact` | require-block dependency upgrade to local import usage | `GET /api/checkIn` |
| `middleware-selector` | package var + struct field middleware method change | `GET /orders` |

All three fixtures currently complete with one impacted endpoint and no
diagnostics.

## Known Unsupported Patterns

- Dynamic route paths are preserved as raw expressions and reported with
  `route_dynamic_path`.
- Indirect route handlers such as map/slice lookups are reported with
  `route_unresolved_handler`.
- go.mod changes propagate conservatively from changed modules to all resolved
  local import usages; external module API/source differences are not analyzed.
- Declarations absent from the post-change snapshot can degrade to
  `deleted_symbol_unresolved`.
- Interface dispatch, reflection, flow-sensitive local receiver inference and
  full runtime route reconstruction remain outside the AST-only scope.
