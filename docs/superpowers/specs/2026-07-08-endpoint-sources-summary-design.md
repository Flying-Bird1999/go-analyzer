# Endpoint Sources Summary Design

## Goal

Add an `endpointSourcesSummary` projection to impact JSON so consumers can answer
"why is this API impacted?" without manually joining `summary`,
`fileSources[].impactedEndpoints`, `moduleSources[].sourceFiles[]`, and recursive
impact trees.

## Output Shape

`endpointSourcesSummary` is a top-level field placed last in the rendered impact
document. When `moduleSources` exists, the stable order is:

```json
{
  "summary": {},
  "fileSources": [],
  "moduleSources": [],
  "endpointSourcesSummary": []
}
```

When there are no module sources, the stable order is:

```json
{
  "summary": {},
  "fileSources": [],
  "endpointSourcesSummary": []
}
```

Each item represents one impacted endpoint:

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
          "id": "type:sc1-client-bff-service/remote/oa::OAClient",
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

Module-origin summaries use the same endpoint item shape and add module metadata
to the source:

```json
{
  "sourceType": "module",
  "modulePath": "gopkg.inshopline.com/sc1/app/modules/medium/proto",
  "changeType": "upgraded",
  "versionBefore": "v1.9.1",
  "versionAfter": "v1.9.8",
  "sourceFile": "service/live/cart.go",
  "rootSymbols": [],
  "chains": [],
  "confidence": "medium"
}
```

## Semantics

- `endpointSourcesSummary` is a lightweight projection, not a replacement for
  recursive trees.
- Full evidence remains in `fileSources[].symbols` and
  `moduleSources[].sourceFiles[].symbols`.
- `sources[]` answers which changed file or module usage caused an endpoint to
  appear in the global impact set.
- `rootSymbols[]` lists changed roots in that source that can reach the endpoint.
- `chains[]` contains at most one shortest chain per root symbol to keep JSON
  size bounded. Each chain is a list of human-readable node labels from changed
  root to endpoint.
- `confidence` is the weakest confidence along the selected chains. If a source
  has no chain but still lists the endpoint, confidence is `low`.
- Non-symbol file fallback roots are allowed; they use the existing root node
  metadata and may produce an empty chain if no endpoint path exists.

## Ordering

All ordering is deterministic:

- endpoint items sort by `method`, then `path`;
- sources sort by `sourceType`, then `sourceFile`, then `modulePath`;
- root symbols sort by `id`;
- chains sort lexicographically by their joined label sequence.

This preserves byte-stable output for golden tests and MR bot diffs.

## Schema And Docs

The impact JSON schema must expose `endpointSourcesSummary` and its nested
definitions. The field is required because it is part of the impact output
contract; it is an empty array when no endpoint is impacted.

The output contract and architecture docs should describe the field as a summary
projection that is safe for default platform consumption. They should keep all
paths project-relative and avoid workspace-specific absolute paths.

## Testing

Implementation must add focused output tests that verify:

- endpoint summaries aggregate file sources from existing recursive trees;
- module sources are included with module metadata;
- the field remains non-nil and sorted deterministically;
- `RenderImpactTreeJSON` places `endpointSourcesSummary` after `moduleSources`
  when modules exist and after `fileSources` when they do not;
- impact schema includes the new top-level field and nested definitions.

Real-project validation must rerun impact for:

- `sl-sc1-admin-bff` with proto module filtering enabled;
- `sl-sc1-bff-service` with proto module filtering enabled;
- `sl-sc1-bff-service` without proto filtering to validate module source
  summaries.
