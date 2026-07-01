# IM Event Impact Analysis Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend BFF diff analysis so the reviewable JSON reports the exact outbound IM events affected by local code changes, without requiring business-project configuration.

**Architecture:** Add IM extraction as an independent fact-building stage. Discover project-local transports from the `broadcast://` plus `/broadcast/send` protocol, summarize wrappers and payload/event flow, and use centralized adapters for verified external SDK calls. Project concrete event facts into the existing impact tree alongside HTTP endpoints; do not create a new source family.

**Tech Stack:** Go 1.24, `go/ast`, `go/parser`, existing `project`/`astindex`/`facts`/`graph`/`impact` packages, table-driven Go tests, unified-diff fixtures, Bash real-project smoke tests.

---

## Scope And File Map

Execute this plan in a dedicated worktree created from the current `main` after
this plan document is committed:

```bash
git worktree add \
  /Users/zxc/Desktop/go-analyzer-factory/go-analyzer-im-event-impact \
  -b feat/im-event-impact \
  main
```

This sibling location is intentional. `scripts/smoke-real-projects.sh` resolves the
real BFF repositories relative to the analyzer root; a nested `.worktrees`
directory would break that lookup.

New files:

- `internal/facts/im.go`: public IM event fact and dependency evidence types.
- `internal/extract/im/extractor.go`: extraction entry point and orchestration.
- `internal/extract/im/expr.go`: static string/event and expression type evaluation.
- `internal/extract/im/protocol.go`: local `broadcast://` and `/broadcast/send` transport discovery.
- `internal/extract/im/adapter.go`: centralized external SDK sink adapters.
- `internal/extract/im/summary.go`: wrapper summaries and deterministic fixed-point propagation.
- `internal/extract/im/flow.go`: payload, argument, return, closure, and control dependency mapping.
- `internal/extract/im/*_test.go`: focused extraction tests.
- `internal/graph/im.go`: dependency/path to IM event lookup.
- `internal/graph/im_test.go`: exact and negative graph matching tests.
- `testdata/fixtures/im-impact/*`: minimal BFF with multiple event payloads and one endpoint.
- `testdata/diffs/im-impact.diff`: field change applied to that fixture.
- `testdata/golden/im-impact.impact.json`: combined endpoint and IM review tree.

Modified files:

- `internal/facts/store.go`: retain extracted IM event facts.
- `internal/output/schema.go`, `internal/output/json.go`, `internal/output/contract.go`: expose facts and impact contract additions.
- `internal/impact/tree.go`, `internal/impact/tree_builder.go`: add IM exits while preserving source trees.
- `internal/output/impact_tree.go`: source-local and global IM summaries.
- `internal/app/pipeline.go`: invoke IM extraction after references are available.
- Existing tests under `internal/app`, `internal/impact`, and `internal/output`.
- `scripts/smoke-real-projects.sh`: exact-event real BFF cases and deterministic reruns.
- `README.md`, `ARCHITECTURE.md`, `docs/contracts/output-contract.md`: capability and boundary documentation.

Do not add route/annotation/IM business configuration, `go/types`, SSA, or upstream
repository analysis.

### Task 1: Add The IM Fact And Facts JSON Contract

**Files:**
- Create: `internal/facts/im.go`
- Modify: `internal/facts/store.go`
- Modify: `internal/output/schema.go`
- Modify: `internal/output/json.go`
- Modify: `internal/output/contract.go`
- Test: `internal/output/json_test.go`
- Test: `internal/output/contract_test.go`

- [ ] **Step 1: Write failing facts rendering tests**

Add a test that builds a `facts.Store` containing one resolved and one unresolved
IM event and asserts deterministic `im_events` output:

```go
func TestRenderJSONIncludesSortedIMEvents(t *testing.T) {
	store := facts.NewStore("/repo", "example.com/bff")
	store.IMEvents = []facts.IMEventFact{
		{ID: "im_event:z", Event: "z", Resolved: true},
		{ID: "im_event:a", Event: "a", Resolved: true},
	}
	got, err := RenderJSON(store)
	if err != nil {
		t.Fatal(err)
	}
	var doc Document
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.IMEvents) != 2 || doc.IMEvents[0].Event != "a" {
		t.Fatalf("im events = %#v", doc.IMEvents)
	}
}
```

Extend the facts schema test to require `im_events`.

- [ ] **Step 2: Run the tests and verify failure**

Run:

```bash
go test ./internal/output -run 'TestRenderJSONIncludesSortedIMEvents|TestFactsSchema' -count=1
```

Expected: compile failure because `IMEventFact`, `Store.IMEvents`, and
`Document.IMEvents` do not exist.

- [ ] **Step 3: Add the fact types**

Implement the minimal fact model:

```go
type IMEventRelation string

const (
	IMRelationPayload    IMEventRelation = "im_payload"
	IMRelationEventValue IMEventRelation = "im_event_value"
	IMRelationControl    IMEventRelation = "im_control"
)

type IMEventDependency struct {
	SymbolID  SymbolID       `json:"symbol_id"`
	Relation  IMEventRelation `json:"relation"`
	Confidence Confidence     `json:"confidence"`
	Span      SourceSpan      `json:"span,omitempty"`
}

type IMEventEvidence struct {
	Relation IMEventRelation `json:"relation"`
	Span     SourceSpan      `json:"span"`
}

type IMEventFact struct {
	ID           string              `json:"id"`
	Event        string              `json:"event,omitempty"`
	EventRaw     string              `json:"event_raw,omitempty"`
	SenderSymbol SymbolID            `json:"sender_symbol"`
	Dependencies []IMEventDependency `json:"dependencies"`
	Evidence     []IMEventEvidence   `json:"evidence"`
	Confidence   Confidence          `json:"confidence"`
	Span         SourceSpan          `json:"span"`
	Resolved     bool                `json:"resolved"`
}
```

`Evidence` stores the exact send, event, payload, and enclosing-control ranges used
when a diff changes the sender function directly. Initialize `Store.IMEvents` to a
non-nil slice, project it into `output.Document`, sort by `ID`, and add the
corresponding JSON Schema definitions.

- [ ] **Step 4: Run focused output tests**

Run:

```bash
go test ./internal/output -count=1
```

Expected: PASS.

- [ ] **Step 5: Refresh the facts golden**

Run:

```bash
UPDATE_GOLDEN=1 go test ./internal/output -run TestMiniBFFGolden -count=1
go test ./internal/output -run TestMiniBFFGolden -count=1
```

Expected: the mini BFF golden contains `"im_events": []` and passes normally.

- [ ] **Step 6: Commit the fact contract**

```bash
git add internal/facts internal/output testdata/golden/mini-bff.facts.json
git commit -m "feat: add IM event facts"
```

### Task 2: Build Static Event And Type Evaluation

**Files:**
- Create: `internal/extract/im/expr.go`
- Create: `internal/extract/im/expr_test.go`
- Reuse: `internal/astindex/index.go`
- Reuse: `internal/project/project.go`

- [ ] **Step 1: Write table-driven failing tests**

Create temporary projects with these event forms:

```go
type Event string

const (
	Inbox Event = "inbox_msg"
	Prefix       = "POST"
	Product      = Prefix + "/PRODUCT_CHANGE"
)
```

Also cover:

```go
type EventCode int

const (
	LockInventory EventCode = iota
	Conversation
)

var eventNames = [...]string{
	"LOCK_INVENTORY_UPDATE",
	"CONVERSATION_UPDATE",
}

func (e EventCode) String() string { return eventNames[e] }
```

Assertions:

- literal, typed constant, alias, concatenation, and conversion resolve exactly;
- `EventCode.String()` resolves its static table entry;
- a function call or map lookup with unknown runtime input remains unresolved;
- selector payload expressions resolve to their declared field type.

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
go test ./internal/extract/im -run 'TestResolveEvent|TestResolveExpressionTypes' -count=1
```

Expected: package or function-not-defined failure.

- [ ] **Step 3: Implement a bounded evaluator**

Implement an evaluator scoped to one loaded project:

```go
type evaluator struct {
	project      *project.Project
	index        *astindex.Index
	constExprs   map[facts.SymbolID]ast.Expr
	stringTables map[facts.SymbolID][]string
	fields       map[fieldKey]fieldInfo
}

func (e *evaluator) eventValue(file *project.File, expr ast.Expr) (string, bool)
func (e *evaluator) expressionTypes(file *project.File, fn *ast.FuncDecl, expr ast.Expr) []facts.SymbolID
```

Support only the approved forms. Keep cycle guards for constant aliases and
`String()` calls. Sort and deduplicate resolved types.

- [ ] **Step 4: Run focused evaluator tests**

Run:

```bash
go test ./internal/extract/im -run 'TestResolveEvent|TestResolveExpressionTypes' -count=1
```

Expected: PASS.

- [ ] **Step 5: Add negative determinism tests**

Evaluate the same project with declarations reordered and assert byte-identical
normalized evaluator results. Assert unknown expressions never return a guessed
string.

- [ ] **Step 6: Commit the evaluator**

```bash
git add internal/extract/im/expr.go internal/extract/im/expr_test.go
git commit -m "feat: evaluate static IM events"
```

### Task 3: Discover Local Protocol Sinks And External SDK Calls

**Files:**
- Create: `internal/extract/im/adapter.go`
- Create: `internal/extract/im/protocol.go`
- Create: `internal/extract/im/extractor.go`
- Create: `internal/extract/im/adapter_test.go`
- Create: `internal/extract/im/protocol_test.go`

- [ ] **Step 1: Write failing local protocol tests**

Use an in-test project containing:

```go
const BroadcastURI = "/broadcast/send"

func (d *SendData) Event(event string) {
	d.URL = "broadcast://" + event
}

func send(topic string, body any) {
	data := &SendData{Body: body}
	data.Event(topic)
	Post(GetDomain()+BroadcastURI, data)
}
```

Assert the transport summary identifies `topic` as event source and `body` as
payload source.

Negative fixtures must not match:

- a function named `SendIM` with no protocol evidence;
- `/broadcast/send` without a `broadcast://` event flow;
- `broadcast://` logging without a send to the endpoint.

- [ ] **Step 2: Write failing common SDK adapter tests**

Cover the verified symbols:

```text
gopkg.inshopline.com/sc1/commons/utils/bus/notify/im.SendIm
gopkg.inshopline.com/sc1/commons/utils/bus/notify/im.SendImAsync
gopkg.inshopline.com/sc1/commons/utils/bus/notify/im.SendImToUid
gopkg.inshopline.com/sc1/commons/utils/bus/notify/im.SendImToUidAsync
```

For each adapter, assert the event expression is argument 3 and payload is argument
4. Assert a same-named function from another import path does not match.

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
go test ./internal/extract/im -run 'TestDiscoverLocalProtocol|TestCommonSDKAdapter' -count=1
```

Expected: FAIL because protocol discovery and adapters are absent.

- [ ] **Step 4: Implement centralized adapters**

Use exact imported package path plus function symbol:

```go
type sinkAdapter struct {
	PackagePath string
	Functions   map[string]sinkArguments
}

type sinkArguments struct {
	EventArg   int
	PayloadArg int
}
```

Do not match package aliases, local directory names, or function names without
resolving the import path.

- [ ] **Step 5: Implement semantic local transport discovery**

Index:

- resolved string constants equal to `/broadcast/send`;
- expressions that construct `broadcast://` plus an event source;
- body assignments that flow into the same request object;
- project-local calls connecting those expressions.

Require both protocol anchors before emitting a transport summary.

- [ ] **Step 6: Run extraction tests**

Run:

```bash
go test ./internal/extract/im -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit sink discovery**

```bash
git add internal/extract/im
git commit -m "feat: discover IM transport sinks"
```

### Task 4: Summarize Wrappers And Build Precise Event Dependencies

**Files:**
- Create: `internal/extract/im/summary.go`
- Create: `internal/extract/im/flow.go`
- Create: `internal/extract/im/summary_test.go`
- Create: `internal/extract/im/flow_test.go`
- Modify: `internal/extract/im/extractor.go`

- [ ] **Step 1: Write a failing SC1 Client-shaped test**

Use one function with three sends:

```go
func (InboxImConsumer) Receive(event InboxImEvent) {
	im.SendIm(ctx, appID, event.MerchantID, inboxConversation, event.ConvInfo)
	im.SendIm(ctx, appID, event.MerchantID, inboxMessage, event.MsgInfo)
	im.SendImToUidAsync(ctx, appID, []string{event.UserID}, inboxCustomerMessage, event.MsgInfo, nil)
}
```

Assert:

- `GetMessageItem` is a payload dependency of `inbox_msg` and
  `inbox_customer_msg`;
- it is not a dependency of `inbox_conv`;
- `GetConversationItem` selects only `inbox_conv`.

- [ ] **Step 2: Write failing wrapper tests**

Cover:

- SC1 `BroadcastParams{Event: ProductChange}` passed through
  `SendSLMessage -> SendMessage`;
- SC1 legacy `eventEnum + channelEnum` passed through a closure payload;
- SC2 `SendBroadcastMessage(ctx, group, app, topic, msg)`;
- a three-level wrapper where event and payload are forwarded through different
  parameter positions;
- a wrapper cycle;
- one dynamic event.

- [ ] **Step 3: Write failing control and changed-range tests**

Create two sends in one function under different `if` conditions. Assert a
dependency used by the first condition selects only the first event. Assert a diff
range intersecting only the second call selects only the second event.

- [ ] **Step 4: Run tests and verify failure**

Run:

```bash
go test ./internal/extract/im -run 'TestPayloadDependencies|TestWrapper|TestControlDependencies' -count=1
```

Expected: FAIL because wrapper and flow summaries are absent.

- [ ] **Step 5: Implement deterministic wrapper summaries**

Use summaries keyed by callable symbol and sink:

```go
type valueSource struct {
	Kind       sourceKind
	ParamIndex int
	SymbolIDs  []facts.SymbolID
	Raw        string
}

type wrapperSummary struct {
	Function facts.SymbolID
	Event    valueSource
	Payload  valueSource
	Control  []facts.SymbolID
	Wrappers []facts.SymbolID
	Span     facts.SourceSpan
}
```

Propagate call-site bindings to a fixed point. Normalize every symbol set and
summary key before comparing iterations. Stop cycles on an already-seen normalized
state. Retain every project-local wrapper symbol traversed to the transport; a
change to a shared wrapper must select each concrete event that uses it.

- [ ] **Step 6: Implement payload and control flow**

Support:

- selector field type to the selected field's target type;
- arguments to parameters;
- local single assignments;
- call returns to assignments and direct arguments;
- composite literals;
- immediately assigned function literals and captured returns;
- enclosing `if`, `switch`, and type-switch predicates.

Do not fall back from a resolved field payload to its containing envelope type,
because that would make `MsgInfo` and `ConvInfo` indistinguishable.

Store concrete event facts at the business call site, not only at the final
transport function. This keeps the impact terminal attached to the reviewable
local sender node. Populate `IMEventFact.Evidence` with separate ranges for the
send call, event expression, payload expression, and enclosing control expressions.
Add traversed local wrapper symbols as `im_control` dependencies because changes to
those functions can alter whether or how the send occurs.

- [ ] **Step 7: Emit unresolved facts without guessing**

When the sink and dependency are known but event evaluation fails, emit:

```go
facts.IMEventFact{
	EventRaw:     renderedExpr,
	Resolved:     false,
	SenderSymbol: sender,
}
```

Do not place a fabricated event in `Event`.

- [ ] **Step 8: Run all extraction tests**

Run:

```bash
go test ./internal/extract/im -count=1
```

Expected: PASS, including negative multi-event assertions.

- [ ] **Step 9: Commit wrapper flow**

```bash
git add internal/extract/im
git commit -m "feat: trace IM event payload flow"
```

### Task 5: Integrate IM Events With The Impact Graph

**Files:**
- Create: `internal/graph/im.go`
- Create: `internal/graph/im_test.go`
- Modify: `internal/impact/tree.go`
- Modify: `internal/impact/tree_builder.go`
- Test: `internal/impact/analyzer_test.go`

- [ ] **Step 1: Write failing IM graph tests**

Build facts for three events sharing one sender and assert dependency-specific
matching:

```go
events := graph.NewIMGraph(store)
got := events.EventsForPath(
	"method:example.com/bff::InboxImConsumer.Receive",
	map[facts.SymbolID]bool{
		"type:example.com/bff::GetMessageItem": true,
	},
	change,
)
```

Expected events: exactly `inbox_msg` and `inbox_customer_msg`.

Add a negative test where the path contains only `GetConversationItem`.

- [ ] **Step 2: Run graph tests and verify failure**

Run:

```bash
go test ./internal/graph -run TestIMGraph -count=1
```

Expected: compile failure because `IMGraph` is missing.

- [ ] **Step 3: Implement root-aware matching**

Index facts by `SenderSymbol`. Match an event when:

- one of its exact dependency symbols occurs in the current impact path; or
- the change root is the sender and a changed range intersects one of the
  send/event/payload/control entries in `IMEventFact.Evidence`.

Do not match solely because `SenderSymbol` occurs in the path.

- [ ] **Step 4: Write failing impact tree tests**

Extend `internal/impact/analyzer_test.go` with:

- one root producing an endpoint and two IM event children;
- one unresolved event terminal;
- no unrelated third event;
- deterministic child order;
- concrete event collection excluding unresolved events.

- [ ] **Step 5: Extend tree results**

Add:

```go
type IMEventImpact struct {
	Event string `json:"event"`
}

type RootImpact struct {
	Change    facts.ChangeFact
	Root      Node
	Endpoints []EndpointImpact
	IMEvents  []IMEventImpact
}
```

Create `im_event` or `im_event_unresolved` terminal nodes in `treeBuilder`. Use
`im_payload`, `im_event_value`, or `im_control` as relation. Keep terminal
`Children` non-nil and empty.

- [ ] **Step 6: Run graph and impact tests**

Run:

```bash
go test ./internal/graph ./internal/impact -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit graph integration**

```bash
git add internal/graph internal/impact
git commit -m "feat: propagate impact to IM events"
```

### Task 6: Add IM Summaries To The Reviewable JSON

**Files:**
- Modify: `internal/output/impact_tree.go`
- Modify: `internal/output/contract.go`
- Modify: `internal/output/impact_tree_test.go`
- Modify: `internal/output/contract_test.go`

- [ ] **Step 1: Write failing summary tests**

Extend output test roots with duplicate and unordered IM events. Assert:

```go
if doc.Summary.ImpactedIMCount != 2 {
	t.Fatalf("summary = %#v", doc.Summary)
}
want := []string{"inbox_customer_msg", "inbox_msg"}
if !reflect.DeepEqual(doc.Summary.ImpactedIMEvents, want) {
	t.Fatalf("events = %#v", doc.Summary.ImpactedIMEvents)
}
```

Assert every file source and module source file has its own sorted event list.
Assert zero-impact documents render `impactedIMCount: 0` and
`impactedIMEvents: []`.

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
go test ./internal/output -run 'TestBuildImpactDocument.*IM|TestImpactSchema' -count=1
```

Expected: compile or assertion failure because the new summary fields are absent.

- [ ] **Step 3: Extend output structs**

Add:

```go
type ImpactSummary struct {
	ImpactedEndpointCount int               `json:"impactedEndpointCount"`
	ImpactedEndpoints     []EndpointSummary `json:"impactedEndpoints"`
	ImpactedIMCount       int               `json:"impactedIMCount"`
	ImpactedIMEvents      []string          `json:"impactedIMEvents"`
}

type FileSourceImpact struct {
	SourceFile        string
	Diff              string
	Symbols           map[string]ImpactNode
	ImpactedEndpoints []EndpointSummary
	ImpactedIMEvents  []string `json:"impactedIMEvents"`
}
```

Collect events from `RootImpact.IMEvents` using source-local and global maps.
Sort lexicographically and keep non-nil arrays.

- [ ] **Step 4: Extend the impact schema**

Require:

- `summary.impactedIMCount`;
- `summary.impactedIMEvents`;
- `file_source_impact.impactedIMEvents`.

No app ID, mode, payload, or field properties are added.

- [ ] **Step 5: Run output tests**

Run:

```bash
go test ./internal/output -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the impact contract**

```bash
git add internal/output
git commit -m "feat: report impacted IM events"
```

### Task 7: Wire The Pipeline And Add An End-To-End Fixture

**Files:**
- Modify: `internal/app/pipeline.go`
- Modify: `internal/app/pipeline_test.go`
- Create: `testdata/fixtures/im-impact/go.mod`
- Create: `testdata/fixtures/im-impact/model/model.go`
- Create: `testdata/fixtures/im-impact/consumer/consumer.go`
- Create: `testdata/fixtures/im-impact/controller/controller.go`
- Create: `testdata/fixtures/im-impact/router/router.go`
- Create: `testdata/diffs/im-impact.diff`
- Create: `testdata/golden/im-impact.impact.json`
- Modify: `internal/output/golden_test.go`

- [ ] **Step 1: Create the post-change fixture**

Model the real SC1 Client shape:

```go
type Message struct {
	ID      string
	Changed string
}

type Conversation struct {
	ID string
}

type Envelope struct {
	MsgInfo  *Message
	ConvInfo *Conversation
}
```

The consumer sends:

- `inbox_msg` with `MsgInfo`;
- `inbox_customer_msg` with `MsgInfo`;
- `inbox_conv` with `ConvInfo`;
- one dynamic event with `MsgInfo`.

The same changed `Message` type also reaches one HTTP controller so the result
contains both exit classes.

- [ ] **Step 2: Write the diff fixture**

`testdata/diffs/im-impact.diff` changes only the `Message.Changed` field in the
post-change fixture and passes `diff.ValidateApplied`.

- [ ] **Step 3: Write the failing pipeline test**

Assert exactly:

```go
wantEvents := []string{"inbox_customer_msg", "inbox_msg"}
```

Reject `inbox_conv`, assert the expected endpoint remains, and walk the source tree
to find one `im_event_unresolved` node that is not counted.

- [ ] **Step 4: Run the test and verify failure**

Run:

```bash
go test ./internal/app -run TestRunImpactMapsStructChangeToExactIMEvents -count=1
```

Expected: no IM events because `buildFacts` does not run the extractor.

- [ ] **Step 5: Wire extraction after references**

In `buildFacts`, call:

```go
if err := im.Extract(p, idx, store); err != nil {
	return builtFacts{}, err
}
```

Run it after `reference.Extract`, because wrapper summaries and graph matching use
the complete local symbol/reference index.

- [ ] **Step 6: Add and verify the golden**

Run:

```bash
UPDATE_GOLDEN=1 go test ./internal/output -run TestIMImpactTreeGolden -count=1
go test ./internal/output -run TestIMImpactTreeGolden -count=1
```

Inspect the golden manually. It must show:

```text
changed Message
  -> Envelope/use site
  -> consumer sender
  -> inbox_msg
  -> inbox_customer_msg
```

It must not contain a concrete `inbox_conv` terminal.

- [ ] **Step 7: Run pipeline and output suites**

Run:

```bash
go test ./internal/app ./internal/output -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit the end-to-end capability**

```bash
git add internal/app internal/output testdata/fixtures/im-impact testdata/diffs/im-impact.diff testdata/golden/im-impact.impact.json
git commit -m "test: cover IM event impact pipeline"
```

### Task 8: Validate Real BFFs And Update The Smoke Suite

**Files:**
- Modify: `scripts/smoke-real-projects.sh`
- Update generated smoke artifacts only under ignored `.analyzer-smoke/`
- Do not commit business-repository changes.

- [ ] **Step 1: Extend smoke output validation**

Require every report summary to contain:

```text
impactedIMCount
impactedIMEvents
```

Require every source file to contain `impactedIMEvents`. Add a helper that accepts
an exact expected event set and rejects extras.

- [ ] **Step 2: Add an SC1 Client exact-event smoke case**

Temporarily add a harmless field to:

```text
sl-sc1-bff-service/remote/pulsar/consumer/mc/inbox.go
type GetMessageItem
```

Generate the diff from that modified file and run against the same working tree.
Expected concrete events:

```text
inbox_customer_msg
inbox_msg
```

Explicitly reject `inbox_conv`.

- [ ] **Step 3: Add an SC1 Admin exact-event smoke case**

Temporarily add a harmless field to:

```text
sl-sc1-admin-bff/service/im/im.go
type LockInventoryUpdateMsg
```

Expected event:

```text
POST/LOCK_INVENTORY_UPDATE
```

Reject every other IM event.

- [ ] **Step 4: Run the complete smoke script**

Run:

```bash
bash scripts/smoke-real-projects.sh
```

Expected:

- all existing HTTP/module cases still pass;
- both IM cases pass exact-set checks;
- repeated output is byte-identical;
- both sibling BFF repositories are clean after the script exits.

- [ ] **Step 5: Manually validate SC1 activity flow**

In `/Users/zxc/Desktop/go-analyzer-factory/sl-sc1-admin-bff`, temporarily change a
field in `remote/pulsar/consumer/activity_convert.go` type
`ActivityVoucherWinnerIm`.

Expected event:

```text
ACTIVITY/VOUCHER_WINNER
```

Generate and retain the validation JSON under `.analyzer-smoke/`, then restore and
verify the BFF worktree is clean.

- [ ] **Step 6: Manually validate SC2 wrapper discovery**

In `/Users/zxc/Desktop/backend2.0/sl-sc2-admin-bff`, temporarily change a field in
`service/im/im_do.go` type `McMessageRespImDO`.

Expected event:

```text
mc/message
```

This case validates protocol discovery across a different wrapper name and module
path. Run twice, compare byte-for-byte, retain the JSON path in the completion
report, restore the change, and verify a clean worktree.

- [ ] **Step 7: Commit the smoke coverage**

```bash
git add scripts/smoke-real-projects.sh
git commit -m "test: validate real BFF IM event impact"
```

### Task 9: Update Documentation And Run Final Verification

**Files:**
- Modify: `README.md`
- Modify: `ARCHITECTURE.md`
- Modify: `docs/contracts/output-contract.md`
- Modify: `testdata/golden/type-impact.impact.json`
- Modify: other output goldens affected by required empty IM arrays.

- [ ] **Step 1: Update the output contract**

Document:

- `summary.impactedIMCount`;
- `summary.impactedIMEvents`;
- source-local `impactedIMEvents`;
- `im_event` and `im_event_unresolved` nodes;
- concrete versus unresolved counting behavior;
- deterministic sorting.

- [ ] **Step 2: Update architecture and README**

Add `extract/im` and `graph/im` to the architecture map and pipeline sequence.
Document:

- BFF-local analysis direction;
- protocol discovery and common SDK adapters;
- zero-config policy;
- precision-first payload/control matching;
- dynamic event and opaque SDK boundaries;
- explicit non-support for upstream `sc1-server` changes.

- [ ] **Step 3: Refresh and inspect all goldens**

Run:

```bash
UPDATE_GOLDEN=1 go test ./internal/output -count=1
go test ./internal/output -count=1
```

Expected: existing reports add empty IM summaries without losing diff or endpoint
trees; the IM golden contains only the approved events.

- [ ] **Step 4: Format and run static checks**

Run:

```bash
gofmt -w internal/facts internal/extract/im internal/graph internal/impact internal/output internal/app
gofmt -l .
git diff --check
go vet ./...
```

Expected:

- `gofmt -l .` prints nothing;
- `git diff --check` prints nothing;
- `go vet ./...` exits zero.

- [ ] **Step 5: Run the complete test suite without cache**

Run:

```bash
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 6: Re-run real-project verification**

Run:

```bash
bash scripts/smoke-real-projects.sh
```

Then verify:

```bash
git -C /Users/zxc/Desktop/go-analyzer-factory/sl-sc1-admin-bff status --short
git -C /Users/zxc/Desktop/go-analyzer-factory/sl-sc1-bff-service status --short
git -C /Users/zxc/Desktop/backend2.0/sl-sc2-admin-bff status --short
```

Expected: all outputs are empty.

- [ ] **Step 7: Review the final report contract**

For retained JSON files, verify:

- endpoint summaries remain unchanged unless the same source genuinely affects
  endpoints;
- concrete IM event sets exactly match expectations;
- no app ID, mode, payload expression, or changed-field output leaked in;
- unresolved events occur only in the intended dynamic fixture;
- no source tree loses its original diff or recursive children.

- [ ] **Step 8: Commit documentation and final contract**

```bash
git add README.md ARCHITECTURE.md docs/contracts/output-contract.md testdata/golden
git commit -m "docs: document IM event impact analysis"
```

- [ ] **Step 9: Record final evidence**

In the completion response, include:

- all commit IDs created by the implementation;
- `go test`, `go vet`, formatting, diff-check, and smoke results;
- exact real-project JSON paths;
- exact event sets for every real case;
- any unresolved boundary observed during validation.
