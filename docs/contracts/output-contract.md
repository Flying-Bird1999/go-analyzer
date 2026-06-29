# go-analyzer Output Contract

This document defines the JSON contracts exposed by `go-analyzer`.

## Schemas

```bash
go-analyzer schema --type facts
go-analyzer schema --type impact
```

CLI path flags require absolute paths. Output ordering is deterministic for
stable project contents and diff input.

## Facts Output

`facts` is the extraction and debugging contract:

```bash
go-analyzer facts --project /absolute/path/to/project --format json
```

It retains complete project metadata, symbols, annotations, routes,
middleware, module facts, references, links, source spans, raw evidence and
diagnostics. It is intended for analyzer development, not as the primary MR
report.

## Impact Output

`impact` is the compact MR impact contract:

```bash
go-analyzer impact \
  --project /absolute/path/to/project \
  --diff /absolute/path/to/change.diff \
  --format json
```

Top-level shape:

```json
{
  "summary": {
    "impactedEndpointCount": 1,
    "impactedEndpoints": [
      {"method": "POST", "path": "/orders"}
    ]
  },
  "fileSources": [],
  "moduleSources": [],
  "nodes": {}
}
```

- `summary` is the globally deduplicated endpoint result.
- `fileSources` contains ordinary source-file changes.
- `moduleSources` contains semantic go.mod changes and their local usages.
- `nodes` is one document-wide propagation DAG keyed by stable node ID.
- `diagnostics` is optional and contains only recoverable issues relevant to
  the current diff or reachable graph.

The impact contract deliberately omits project root, source spans, raw
expressions, package names, graph levels and endpoint leaf nodes. Complete
evidence remains available from `facts`.

### `fileSources`

Every ordinary changed file is retained, including changes that reach no
endpoint:

```json
{
  "sourceFile": "model/order.go",
  "diff": "diff --git ...",
  "roots": [
    {"id": "type:example.com/app/model::Order"}
  ],
  "impactedEndpoints": [
    {"method": "POST", "path": "/orders"}
  ]
}
```

- `sourceFile` is project-relative.
- `diff` is the original per-file unified diff and is always retained for
  ordinary diff sources.
- `roots` references entries in the top-level `nodes` map.
- file fallback uses the stable `__non_symbol__` root ID.
- `impactedEndpoints` is the deduplicated endpoint result for this file.

### `moduleSources`

Resolved dependency changes are separate from ordinary source changes:

```json
{
  "modulePath": "github.com/shopspring/decimal",
  "changeType": "upgraded",
  "versionBefore": "v1.3.1",
  "versionAfter": "v1.4.0",
  "basis": "matched_import_usage",
  "sourceFiles": [
    {
      "sourceFile": "util/transform/transform.go",
      "roots": [
        {"id": "func:example.com/app/util/transform::ParseStringToFloat64"}
      ],
      "impactedEndpoints": []
    }
  ]
}
```

- version and optional replacement fields describe the semantic go.mod change.
- `basis` is `matched_import_usage`, `matched_file_usage`, or
  `module_unreferenced`.
- `sourceFiles` contains local usage roots and endpoint summaries. It does not
  duplicate the go.mod diff.
- a resolved go.mod change is not repeated as a file fallback source.

### `nodes`

Each graph entry may contain:

- `kind`, optional `name`, and project-relative `file`.
- optional route/annotation `method` and `path`.
- optional `stopBoundary`.
- `children`, where each edge contains `to`, optional `relation`, and optional
  non-high `confidence`.

Endpoint leaves are represented by `summary` and per-source
`impactedEndpoints`, so they are not duplicated in `nodes`. Cycles are ordinary
DAG references instead of repeated recursive objects.

### Confidence

`confidence` describes static evidence strength, not probability, and does not
control propagation:

- `high`: direct AST/fact evidence.
- `medium`: targeted inference or fallback.
- `low`: weak file-level fallback.

High confidence is the default and is omitted from compact impact JSON.
`medium` and `low` remain on affected roots or edges so consumers can highlight
results that deserve review.

### Diagnostics

Relevant recoverable failures appear in the optional top-level `diagnostics`
array with `code`, `severity`, `message`, and optional project-relative `file`.
Diagnostic spans, IDs and related fact IDs remain available only from `facts`.

## Single-snapshot limitation

Impact analysis indexes the post-change project. A deletion-only hunk is
anchored to a surviving declaration when possible. Deleted route registrations
are additionally parsed from diff lines to recover their handler and endpoint.
Other deleted declarations can degrade to a file fallback and
`deleted_symbol_unresolved`.

Dual base/head snapshots are intentionally deferred.
