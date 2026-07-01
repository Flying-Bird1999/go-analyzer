# IM Event Impact Analysis

## Context

`go-analyzer` currently treats HTTP endpoints as the only externally visible impact
exit. Lego BFF repositories also publish IM events. A BFF-local change to a struct
field, type, conversion function, condition, or sender can change one or more IM
messages even when no HTTP endpoint is involved.

The required analysis direction is entirely inside the BFF:

```text
BFF unified diff
  -> changed local symbol/type
  -> local data and control dependencies
  -> IM send
  -> concrete IM event
```

Changes in `sc1-server` or another upstream repository are outside this capability.

Real repositories do not share one business wrapper API:

- SC1 Admin uses `SendSLMessage` and `SendImBroadcastMessage`.
- SC1 Client calls the common `notify/im` SDK.
- SC2 Admin uses `SendBroadcastMessage`.

The stable behavior is below those wrappers. All verified implementations encode
the topic as `broadcast://<event>` and send the payload to `/broadcast/send`.
Function names and wrapper signatures are compatibility samples, not the protocol.

## Goals

- Report the concrete IM events affected by a BFF diff.
- Preserve the complete propagation tree from the changed source to each IM event.
- Distinguish different payloads sent from the same function, avoiding event-wide
  over-reporting.
- Discover project-local IM wrappers from stable protocol behavior.
- Support the verified common IM SDK through analyzer-owned adapters.
- Keep normal BFF adoption zero-config.
- Preserve deterministic JSON output.

## Non-goals

- Analyze changes in `sc1-server`, an IM service repository, or another upstream
  repository.
- Produce runtime merchant IDs, user IDs, app IDs, group IDs, payload values, or
  delivery modes.
- Describe changed payload fields in the public JSON.
- Treat IM token generation, authentication, or subscription as an IM event.
- Implement whole-program taint analysis, `go/types`, or SSA in the first version.
- Guess a concrete event generated only through runtime configuration, reflection,
  or otherwise unresolved dynamic behavior.
- Add public IM configuration before a real unsupported project requires it.

## Correctness Policy

The analysis is precision-first. A concrete event may be reported only when static
evidence connects the changed source to that event through one of these relations:

1. payload data flow;
2. event-value data flow;
3. a control predicate governing the send;
4. a diff range that directly changes the send call or one of its enclosing control
   expressions.

The analyzer must not report every IM event merely because an impacted function
contains multiple sends.

For example, changing a field of `GetMessageItem` in `sl-sc1-bff-service` must
report `inbox_msg` and `inbox_customer_msg`, whose payload is `event.MsgInfo`. It
must not report `inbox_conv`, whose payload is `event.ConvInfo`.

If the analyzer proves that an IM send is affected but cannot resolve its event
value, it keeps an unresolved terminal node in the raw tree. It does not invent an
event or include that node in the concrete event summary.

## Architecture

The capability is implemented as an independent extraction and graph concern, then
integrated into the existing impact tree:

```text
project AST/index
  -> internal/extract/im
     -> transport sinks
     -> wrapper summaries
     -> concrete/unresolved IM event facts
  -> internal/graph/im
     -> dependency -> IM event index
  -> internal/impact
     -> endpoint exits + IM event exits
  -> internal/output
     -> one combined reviewable report
```

The existing module boundaries remain:

- `extract/im` determines what IM sends and wrapper flows exist in source.
- `graph/im` indexes which symbols and types can affect which events.
- `impact` attaches event exits while walking a changed-source tree.
- `output` projects already-computed results and performs no IM inference.
- `app` only controls construction order.

No `imSources` collection is added. IM is an impact exit, not a new diff source.
Ordinary code changes remain under `fileSources`; module changes remain under
`moduleSources`.

## Fact Model

Add an IM event fact with enough internal evidence to build and review the tree:

```text
ID
Event
EventRaw
SenderSymbol
Dependencies
Relation
Confidence
Span
Resolved
```

`Dependencies` contains exact project symbol or owning-type IDs that influence the
event payload, event value, or governing control expression. Internal wrapper
summaries also retain parameter and return-flow bindings, but they are not exposed
in the public facts or impact contracts unless debugging proves that necessary.

An unresolved fact has an empty concrete event, retains `EventRaw`, and sets
`Resolved` to false.

## Sink Discovery

### Project-local transports

Local code is identified semantically rather than by wrapper name. Discovery starts
from the conjunction of:

- an event URL built from the `broadcast://` scheme;
- a request sent to `/broadcast/send`;
- a body value carried into the IM request data.

The extractor summarizes the local functions between event construction and the
transport call. It then propagates the event and payload parameter positions back
through project-local callers. This allows wrappers such as `SendSLMessage`,
`SendImBroadcastMessage`, and `SendBroadcastMessage` to be discovered without
making those names the primary rule.

A partial match is insufficient. Seeing a function named `Send*`, a package named
`im`, or the word `broadcast` alone must not create an IM sink.

### External SDKs

Source for an imported SDK is not part of the current project loader. Verified IM
SDK symbols therefore use analyzer-owned adapters that define their event and
payload argument positions. The first adapter covers the common
`gopkg.inshopline.com/sc1/commons/utils/bus/notify/im` send functions.

Adapters are centralized in `extract/im`; business package paths and controller
names are not scattered through the analyzer.

### Adapter extension

The package exposes an internal adapter interface so another stable SDK can be
added with focused tests. No public configuration is introduced in the first
version.

If a future real BFF uses an opaque external SDK that exposes neither analyzable
source nor a known adapter, configuration may later describe only:

```json
{
  "im": {
    "sinks": [
      {
        "symbol": "example.com/im.Send",
        "eventArg": 3,
        "payloadArg": 4
      }
    ]
  }
}
```

This is a reserved direction, not part of the first implementation or output
contract.

## Wrapper Summaries

Each project-local function receives a compact summary describing how an IM send
depends on its inputs. Supported sources include:

- string literals and typed string constants;
- constant aliases and string concatenation;
- `iota`-backed enums whose `String` method indexes a static string table;
- function parameters;
- struct fields and selector expressions;
- local variables with a single statically resolved assignment;
- project-local function return values;
- immediately assigned function literals and closure return values;
- composite literals such as `BroadcastParams{Event: value}`.

Summaries are propagated across call sites until a deterministic fixed point is
reached. The summary key includes function symbol, sink identity, event source, and
payload source. Sets are deduplicated and sorted before comparison so iteration
order cannot affect the result.

Cycles stop at an already-visited summary state. They must not create duplicate
events or unbounded traversal.

## Event Evaluation

The event evaluator resolves only statically provable values:

- quoted string literals;
- package and local constants;
- aliases of resolved constants;
- string concatenation of resolved operands;
- conversions between named string types and `string`;
- known enum `String()` implementations backed by a static array or slice;
- wrapper arguments bound to one resolved call-site value.

When more than one reachable value exists, each exact value may become a separate
event only if every candidate is statically enumerated. An unknown candidate makes
that path unresolved; it must not be collapsed into a guessed event.

Legacy channel/event composition such as `POST` plus `LOCK_INVENTORY_UPDATE` is
evaluated through its actual string-building expression, producing
`POST/LOCK_INVENTORY_UPDATE`.

## Payload and Control Flow

The public report does not expose payload details, but internal payload flow is
required to avoid false positives.

The lightweight flow model tracks:

- the static type of a direct payload expression;
- owning types for field selections;
- parameter-to-argument bindings;
- call-return-to-assignment bindings;
- composite literals and address/dereference wrappers;
- closure captures used as returned payloads;
- conditions of enclosing `if`, `switch`, and type-switch statements.

A struct field diff continues to map to its owning type under the existing diff
model. That owning type can then match the corresponding IM payload dependency.

When the diff directly changes a function containing multiple sends, changed ranges
are compared with each send call, its event and payload expressions, and its
enclosing control expressions. A change elsewhere in the function does not by
itself select every event in that function.

Unsupported flow, including mutation through unknown aliases, global state observed
later, reflection, and arbitrary flow-sensitive reassignment, does not create a
concrete event edge.

## Impact Graph Integration

`graph/im` provides deterministic lookup by dependency symbol or owning type. While
`impact` expands a symbol node through the existing reverse reference and route
graphs, it also asks the IM graph for event exits justified by that node and the
root change ranges.

Resolved exits use:

```text
kind       = im_event
name       = concrete event
relation   = im_payload | im_event_value | im_control
confidence = high for exact evidence, medium for supported bounded inference
```

Unresolved exits use `kind = im_event_unresolved`, retain the raw expression, and
are excluded from event summaries.

The same changed source may produce HTTP endpoint children, IM event children, or
both. Existing cycle handling and tree merging apply to local symbol propagation.

## Output Contract

The top-level summary always contains both exit classes:

```json
{
  "summary": {
    "impactedEndpointCount": 1,
    "impactedEndpoints": [],
    "impactedIMCount": 2,
    "impactedIMEvents": [
      "inbox_customer_msg",
      "inbox_msg"
    ]
  }
}
```

Every `fileSources[]` and `moduleSources[].sourceFiles[]` entry adds:

```json
{
  "impactedIMEvents": ["inbox_msg"]
}
```

A resolved terminal node is represented as:

```json
{
  "id": "im_event:inbox_msg",
  "kind": "im_event",
  "name": "inbox_msg",
  "relation": "im_payload",
  "confidence": "high",
  "level": 4,
  "children": []
}
```

Output rules:

- concrete events are deduplicated and sorted lexicographically;
- `impactedIMCount` equals the length of `impactedIMEvents`;
- no IM impact produces a zero count and an empty array, not omitted fields;
- source-local summaries contain only events reachable from that source;
- app ID, delivery mode, payload type, changed fields, and payload expressions are
  not added;
- existing diff text and complete trees remain unchanged.

The Go output structs, JSON Schema, output contract documentation, golden tests,
README, and `ARCHITECTURE.md` must change together.

## Diagnostics and Failure Behavior

Impact JSON remains free of top-level diagnostics.

Facts diagnostics may record:

- a protocol-shaped transport whose event flow cannot be resolved;
- an SDK send whose adapter arguments do not match the call;
- a wrapper summary stopped by unsupported dynamic flow.

When an affected send is known but its event is dynamic, the raw impact tree keeps
an `im_event_unresolved` node. This is impact evidence rather than a top-level
diagnostic and prevents silent loss without polluting the concrete event list.

Malformed changed Go files and a diff that does not match the post-change source
retain the existing fatal behavior.

## Verification

### Unit tests

Add focused coverage for:

- local protocol sink discovery using `broadcast://` and `/broadcast/send`;
- rejection of name-only or partial protocol matches;
- common SDK adapter functions;
- literal, alias, concatenated, and named-string events;
- `iota` plus static `String()` table event resolution;
- generic wrappers;
- multi-level parameter propagation;
- function-return and closure-return payload flow;
- composite-literal event fields;
- payload-field discrimination for multiple sends in one function;
- direct diff-range changes to one of several sends;
- enclosing `if` and `switch` control dependencies;
- unresolved dynamic events;
- wrapper cycles;
- deterministic facts, trees, and JSON across repeated runs.

Negative tests must assert that unrelated payload types and unrelated lines in a
multi-send function do not produce concrete events.

### End-to-end fixtures

Create a minimal fixture that combines:

- one changed type used by two IM events;
- a sibling type used by a third event in the same function;
- an HTTP endpoint on one branch;
- one dynamic event.

The expected report must contain the exact endpoint/event union, complete trees,
and one unresolved raw node without counting the dynamic event.

### Real BFF verification

Generate real diffs against modified post-change working trees:

1. `sl-sc1-bff-service`
   - change `GetMessageItem`;
   - expect only `inbox_msg` and `inbox_customer_msg`;
   - explicitly reject `inbox_conv`.
2. `sl-sc1-bff-service`
   - change `GetConversationItem`;
   - expect only `inbox_conv`.
3. `sl-sc1-admin-bff`
   - change `LockInventoryUpdateMsg`;
   - expect `POST/LOCK_INVENTORY_UPDATE`.
4. `sl-sc1-admin-bff`
   - change an activity IM conversion DTO;
   - expect its exact `ACTIVITY/...` event.
5. `sl-sc2-admin-bff`
   - change an IM message DTO;
   - expect its exact `mc/...` event.

For each case:

- generate the diff from the modified source;
- analyze that same post-change source;
- inspect the full propagation chain;
- reject extra events and endpoints;
- run twice and compare output byte-for-byte;
- restore the business repository and verify a clean worktree.

Finally run `go test ./... -count=1`, `go vet ./...`, `git diff --check`, formatting
checks, and the complete real-project smoke suite.

## Rollout

Implement in independently verifiable increments:

1. Add IM facts, semantic local transport discovery, and common SDK adapters.
2. Add wrapper summaries, event evaluation, and payload/control dependency flow.
3. Integrate IM exits into impact trees and the JSON contract.
4. Add fixtures and real BFF regression cases.
5. Update architecture, README, and output contract documentation.

Do not expose IM configuration during this rollout. Revisit configuration only
with a concrete opaque SDK or project pattern that semantic discovery and the
central adapter mechanism cannot represent.
