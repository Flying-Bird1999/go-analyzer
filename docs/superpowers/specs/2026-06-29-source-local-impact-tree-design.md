# Source-Local Impact Tree Design

## Goal

Keep the impact JSON as an original, human-reviewable analysis report. Each
change source must contain its own complete propagation chain.

## Reference

The structure follows the readable raw report in `ts-analyzer`:

- `fileSources[].symbols` is the primary file-change propagation tree.
- package-driven `sourceFiles[].symbols` uses the same recursive shape.
- compact/final projections are downstream concerns and do not replace the raw
  report.

## Go Impact Shape

```json
{
  "summary": {},
  "fileSources": [
    {
      "sourceFile": "model/order.go",
      "diff": "diff --git ...",
      "symbols": {
        "type:example.com/model::Order": {
          "id": "type:example.com/model::Order",
          "kind": "type",
          "children": []
        }
      },
      "impactedEndpoints": []
    }
  ],
  "moduleSources": [
    {
      "modulePath": "github.com/shopspring/decimal",
      "sourceFiles": [
        {
          "sourceFile": "util/transform/transform.go",
          "symbols": {},
          "impactedEndpoints": []
        }
      ]
    }
  ]
}
```

There is no top-level shared `nodes` map. Ordinary file changes retain their
unified diff. Module source files do not duplicate the go.mod diff.

## Propagation Nodes

Each recursive node retains review evidence:

- stable `id`, `kind`, optional `name`;
- project-relative `file` and optional package;
- incoming `relation`, raw source expression, confidence and level;
- cycle and stop-boundary markers;
- route/annotation/endpoint method and path;
- recursive `children`.

Endpoint nodes remain in the tree so a reviewer can follow a root all the way
to the HTTP endpoint. Per-source and global endpoint summaries remain for
quick consumption.

`span` stays excluded from impact JSON. Diagnostics remain optional and flat,
without spans or internal IDs.

## Module Sources

`moduleSources` keeps semantic module change metadata and groups propagated
usage roots by local source file. Every `sourceFiles[]` has the same `symbols`
and `impactedEndpoints` shape as `fileSources[]`.

For the decimal validation case the report must visibly contain:

```text
github.com/shopspring/decimal
  -> util/transform/transform.go
  -> ParseStringToFloat64
  -> ConvertPrice
  -> affected endpoints
```

## Compatibility

Facts output and internal impact trees are unchanged. The rejected compact DAG
is removed from the public impact schema, tests, golden output and docs.
