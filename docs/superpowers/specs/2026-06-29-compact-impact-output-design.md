# Compact Impact Output Design

## Goal

Reduce the public impact JSON to analysis-relevant data while preserving source
attribution, dependency attribution, endpoint conclusions, and explainable
propagation.

The internal facts and impact tree remain unchanged. Only the public output
projection is replaced.

## Reference

`ts-analyzer` separates its internal analysis model from a smaller readable
projection. The Go analyzer follows the same boundary:

- facts retain complete AST evidence and source spans;
- impact output contains a compact, deterministic consumption model;
- ordinary file changes and module changes remain separate source types.

## Public Shape

```json
{
  "summary": {
    "impactedEndpointCount": 1,
    "impactedEndpoints": [
      {"method": "GET", "path": "/products"}
    ]
  },
  "fileSources": [
    {
      "sourceFile": "controller/product.go",
      "diff": "diff --git ...",
      "roots": [
        {"id": "func:example.com/project/controller::GetProduct"}
      ],
      "impactedEndpoints": [
        {"method": "GET", "path": "/products"}
      ]
    }
  ],
  "moduleSources": [
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
            {"id": "func:example.com/project/util/transform::ParseStringToFloat64"}
          ],
          "impactedEndpoints": []
        }
      ]
    }
  ],
  "nodes": {
    "func:example.com/project/model::ConvertPrice": {
      "kind": "func",
      "name": "ConvertPrice",
      "file": "model/form_product.go",
      "children": [
        {
          "to": "method:example.com/project/controller:handler:convert",
          "relation": "call"
        }
      ]
    }
  }
}
```

## Source Projection

`fileSources` contains ordinary diff-driven roots. `moduleSources` contains
go.mod semantic changes and project-local usage roots.

Each source contains:

- project-relative `sourceFile`;
- original unified `diff` for ordinary changed files;
- unique root IDs;
- endpoints attributable to those roots.

A root optionally carries `confidence` only when it is `medium` or `low`.
`high` is the default and is omitted.

## Node Graph

`nodes` is a document-wide map keyed by stable node ID. Repeated tree instances
merge into one node, converting the recursive tree to a directed graph.

A node contains only:

- `kind`;
- human-readable `name` for non-route symbols;
- project-relative `file`;
- `method` and `path` where applicable;
- `stopBoundary` when true;
- unique outgoing `children` edges.

An edge contains:

- target node ID in `to`;
- `relation`;
- `confidence` only for `medium` or `low`.

Endpoint leaves are not nodes because endpoint conclusions already exist in
source and global endpoint lists. Route and annotation nodes retain
machine-readable method/path evidence.

Consumers must use a visited set when walking `nodes`, because the graph may
contain cycles.

## Removed Impact Fields

The public impact output does not contain:

- `meta` or absolute `projectRoot`;
- `raw`;
- `span` anywhere, including diagnostics;
- `package`;
- `level`;
- node-local `id`;
- `cycle`;
- repeated `confidence: high`;
- repeated endpoint nodes;
- constant `impactBasis.kind`.

Facts output continues to expose complete source spans and raw extraction data.

## Diagnostics

Relevant diagnostics remain optional at the top level. Public diagnostics
contain only:

- `code`;
- `severity`;
- `message`;
- optional project-relative `file`.

Internal diagnostic IDs, source spans, and related fact IDs remain in facts.

## Configuration

`analysis.includeDiff` and `analysis.includeRawEvidence` are removed. Ordinary
file diffs are always retained, while raw per-edge AST evidence is never
published by the compact impact contract. No replacement config is added.

## Acceptance

- repeated impact tree instances produce one entry per unique node ID;
- impact output contains no `span`, `raw`, `package`, `level`, or
  absolute project root;
- only non-high confidence is serialized;
- endpoint totals and file/module attribution are unchanged;
- the real decimal fixture still reports 10 module endpoints plus one ordinary
  file endpoint;
- the compact real report is materially smaller than the current report.
