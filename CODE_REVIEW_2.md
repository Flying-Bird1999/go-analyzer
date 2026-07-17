# go-analyzer 全源码深度 Review — Findings

> 范围：`go-analyzer` 全部源码与输出契约（非 `docs/`）。按 handoff.md §7 工作规程执行。
> 数据流阅读顺序：CLI → app → extract/facts → graph/link → impact/serviceimpact → output/schema。
> 日期：2026-07-17。

## 0. 执行状态与工具缺口（必读）

**本轮未能执行任何 Go 工具链，findings 全部来自静态阅读。** 沙箱未预装 Go，且代理服务器封禁了所有 Go 分发主机（`go.dev`、`dl.google.com`、`storage.googleapis.com`、GitHub `release-assets.githubusercontent.com`）以及 `api.github.com`，无法安装 Go。因此 handoff §7.3 要求的以下步骤**均未执行**，属于残余风险：

- `go build ./cmd/go-analyzer`、`go test ./...`、`go vet ./...`、`staticcheck ./...`
- `go run ./cmd/go-analyzer schema --type facts|impact|grpc-impact`
- `git diff --check`
- 对 `sc1-server`、`sc2-server` 及真实 BFF 的 CLI 真实运行验证（`bash scripts/smoke-real-projects.sh`）

替代手段：对 extractor 的关键假设做了针对真实项目源码（`sc1-server`、`sc2-server`、`sl-sc1-bff-service`、`sl-sc2-admin-bff`）的**静态**交叉核对（HTTP verb 大写、Dubbo 动态 version 表达式、单实现 gRPC 注册等模式均与实现假设一致）。凡需运行才能坐实的结论，均标注「未验证（需运行）」。

**结论概览：未发现 P0 阻断性缺陷。** 核心架构边界、facts-first 分层、无旧镜像字段复活、空数组恒定、稳定排序、BFF/service-entry 输出隔离均成立。主要问题集中在：一处与文档明确承诺的 confidence 不变量相矛盾的实现分歧（P1）、关键反例/负路径的测试缺口（P1），以及若干需要非典型但合理输入才能触发的正确性风险（P2）。

---

## 1. P1 — 严重（正确性 / 文档不变量违背）

### P1-1　`entrySourcesSummary` 的 per-source confidence 取「最短路径」而非「最弱路径」，违背 §6.2.1 明确承诺的不变量

- **文件**：`internal/output/grpc_service_impact.go:359-368`（`buildEntrySourcesSummary`）与 `:404-419`（`shortestContractPath`）。
- **代码事实**：`shortestContractPath` 仅按 `len(candidate) < len(best)` 选择路径，完全忽略 confidence；随后 `confidence := weakestConfidence(path)` 只对这条**最短**路径求最弱值。而 handoff §6.2.1 明文承诺：「`entrySourcesSummary` 另从树路径独立计算 `weakestConfidence(path)`，两者结果一致」。
- **可复现场景**：变更根 `S` 被两个调用者引用——`A`（边置信度 high）与 `B`（边置信度 medium，例如 `value_ref` 或 diff 恢复得到的 medium 引用）；`A`、`B` 均引用暴露契约 `C` 的 handler `H`。由于 `A→H`、`B→H` 挂在不同父节点，树中保留两个终点 `C`：`S→A→H→C`（最弱=high）与 `S→B→H→C`（最弱=medium），二者长度都为 4。`shortestContractPath` 按 `Children` 顺序返回先命中的那条；若命中 `A` 路径，则 `source.Confidence = high`，尽管同一 source 存在一条 medium 的传播路径。
- **对照**：summary 侧 `serviceimpact.recordContract` 对**所有**路径取最弱，故 `contract.confidence`（文档定义的正式不变量字段）= medium，正确。于是同一 `ContractSourceSummary` 内 `Contract.Confidence=medium` 与 `Sources[].Confidence=high` 自相矛盾，且 source 侧高估了证据强度——正是 §6.2.1 要防止的「弱路径被静默升级」，只是发生在 source 粒度。
- **风险**：消费 `entrySourcesSummary[].sources[].confidence` 做反查的调用方，会得出「该变更以 high 置信度到达契约」的错误结论。虽然正式的 `contract.confidence` 安全，但文档承诺的一致性被打破。
- **建议修复**：在 `buildEntrySourcesSummary` 中枚举 root→contract 的**所有**路径，取 `min(weakestConfidence(p))` 作为 `source.Confidence`；`shortestContractPath` 的结果仅用于展示 `chain`。
- **测试建议**：构造双调用者菱形（high 与 medium/low 边）到达同一 handler/契约，断言 `entrySourcesSummary.<proto>[0].sources[0].confidence` 等于 `summary.<proto>[0].confidence`（应为较弱者）。

### P1-2　关键反例 / 负路径缺乏测试，且 `internal/serviceimpact` 无任何单元测试

- **文件**：`internal/serviceimpact/`（仅 `tree.go`，无 `*_test.go`）；`internal/extract/grpc/server_extractor.go`、`server_catalog.go`（无对应 `*_test.go`）；`testdata/golden/` 仅有 `mini-bff.facts.json`、`type-impact.impact.json`（无 grpc-impact golden）。
- **事实与风险**（handoff §7.2 item 8 明确要求正反例并审）：
  - `registrationIsLive` 返回 `false` 的分支（handler 存在但注册函数无项目引用、且名字不符合 `main`/`Register*`/`Initialize*`）是「未注册实现不得成为终点」的核心闸门，**当前无任何测试**——`grpc-service` fixture 中每个注册函数都被引用或符合命名约定，闸门恒为 `true`。一个使 liveness 恒真/恒假的回归能通过全部测试。
  - gRPC server 注册身份的 `ServerImplementationAmbiguityError`（`server_extractor.go` ~102-105，同函数多实现歧义）与 `ServerBindingIssue`（注册存在但实现不可证）两条路径，全仓无测试驱动。
  - `symbolic` 身份（动态 HTTP path、动态 Dubbo version）在 grpc-impact 输出端到端未测：`*_test.go` 中检索 `symbolic`/`identityResolution`/`pathExpression`/`dubboVersionExpression` 零命中。此分支必须保留原始表达式且不伪造 URL/version（§5.1），回归无测试可捕获。
  - 无 grpc-impact 的字节级 golden，也无「diff 命中空 → 四数组恒在且为空」的端到端测试；impact/grpc-impact 结构体缺 `required` 字段与 `omitempty` 的对齐守卫测试（仅 facts 有），`confidence` 必填契约可能静默漂移。
- **建议修复/测试**：
  1. 新增 `internal/serviceimpact/tree_test.go`，用真实 `facts.Store` 直接覆盖：未注册实现（`RegistrationSymbol` 无引用且命名非启动约定，如 `wireProviders`）→ 契约不入 summary；`DubboServiceChanged` 根 → 该 interface 全部方法出现、其他 interface 不出现；循环引用 → 标记 `Cycle` 并终止；同 handler 两条不同置信度边 → 记录最弱。
  2. gRPC server 歧义/绑定失败 fixture，断言返回对应 typed error / `HandlerSymbol==""`（下游因此不成终点）。
  3. symbolic 端到端 fixture（动态 HTTP 前缀 + 非字面量 Dubbo `Version:`），断言 `identityResolution:"symbolic"` 且表达式字段非空、无伪造解析值。
  4. 新增 `grpc-service` 字节级 golden 与空结果四数组断言；将 facts 的 `required`↔`omitempty` 对齐测试扩展到 impact/grpc-impact 结构体。

---

## 2. P2 — 中（真实正确性风险，需非典型但合理的输入）

### P2-1　Dubbo `ServiceConfig → SetProviderService` 采用「其后第一个调用」的位置启发式，无 interface/type 交叉校验、无歧义诊断

- **文件**：`internal/extract/dubbo/extractor.go:167-184`（`providerServiceExpressionAfter`），调用点 `:50-58`。
- **事实**：每个 `ServiceConfig` 字面量绑定到其结束位置**之后第一个** `.SetProviderService(...)` 调用，不校验该调用是否注册同一 interface，也**没有**类似 gRPC 侧 `ServerImplementationAmbiguityError` 的歧义检测——直接确定性地取一个候选，并按 `resolveProviderType` 的置信度（常为 high）产出 fact。
- **可复现场景**：同一函数内两个 `ServiceConfig`（A、B）与两个 `SetProviderService` 调用交错，源码顺序为 `configA, configB, SetProviderService(B_impl), SetProviderService(A_impl)`：configA 绑定到 `B_impl`（错误），configB 也绑定到其后第一个即 `B_impl`——A 的真实实现漏报、B 被重复计。若两个 interface 恰有同名方法（`Get`/`List` 等），`mapper`/`uniqueGoMethod` 仍可命中，错误被静默掩盖。handoff §5.1 明确把「多 provider 顺序绑定」列为审查要点。
- **风险**：对真实 owner 的变更被归到错误的 `interface@version/method`，或反之漏报。现有多 provider 测试只覆盖「config 后紧跟自己的 SetProviderService」的非交错顺序，未覆盖此类。
- **建议修复**：按 `[config.end, nextConfig.start)` 区间要求恰好一个 `SetProviderService`；出现多候选或跨区间时产出歧义诊断而非猜测（对齐 gRPC 侧做法）。
- **测试建议**：同函数两个交错 config 的 fixture，断言正确配对或显式诊断，而非静默高置信度错误 fact。

### P2-2　`forwardChains` 全局 visited 集丢失 gRPC 备选调用链证据

- **文件**：`internal/dependency/query.go:194-217`。
- **事实**：BFS 用单个 `visited`（`{handler:true}`）跨所有路径共享，符号只在首次到达时展开。handler `H` 经 `A` 与 `B` 都到达 helper `C`（`C` 发起 gRPC `Op`）时，`C` 只被 `A` 路径记录一次，`H→B→…→C→Op` 链永不产出。
- **风险**：operation 本身仍被上报（去重后 `Op` 存在，**双向不变量不破**），但 `Chains`/`Symbols` 证据不完整——「endpoint 如何到达该 RPC」的证据是产品价值点，`endpoint-assets` 与 `impact --grpc` 共用此函数，两侧证据同样缺失。
- **建议修复**：不要用 `visited` 门控 gRPC-call 采集，只门控 callee 的重复入队；或按「到达该符号的边」去重，记录所有 `(caller→Op)` 对。
- **测试建议**：两条不同 handler 路径汇聚到同一 gRPC helper 的 fixture，断言产出两条 `Symbols` 前缀不同的 `Chains`。

### P2-3　diff 解析器 hunk 分类无 `default` 兜底；被去除行尾空白的空上下文行会使行号错位

- **文件**：`internal/diff/parser.go:133-196`。
- **事实**：hunk 内分类 `switch` 仅处理 `+`/`-`/`" "`（前导空格）三种前缀，无 `default`。标准 unified diff 中空白源行以 `" "`（单空格）表示，但被编辑器/CI/复制粘贴去除行尾空白后会变成真正的空行 `""`，任何 case 都不匹配，`oldLine`/`newLine` 均不前进。
- **风险**：此后所有 `ExpectedLine`、`LineRange` 行号偏移 1。`ValidateApplied` 或误判「未应用」拒绝一个实际已应用的 diff，或经 `MapChanges` 把变更挂到错误符号，产出「看似有效实则错误」的影响结论——直接冲击核心「diff 必须已应用」契约。（注：`git diff` 默认输出为 `" "`，本问题只在空白被 mangle 的 diff 上触发。）
- **建议修复**：hunk 内把纯空行 `""` 当作空白上下文行处理（两计数器同时前进）；并加 `default:` 对未知前缀报错，而非静默忽略。
- **测试建议**：含 `""` 空上下文行的 hunk，断言其后行号与 `" "` 变体一致；被去空白但已应用的 diff 仍通过 `ValidateApplied`。

### P2-4　`deletedBlockStillPresent` 用旧行号索引变更后文件；多删除 hunk 的 `-U0` diff 校验不可靠

- **文件**：`internal/diff/validate.go:61-94`（仅在 `len(change.ExpectedLines)==0` 时使用）。
- **事实**：对无上下文的纯删除 diff，唯一的已应用性检查读取 `lines[block.OldStartLine-1 ...]`，其中 `lines` 是**变更后**文件，而 `OldStartLine` 是**旧版本**行号。单文件多删除 hunk（或删除位于更早已应用 hunk 之后）时新旧编号错位，可能漏检真正未应用的删除（stale diff 蒙混过关 → 错误影响）或误拒已正确应用的 diff。
- **风险**：恰在该检查设计要覆盖的场景（无上下文删除）削弱「未应用/过期 diff 必须失败」保证。*（推理得出，未运行验证。）*
- **建议修复**：对无上下文删除，或要求调用方提供上下文、或全文件扫描被删块、或按累计已应用行 delta 平移旧→新锚点；至少在单文件多删除块时产出诊断。
- **测试建议**：两个删除 hunk 的 `-U0` diff 只应用第一个，断言 `ValidateApplied` 失败。

### P2-5　`registrationIsLive` 的命名约定可放行「命名符合但从未被引用」的死注册函数

- **文件**：`internal/serviceimpact/tree.go:190-199`。
- **事实**：`name == "main" || HasPrefix(name,"Register") || HasPrefix(name,"Initialize")` 是纯命名匹配。名字以 `Register`/`Initialize` 开头但从未被 `main`/`init` 调用、零项目引用的死函数（如 `RegisterHelperUnused`）也被判 live，其契约进入正式 summary。该实现与 handoff §5.1 文档约定一致，但约定本身让命名匹配绕过了「有项目内引用」的要求，与「未注册实现不能成为终点」的精神相悖（对死注册路径）。
- **建议修复**：优先用对 `main`/包 `init` 的引用可达性判定；命名约定仅作为可达性不可用时的降级，且当仅凭命名放行时产出诊断。
- **测试建议**：`Register*` 命名但无引用、不可达的注册函数 → 断言契约不入 summary（或被标记）；及被引用的正例。

### P2-6　IM `Body`/`Event` 结构配对按变量**名**而非接收者**类型**，同名变量复用可误配

- **文件**：`internal/extract/im/summary.go:412-490`（`directProtocolSummary`）。
- **事实**：配对仅以 `recv.Name` 字符串为键（`bodyPayload[recv.Name]`），不校验 `.Event(...)` 接收者与持有 `Body` 的复合字面量是否为同一类型。现有正例测试只证明「不同变量名的干扰项」能规避，未覆盖同一函数内同名变量（`data`/`req`/`msg`）先后赋给不相关类型再赋给真正 `SendData` 的情形（`addBodyPayload` 首次占位）。
- **风险**：复用常见局部变量名的业务函数可能产出错误或重复的 `IMEventFact`（正是「按名匹配、错误接收者」风险类）。
- **建议修复**：接受配对前，用 `astindex`/引用解析校验 `Event` 接收者静态类型属同一 IM SDK 类型族，或至少确认赋值与 `Event` 调用之间该标识符未被重新赋为不相关复合字面量。
- **测试建议**：同名变量在一函数内先后赋给不相关类型与真实 `SendData` 的 fixture，断言不误选。

### P2-7　route 非白名单 wrapper 的「最后一个 handler 形状实参」兜底，误命中时不产出诊断

- **文件**：`internal/extract/route/handler.go:44-59`（`handlerArgument`）、`rules.go:12-20`（`handlerWrappers` 白名单）。
- **事实**：调用名不在白名单时，回退猜测「最后一个 handler 形状实参」为真 handler 并记为合成 `WrapperFact`。当 wrapper 语义并非「原样转发其 handler 实参」（如记录/审计后返回另一个闭包、或交换实参、或最后一个「handler 形状」实参实为无关业务数据）时会误绑；且与「未找到 wrapper」不同，此路径**不触发** `CodeRouteUnresolvedHandler`，因为它「成功」产出了一个 handler 形状表达式。
- **风险**：静默把伪造/错误 handler 符号当作 endpoint 的真 handler，无诊断信号（§7.2.3 静态准确性）。此为架构自述的启发式取舍（「不是 go/types 替代品」），残余风险窄但真实。
- **建议修复**：当经结构兜底且 wrapper 名不在白名单时，降低置信度或产出专门诊断，供下游/评审区分「已验证 wrapper」与「猜测 wrapper」。
- **测试建议**：一个**不**原样转发 handler 的 wrapper fixture，确认当前会绑到表面形状实参，并据此决定是否降级。

---

## 3. P3 — 低（非确定性硬化 / 延迟隐患 / 性能，均可定位到代码）

- **输出/图排序 tie-break 不足（非确定性 JSON 风险）**：
  - `internal/graph/reverse.go:41-46`：`sort.Slice` 仅按 `FromSymbol`+`StartLine`，非稳定排序；同行两次调用同目标无进一步 tie-break。建议追加 `StartCol`→`Kind`→`ID`。
  - `internal/dependency/query.go:256-260`：client 绑定排序把 `GoPackage+ClientType+GoMethod` 无分隔符拼接，`{"ab","c","d"}` 与 `{"a","bc","d"}` 均为 `"abcd"`，边界碰撞导致顺序不定。建议逐字段比较。
- **重复累积/去重不一致**：
  - `internal/graph/route.go:62-75`：`ChildGroupsByID` 从 `ParentGroupID` 与 `RouteGroupFlows` 两处 append 且不去重（`sort()` 只排序不去重）。`RoutesForGroup` 有 `seenGroups` 兜底故输出无害，但索引本身误导且浪费递归。
  - `internal/link/linker.go:45-48`：导出的 `LinkRoute` 每次新建 `linkedHandlers`，对共享 handler 的多个 route 逐个调用会向 `store.Links` 追加重复 `handler_to_annotation`（ID 确定，ID 级去重可收敛，但直接遍历 `store.Links` 的消费方见重复）。
  - `internal/graph/call.go:26-30`：`grpcByCaller` 用普通 `append`，与 `forward`/`reverse` 的 `appendSymbolOnce` 不一致（今日安全，因 `GrpcCallFact` ID 唯一）。
- **`CombineConfidence` 未知非空值静默透传**：`internal/facts/reference.go:37-61`，`rank` 对非 `low/medium/high` 返回 0（等同空），畸形非空 confidence 被当作「未知」由另一操作数胜出，可能掩盖数据 bug。建议未知非空值按最弱（rank 1）防御或文档化仅四个合法值。
- **性能（可定位）**：
  - `internal/diagnostics/facts.go:29-50`：`AddFact` 每次从全部 `store.Diagnostics` 重建 `Collector` 再排序，O(n²) 累积。大项目 `facts` 运行时明显退化（未验证，需运行）。建议持有单个 `*Collector`，render 时排序一次。
  - `internal/link/symbol.go:12-22`：`fileByRelativePath` 每次 O(packages×files) 且每文件 `filepath.Rel`，按 route/中间件绑定各调用一次。建议预建 `map[relPath]*project.File`。
- **语义/延迟隐患（今日不影响输出）**：
  - `internal/extract/im/summary.go:641-652`：`IMEventFact.Confidence` 恒为 `ConfidenceHigh`，即使事件模板未解析（下游按 `Resolved` 布尔正确排除，故输出无误，但 `facts` 排障视图字段名误导）。建议由 `resolved` 派生或文档化字段含义。
  - `internal/impact/tree_builder.go:168-171`：middleware 根 confidence 被先由 `middlewareNode` 内 `CombineConfidence(parent,High)` 设置（子节点据此构建）后又被调用方覆盖回 `change.Confidence`。当前 lattice 下等价，但若 middleware 边改为非 high 则子节点会与重设的根分歧。建议一次性传入最终根置信度。
  - `internal/impact/deleted_route.go:437-486`：`resolveDeletedRouteGroup` 用 `NewAnchorLine`（变更后近似行）对变更后 group span 做「就近取前一个 group」，同名 `GroupVar` 多 group 或删除引起行移时可能误绑前缀 → 错误 `deleted_route_endpoint` path（未验证，需真实多 group 删除样本）。建议优先按同一 `RouteFunc`/所在函数身份绑定，歧义时诊断。
  - `internal/app/grpc_impact.go:204-209`：`remoteImports := imports[:0]` 复用入参底层数组，仅因 `ServerRegistrationImportPaths` 返回全新切片而安全；建议改 `make(...)`。
- **测试/可用性小项**：
  - `cmd/go-analyzer/main_test.go`：有 `schema --type facts`、`grpc-impact` 的 CLI 测试，缺 `schema --type impact`。
  - `internal/app/pipeline_test.go:26-130`：多处仅断言 `len(...)==N` 或无 error，锁基数不锁身份，扩展 gRPC-only（`impact --grpc` 无 `--diff`）路径的端到端 shape 无测试（未验证，需运行）；`--timings`+error 时 stdout 保持空亦无回归测试。
  - `cmd/go-analyzer/main.go:327-352`：`endpoint-assets` help 较薄，未复述绝对路径要求与 `--format`/`--timings`。

---

## 4. 已核对且判定健康（非 findings，供留痕）

- 无旧镜像字段复活：`impactedContracts`/`impactedGrpcOperations`/`contractSourcesSummary`/`grpcOperationSourcesSummary` 仅出现在 handoff.md 与一处**负向断言**（`cmd/go-analyzer/main_test.go`），非测试源码零命中。grpc-impact 仅一套聚合模型（`summary`+`fileSources[].impacts`+`entrySourcesSummary`，各含 grpc/dubbo/http/job）。
- `facts.CombineConfidence` 正确实现链路最弱合并（low<medium<high），空/未知处理有单测；`contract.confidence` 的跨根/跨文件最弱合并（`mergeContractSummary`、`recordContract`）成立——**P1-1 仅影响 source 侧派生字段，不影响正式 `contract.confidence`**。
- `ReverseGraph` 方向为 callee→callers（与文档一致）；`RouteGraph.RoutesForGroup` 有 `seenGroups` 循环保护；空/nil 集合处理普遍防御（`NewStore` 初始化非 nil 空切片，查询返回防御性拷贝）。
- endpoint↔gRPC 双向不变量结构性成立：`FindGrpcImpactSources` 复用 `FindEndpointAssets`/`endpointHandlers` 同一关系，无方向不对称。
- 动态 HTTP 前缀与动态 Dubbo `Version` 均保留原始表达式为 `symbolic`，不伪造（§5.1）；reference 层歧义 interface dispatch 用诊断而非猜测；IM SDK 按 import-path+函数名精确匹配。
- serviceimpact 未查询 BFF、未建模 outbound、未跨仓；`may_call` relation、`impact` 不输出 `buildContext`/diagnostics 均符合契约；`registrationIsLive` 正确门控空 `HandlerSymbol`（未注册实现不成终点）——除 P2-5 的命名约定过宽外成立。
- 依赖方向符合 ARCHITECTURE §7：`graph`/`link`/`dependency` 仅依赖 `facts`（`dependency` 另依赖 `graph`），未反向引入 `output`/`impact`/`cmd`。

---

## 5. 未覆盖协议 / 真实样本缺口 / 未执行工具 / 残余风险

- **未执行工具**：`go build`/`go test`/`go vet`/`staticcheck`/`schema` 命令、`git diff --check` 及真实项目 smoke 全部未运行（无 Go 运行时）。所有「未验证（需运行）」标注项待补跑。
- **未做动态真实验证**：`sc1-server`、`sc2-server` 与真实 BFF 的实际 CLI 输出、最终 JSON 排序稳定性、双向不变量的运行时验证均未执行；仅做了源码模式的静态交叉核对。P1-1、P2-1、P2-3、P2-4、P3 中的 deleted_route 误绑等均为代码推理，未观测到运行时复现。
- **协议范围缺口**：Pulsar/IM producer 与 consumer 尚未进入 service-entry 终点模型（§5 明示为后续工作），本轮未审、不产出结论——保持「未支持」，不可推断为已支持。
- **建议补跑清单（获得 Go 后按序）**：`go build ./cmd/go-analyzer` → `go vet ./...` → `go test ./...` → `staticcheck ./...` → 三个 `schema` 子命令快照对齐 → 用真实 sc1/sc2 构造已应用的单/多文件 diff（handler 变更、gRPC/Dubbo/Job 注册与配置变更、多 provider 交错、动态 path/version、未注册 handler 反例）跑 `impact`/`grpc-impact`/`endpoint-assets` 并核对四数组、confidence、symbolic 字段与双向不变量。
