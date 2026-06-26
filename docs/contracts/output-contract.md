# go-analyzer Output Contract

This document defines the MVP output contract consumed by downstream tools.

## Version

Current contract version: `v1alpha1`.

The contract is additive by default:

- Existing field names keep their meaning.
- New optional fields may be added.
- Required top-level arrays remain present even when empty.
- Output order is deterministic for stable diffs and golden tests.

## Schemas

Print the JSON schemas from the CLI:

```bash
go-analyzer schema --type facts
go-analyzer schema --type impact
```

`facts` output is produced by:

```bash
go-analyzer facts --project /absolute/path/to/project --format json
```

`impact` output is produced by:

```bash
go-analyzer impact --project /absolute/path/to/project --diff /absolute/path/to/change.diff --format json
```

All CLI path flags require absolute paths. Optional project config is passed
with `--config /absolute/path/to/go-analyzer.json`.

## Facts Output

Top-level fields:

- `project`: analyzed project metadata.
- `symbols`: Go function, method, type, var, and const symbols.
- `annotations`: controller comment endpoint annotations.
- `route_groups`: route group context facts.
- `routes`: route registration facts.
- `middleware`: middleware binding facts.
- `changes`: mapped code or module change facts.
- `references`: symbol reference facts.
- `modules`: parsed `go.mod` dependencies.
- `module_changes`: mapped `go.mod` dependency changes.
- `module_usages`: local usage facts for changed modules.
- `links`: route-handler and handler-annotation links.
- `diagnostics`: non-fatal extraction diagnostics.

## Impact Output

Top-level fields:

- `impacted_endpoints`: impacted HTTP endpoints.
- `evidence_chains`: evidence graph for each endpoint impact.
- `module_impacts`: changed module impact summary.
- `diagnostics`: impact-stage diagnostics.

`impacted_endpoints[*].method` and `impacted_endpoints[*].path` are sourced from
controller annotations in the MVP. Route facts are used as evidence and for route
context propagation.
