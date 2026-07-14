# go-analyzer Output Contract

This document defines the JSON contracts exposed by `go-analyzer`.

## Schemas

```bash
go-analyzer schema --type facts
go-analyzer schema --type impact
```

CLI path flags require absolute paths. Output ordering is deterministic for
stable project contents and diff input.
`--goos`, `--goarch`, `--tags`, and `--cgo` override the Go build context used
for build-constraint filtering. `--timings` writes stage timings to stderr only;
it does not change JSON stdout.

## Facts Output

`facts` is the extraction and debugging contract:

```bash
go-analyzer facts --project /absolute/path/to/project --format json
```

It retains project metadata, symbols, annotations, routes, middleware, current
module dependencies, references, IM event facts, links, source spans, raw
evidence and diagnostics. Diff-only transient facts (`changes`, `module_changes`, and
`module_usages`) are internal to impact analysis and are not emitted by the
`facts` command.

`project.build_context` records the effective Go build context used for file
loading. Route registrations and references include a normalized `evidence`
array with expression kind, raw source text, span, and confidence.

`im_events` records each discovered outbound IM send with its sender symbol,
static event value when resolvable, payload/event/control dependencies,
evidence spans and resolution state. It is a debugging contract; the impact
report projects only event names and propagation nodes.

`facts` also emits `grpc_operations` and `grpc_calls`. Operations are discovered
from the selected module dependency graph's generated client transport source;
calls are emitted only when a project selector call has one exact generated
client binding. `facts` records gRPC discovery failures as diagnostics, while
`impact --grpc` and `endpoint-assets` fail atomically.

## BFF gRPC Dependency Output

```bash
go-analyzer endpoint-assets --project /absolute/path/to/project --endpoint "GET /orders/:id"
```

`endpoint-assets` returns `{ "project": {"module": ...}, "endpointAssets": [...] }`.
Each asset contains `endpoint`, `routes`, `handlers`, and `dependencies.grpc`;
every gRPC item contains canonical identity, generated client bindings, and endpoint-to-call-site
chains. `endpoint` is the annotation-first formal identity. `routes` lists route
paths that can be statically resolved for the handler; it is auxiliary evidence and may be empty
or incomplete for dynamic registration. Chains always point from BFF endpoint handler to the project gRPC call site.

Inputs are exact and repeatable. An unknown endpoint is an error. Errors write
`error_code=<stable-code> message=<message>` to stderr and do not write partial
JSON to stdout. No additional `schema --type` is introduced for endpoint assets.

## Impact Output

`impact` is the human-reviewable BFF impact report. `--diff` and repeatable
`--grpc` are independent sources and may be combined:

```bash
go-analyzer impact \
  --project /absolute/path/to/project \
  --diff /absolute/path/to/change.diff \
  --grpc "/package.OrderService/GetOrder" \
  --format json
```

Top-level shape:

```json
{
  "summary": {
    "impactedEndpointCount": 1,
    "impactedEndpoints": [
      {"method": "POST", "path": "/orders"}
    ],
    "impactedIMCount": 1,
    "impactedIMEvents": ["order/changed"]
  },
  "fileSources": [],
  "grpcSources": [],
  "endpointSourcesSummary": []
}
```

- `summary` is the globally deduplicated endpoint and concrete IM event result.
- `fileSources` contains ordinary source-file changes and their complete trees.
- `moduleSources` is optional. It contains semantic go.mod changes and their
  local usage trees, and is omitted when there are no emitted module changes.
- `grpcSources` contains each requested canonical gRPC operation, its statically
  proven BFF consumers, their generated client binding and endpoint-to-call-site
  chains. A consumer exposes both annotation-first `endpoint` and auxiliary
  `routes` route evidence. The same field is included in endpoint summaries. Consumer `relation` is always `may_call`: it proves static reachability,
  not that every request executes the call.
- `endpointSourcesSummary` is a lightweight endpoint-to-source projection placed
  last in the rendered JSON. It is always present and is an empty array when no
  endpoint is impacted.

## gRPC Service Impact Output

`grpc-impact` uses a separate contract for gRPC Provider projects while retaining the BFF impact source-tree structure:

```json
{
  "summary": {
    "impactedGrpcOperationCount": 1,
    "impactedGrpcOperations": [
      {
        "fullMethod": "/package.Service/Method",
        "protoPackage": "package",
        "service": "Service",
        "method": "Method"
      }
    ]
  },
  "fileSources": [
    {
      "sourceFile": "provider/service.go",
      "diff": "...",
      "symbols": {},
      "impactedGrpcOperations": []
    }
  ],
  "grpcOperationSourcesSummary": []
}
```

- `summary` is the deduplicated formal result.
- `fileSources` retains the applied diff and recursive `ImpactNode` evidence.
- `moduleSources` is emitted only for semantic go.mod changes.
- `grpcOperationSourcesSummary` is the operation-to-file/module reverse view.
- gRPC terminal nodes use `kind=grpc_operation`, `relation=exposed_grpc_operation`, and canonical `fullMethod`.

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
  "impactedEndpoints": [],
  "impactedIMEvents": []
}
```

- `sourceFile` is project-relative.
- `diff` is the original per-file unified diff and is always retained.
- `symbols` is keyed by stable changed-root ID and contains recursive trees.
- file fallback uses the reserved `__non_symbol__` key.
- `impactedEndpoints` is the deduplicated endpoint result for this source.
- `impactedIMEvents` is the sorted, deduplicated set of statically resolved IM
  event strings for this source.

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
      "impactedEndpoints": [],
      "impactedIMEvents": []
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

### `endpointSourcesSummary`

`endpointSourcesSummary` lets consumers answer why an endpoint appears without
manually joining all source trees:

```json
{
  "method": "GET",
  "path": "/api/bff-web/live/view/:salesId/redirect",
  "sources": [
    {
      "sourceType": "file",
      "sourceFile": "remote/oa/oa.go",
      "rootSymbols": [
        {
          "id": "type:example.com/app/remote/oa::OAClient",
          "kind": "type",
          "name": "OAClient",
          "file": "remote/oa/oa.go"
        }
      ],
      "chains": [
        [
          "type OAClient",
          "var Client",
          "func GetMerchantInfo",
          "func LiveViewRedirect",
          "GET /api/bff-web/live/view/:salesId/redirect"
        ]
      ],
      "confidence": "high"
    }
  ]
}
```

- `sources[]` contains ordinary file sources and module usage sources that reach
  the endpoint.
- `sourceType` is `file` or `module`. Module sources also include `modulePath`,
  `changeType`, `versionBefore`, and `versionAfter`.
- `rootSymbols[]` lists changed roots from that source that can reach the
  endpoint.
- `chains[]` contains at most one shortest human-readable chain per root symbol.
- `confidence` is the weakest confidence on the selected chains.
- Full recursive evidence remains in `fileSources[].symbols` and
  `moduleSources[].sourceFiles[].symbols`; this summary intentionally avoids
  copying the full tree.

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

Resolved IM terminals use `kind: "im_event"` and put the concrete event string
in `name`. Dynamic event expressions use `kind: "im_event_unresolved"` so the
reviewer can see the incomplete path. Unresolved terminals are intentionally
excluded from `impactedIMCount`, `impactedIMEvents`, and source event summaries.

The public impact contract reports only the event identity. It does not expose
app ID, delivery mode, payload expressions, or changed payload fields. The
source diff and recursive tree retain the evidence needed for human or agent
review.

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

## IM Analysis Boundary

IM impact starts from the BFF-local unified diff and post-change source. It
does not inspect sc1-server or infer upstream schema changes across repositories.
Protocol implementations are discovered only when both `broadcast://` and
`/broadcast/send` anchors are present. Common IM SDKs are matched through
built-in exact import/function adapters. No project configuration is required
for IM discovery.
