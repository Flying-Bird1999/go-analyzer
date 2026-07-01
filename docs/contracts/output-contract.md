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

It retains project metadata, symbols, annotations, routes, middleware, current
module dependencies, references, links, source spans, raw evidence and
diagnostics. Diff-only transient facts (`changes`, `module_changes`, and
`module_usages`) are internal to impact analysis and are not emitted by the
`facts` command.

## Impact Output

`impact` is the original human-reviewable MR impact report:

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
  "moduleSources": []
}
```

- `summary` is the globally deduplicated endpoint result.
- `fileSources` contains ordinary source-file changes and their complete trees.
- `moduleSources` contains semantic go.mod changes and their local usage trees.

### `fileSources`

Every ordinary changed file is retained, including changes that reach no
endpoint:

```json
{
  "sourceFile": "model/order.go",
  "diff": "diff --git ...",
  "symbols": {
    "type:example.com/app/model::Order": {
      "id": "type:example.com/app/model::Order",
      "kind": "type",
      "name": "Order",
      "file": "model/order.go",
      "confidence": "high",
      "level": 0,
      "children": []
    }
  },
  "impactedEndpoints": []
}
```

- `sourceFile` is project-relative.
- `diff` is the original per-file unified diff and is always retained.
- `symbols` is keyed by stable changed-root ID and contains recursive trees.
- file fallback uses the reserved `__non_symbol__` key.
- `impactedEndpoints` is the deduplicated endpoint result for this source.

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
      "symbols": {
        "func:example.com/app/util/transform::ParseStringToFloat64": {
          "id": "func:example.com/app/util/transform::ParseStringToFloat64",
          "kind": "func",
          "children": []
        }
      },
      "impactedEndpoints": []
    }
  ]
}
```

- version and optional replacement fields describe the semantic go.mod change.
- `basis` is `matched_import_usage`, `matched_file_usage`, or
  `module_unreferenced`.
- `sourceFiles` uses the same recursive `symbols` and endpoint summary shape as
  `fileSources`.
- module usage files do not duplicate the go.mod diff.
- a resolved go.mod change is not repeated as a file fallback source.
- module changes ignored by optional impact config are omitted from
  `moduleSources` and from the top-level `summary`.

### Recursive Impact Nodes

Every root and descendant may contain:

- `id`, `kind`, optional `name`;
- project-relative `file` and optional Go `package`;
- incoming `relation` and source `raw`;
- `confidence` and `level`;
- optional `cycle`;
- recursive `children`;
- optional `method` and `path` for route, annotation and endpoint nodes.

Endpoint nodes remain in the tree so reviewers can follow a changed symbol to
the final HTTP endpoint without joining another top-level graph.

Source spans are intentionally absent from impact JSON. Full spans remain
available from `facts`.

### Confidence

`confidence` describes static evidence strength, not probability, and does not
control propagation:

- `high`: direct AST/fact evidence.
- `medium`: targeted inference or fallback.
- `low`: weak file-level fallback.

## Single-snapshot Limitation

Impact analysis requires the unified diff to be applied to the project passed
with `--project`. Added and context lines are checked before AST indexing;
an empty diff, unsafe path, deleted file that still exists, or pre-change
snapshot is rejected. Changed Go files that cannot be parsed are also rejected.

A deletion-only hunk is
anchored to a surviving declaration when possible. Deleted route registrations
are additionally parsed from diff lines to recover their handler and endpoint.
Other deleted declarations can degrade to a file fallback and
`deleted_symbol_unresolved`.

Dual base/head snapshots are intentionally deferred.
