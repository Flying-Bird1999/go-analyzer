# gRPC Service Impact

> This document records the gRPC-specific evidence model. The current command also emits HTTP, Dubbo and XXL-Job contracts; see [service external impact design](../service-external-impact/design.md).

## Scope

`grpc-impact` analyzes one already-updated Go service repository and one applied unified diff. It does not query BFF repositories or orchestrate cross-repository analysis.

```bash
go-analyzer grpc-impact \
  --project /absolute/path/to/go-service \
  --diff /absolute/path/to/change.diff \
  --format json
```

The command name is retained for compatibility. Its formal result is now the set of affected registered service contracts: `grpc_operation`, `http_endpoint`, `dubbo_method`, and `job`.

## gRPC Evidence

A gRPC operation requires all of the following:

1. Generated `ServiceDesc` metadata supplies the canonical service and method identity.
2. Project source calls the matching `RegisterXxxServer` function.
3. The registration argument resolves to one concrete project type.
4. The concrete handler method matches the generated server method.

Direct values, project constructors, registration-struct fields, and resolvable generic container wrappers are supported. Ambiguous implementations are rejected rather than guessed. Generated packages are read from repository source first, including nested modules, and otherwise loaded selectively from the active module graph.

## Output

`summary` is the single protocol-grouped result map:

```json
{
  "grpc": [],
  "dubbo": [],
  "http": [],
  "job": []
}
```

The same four keys appear in `fileSources[].impacts` and the reverse `entrySourcesSummary`. No gRPC-only mirror fields are emitted. `fileSources[].symbols` retains the recursive evidence chain; the gRPC terminal relation remains `exposed_grpc_operation`.

## Boundaries

- Pulsar and IM producer/consumer analysis is a follow-up item.
- Outbound gRPC, HTTP and Dubbo calls are not provider terminals.
- Proto compatibility classification is not implemented.
- Cross-repository `gRPC -> BFF -> frontend` orchestration remains external.

Real-project validation and protocol-specific evidence rules are maintained in the service entry design document to avoid duplicating status in two places.
