# Nexus Reference And Module Sources Design

## Goal

Fix missed impact propagation through generated Nexus controller methods and make
dependency-driven impact output distinct from ordinary file-driven impact.

## Reference Resolution

The reference extractor must resolve project-local method calls made through:

- the current method receiver, such as `c.convert(...)`;
- local variables with an explicit type;
- local variables initialized by a project-local constructor, such as
  `c := newControllerHandler(...)`.

Resolution remains AST-based. It must not require a buildable dependency graph or
special-case Nexus names. Receiver and local variable types are scoped to the
enclosing function and passed to call resolution. Existing package-level selector
resolution remains unchanged.

For the real Nexus flow, the graph must include:

```text
calldataTransfer -> executeFlow -> controller -> annotated handlers -> routes
```

## Output Projection

Internal facts remain normalized:

- `ModuleChangeFact` describes a changed Go module;
- `ModuleUsageFact` describes project-local usage entry points;
- synthetic `ChangeFact` values drive the existing impact engine.

The public impact document no longer exposes `module_changes` and
`module_usages`. It projects them into `moduleSources`, following the readable
source split used by `ts-analyzer`.

```json
{
  "summary": {
    "impactedEndpointCount": 1,
    "impactedEndpoints": []
  },
  "fileSources": [],
  "moduleSources": [
    {
      "modulePath": "github.com/shopspring/decimal",
      "changeType": "upgraded",
      "versionBefore": "v1.3.1",
      "versionAfter": "v1.4.0",
      "impactBasis": {
        "kind": "module_reference",
        "reason": "matched_import_usage"
      },
      "sourceFiles": []
    }
  ]
}
```

`fileSources` contains roots created by ordinary diff mapping. `moduleSources`
contains roots created from module usages. A `go.mod` diff is not emitted as a
`fileSources` non-symbol root when its semantic module changes were resolved.
The top-level summary is the deduplicated union of endpoints from both source
types.

Each module source groups all usage roots for one changed module. Its
`sourceFiles` reuse the same symbol-tree and endpoint projection shape as
`fileSources`, without embedding the `go.mod` patch in each usage file.

## Diagnostics

Facts output keeps all project diagnostics. Impact output keeps only diagnostics
that can affect the current result:

- diagnostics produced while mapping the current diff or module changes;
- propagation diagnostics produced for current roots;
- extraction diagnostics whose related fact IDs or source locations intersect
  current impact trees.

Empty impact diagnostics are omitted. No user configuration is added.

## Verification

Automated coverage must include:

- receiver method calls;
- constructor-inferred local method calls;
- module source grouping and endpoint union;
- exclusion of resolved `go.mod` roots from `fileSources`;
- removal of public `module_changes` and `module_usages`;
- unrelated project diagnostics excluded from impact output.

The real `sl-sc1-bff-service` decimal upgrade fixture must reach the Nexus
endpoints that consume `model.ConvertPrice`, while the independent `CheckIn`
logic edit remains under `fileSources`.
