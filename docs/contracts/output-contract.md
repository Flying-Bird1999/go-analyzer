# go-analyzer Output Contract

This document defines the JSON contracts exposed by `go-analyzer`.

## Schemas

```bash
go-analyzer schema --type facts
go-analyzer schema --type impact
```

All CLI path flags require absolute paths. Output ordering is deterministic for
stable project contents and diff input.

## Facts Output

`facts` is the extraction/debug contract:

```bash
go-analyzer facts --project /absolute/path/to/project --format json
```

It exposes project metadata, symbols, annotations, route groups, routes,
middleware bindings, current go.mod dependencies, changes, references, links
and non-fatal diagnostics. Facts are useful when validating extractors; they
are not the primary MR review report.

## Impact Output

`impact` is the human-reviewable MR impact report:

```bash
go-analyzer impact \
  --project /absolute/path/to/project \
  --diff /absolute/path/to/change.diff \
  --format json
```

Schema version: `go-impact/v1alpha1`.

Top-level shape:

```json
{
  "meta": {
    "schemaVersion": "go-impact/v1alpha1",
    "projectRoot": "/absolute/path/to/project",
    "diagnostics": []
  },
  "fileSources": []
}
```

### `fileSources`

Every changed source file is retained, including sources that do not reach an
endpoint:

```json
{
  "sourceFile": "model/order.go",
  "diff": "diff --git ...",
  "symbols": {},
  "impactedEndpoints": []
}
```

- `sourceFile` is project-relative.
- `diff` is the original per-file unified diff when `analysis.includeDiff` is
  enabled.
- `symbols` is an object keyed by the stable changed root ID.
- file-level fallback uses the reserved key `__non_symbol__`.
- `impactedEndpoints` is the deduplicated summary of endpoint leaves reachable
  from roots in this source file.

### Recursive impact nodes

Each symbol root and descendant contains:

- `id`, `kind`, `name`.
- project-relative `file` and Go `package`.
- `relation`: edge used to reach this node, such as `call`, `type_ref`,
  `value_ref`, `registered_handler`, `handler_annotation`.
- `raw`, `span`, `confidence`: source evidence for the edge.
- `level`.
- `cycle` and `stopBoundary` when propagation terminates intentionally.
- `children`, always an array.
- `method` and `path` for route, annotation and endpoint nodes.

The normal endpoint path is:

```text
changed symbol
  -> dependent symbol(s)
  -> route
  -> annotation
  -> endpoint
```

Struct field and tag changes map to their owning `type` symbol. The analyzer
does not emit field-level change facts.

### Diagnostics

Recoverable failures are reported in `meta.diagnostics`, including unresolved
project references, parse failures in individual Go files, deletion-only
fallbacks and propagation depth truncation. They do not remove successfully
analyzed roots.

## Single-snapshot limitation

Impact analysis indexes the post-change project only. A deletion-only hunk is
anchored to a surviving declaration when possible. If the deleted declaration
no longer exists, the report retains a file fallback root and emits
`deleted_symbol_unresolved`.

Dual base/head snapshots are intentionally deferred.
