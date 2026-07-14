# gRPC Service Impact

## Scope

`grpc-impact` analyzes one gRPC provider repository and answers:

```text
Which registered canonical gRPC operations are affected by this applied diff?
```

It does not query BFF repositories or orchestrate cross-repository analysis.

## Command

```bash
go-analyzer grpc-impact \
  --project /absolute/path/to/grpc-project \
  --diff /absolute/path/to/change.diff \
  --format json
```

The diff must already be applied to the project snapshot. Build-context and optional impact-config flags follow the BFF `impact` command.

## Evidence Model

An exposed operation requires these facts:

1. A generated `ServiceDesc` provides `ServiceName`, method names and streaming mode.
2. Project source calls the matching generated `RegisterXxxServer` function.
3. The registration argument resolves to one concrete project type when a handler binding is claimed.
4. The concrete method symbol matches the generated server method.

Supported implementation expressions include direct struct values, project constructors, registration-struct fields, and generic container wrappers. Constructor functions may be passed as values to a container and may declare an interface return when every explicit return proves the same project-local concrete type. Ambiguous implementations are rejected instead of guessed.

The protobuf descriptor method is the canonical RPC method and retains its original case. Older generators can emit lower-camel RPC names while the Go server interface uses exported upper-camel methods; the analyzer maps these only when one interface method matches case-insensitively.

## Pipeline

```text
project + applied diff
  -> shared project loader / AST index / symbols / references
  -> generated server catalog
  -> RegisterXxxServer provider bindings
  -> ChangeFact mapping
  -> shared ReverseGraph
  -> registered gRPC operation terminals
  -> stable JSON
```

Generated packages are read from repository source first, using the nearest `go.mod` for nested-module import identity. Missing packages are loaded selectively from the active module graph; unrelated dependencies are not traversed.

## Output

The contract follows BFF impact terminology and nesting:

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
  "fileSources": [],
  "grpcOperationSourcesSummary": []
}
```

`fileSources[].symbols` uses the same recursive `ImpactNode` contract as BFF impact. A gRPC terminal has `kind=grpc_operation`, `relation=exposed_grpc_operation`, and `fullMethod`.

## Current Boundary

- The public terminal is a registered canonical gRPC operation.
- Outbound gRPC, Pulsar, HTTP, Dubbo and database assets are not additional provider terminals in this phase.
- Proto compatibility classification is not implemented yet.
- Cross-repository `gRPC -> BFF -> frontend` orchestration remains external.

## Real-Project Validation

Validation uses already-applied, comment-only diffs and restores both repositories after analysis.

| Project | Diff scope | Result |
| --- | --- | --- |
| `sc1-server` | 5 files across mc, message and user; direct providers plus downstream application/shared helpers | 11 deduplicated operations. `ChatBotProxyService/Proxy` and `MessageTemplateAdminService/CreateTemplate` each retain two independent source files. Internal reuse of `MerchantTokenClientService.GetToken` is preserved as auditable chains to seven additional registered operations. |
| `sc2-server` | 7 files across channel, trade, medium and message; direct providers plus downstream channel/trade services | 5 deduplicated operations. `StoreChannelConfigService/Bind` and `TradeService/EstimateCost` each retain two independent source files; all five operations match their expected service and method exactly. |

The sc2 case covers direct constructors, constructors passed as function values to `MustProvideAtOnce[...]`, interface-returning constructors, nested proto modules, and lower-camel descriptor methods. No sibling methods were reported for direct provider changes.
