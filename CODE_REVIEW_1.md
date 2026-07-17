# go-analyzer 全源码深度 Review 报告

> 日期：2026-07-16
> 范围：go-analyzer 全部源码、测试、CLI 实际行为、JSON Schema（不含 `docs/`）
> 模式：review-only，未修改任何代码
> 真实验证项目：sc1-server、sc2-server、sl-sc1-admin-bff、sl-sc1-bff-service、sl-sc2-admin-bff

## 方法与基线

- **分片审查**：6 个并行深度 review agent 覆盖 CLI/app、project/astindex/diff、route/annotation/link、reference/graph、协议抽取（grpc/dubbo/job/im）、impact/serviceimpact/output/schema，每片都在真实项目上实证。
- **基线工具**：`go build` ✅、`go vet` ✅、`go test ./...` ✅ 全绿、`git diff --check` ✅。
- **staticcheck 无法运行**：本机 Go 1.24 标准库 `export` 版本不被当前 staticcheck 支持（`unsupported version: 2`），属环境限制，非项目问题；需在兼容环境补跑后合并结论。
- **独立复核**（非仅采信 agent）：ValidateApplied 删除缺陷、iota 枚举丢边、route_group 伪事实、dubbo/grpc-server 真实抽取正确、gRPC 双向不变量成立，均由本人直接验证。
- 所有探针 diff 已还原，6 个仓库 `git status` 全部干净。

**总体结论：无 P0**（无崩溃、无顶层输出契约破坏、无旧镜像字段复活、Schema 与实际输出一致）。三个 P1 为真实正确性/契约缺陷，建议优先修。

---

## P1（必须修）

### P1-1 · `iota` 续行枚举成员丢失类型 → 大量调用边系统性漏报（false negative）
- **位置**：[astindex/index.go:155](internal/astindex/index.go#L155) `indexValueReceiverTypes`、[astindex/index.go:399](internal/astindex/index.go#L399) `valueTypeFromValueSpec`；同源缺陷另见 [reference/scoped_types.go:153](internal/extract/reference/scoped_types.go#L153)（仅处理 `token.VAR`，忽略 `token.CONST`）。
- **机制**：Go 的 `const ( A T = iota; B; C )` 中 B、C 的 `spec.Type` 与 `spec.Values` 均为 nil（隐式继承上一 spec）。逐 spec 处理时 B、C 拿不到类型，无法进入 `ValueReceiverTypes`，`pkg.B.M()` / `pkg.C.M()` 因此无法解析 receiver，调用边丢失并转成 `symbol_reference_unresolved` 诊断。
- **复现（已验证）**：sc1-server `modules/inbox/proto/constants/inbox_sender_type.go` 正是此模式（`SENDER_MERCHANT/USER/BOT` 为 `SenderType = iota` 续行常量，带 `.Name()/.Val()` 方法）。代码中 `constants.SENDER_USER.Val()` 被调用 ≥3 处（`inbox_ec_inner_service.go:119/196/987`），但 facts 中**到达 `SenderType` 方法的 call 边 = 0**。sc2-server 同样 44 条同类未解析。
- **影响**：改动枚举访问器（`Name()/Val()/String()` 等贯穿 sc1/sc2 共享库的 Java 枚举转译产物）时，数百个 dispatch 调用点不会进入反向可达树，**仅经枚举访问器可达的 endpoint/IM event 被报为"未受影响"**——直接违背"不漏报受影响接口"的核心契约。
- **修复方向**：在单个 `GenDecl` 内追踪最近一次显式 `spec.Type`，续行 spec 继承之；最小修复仅补 `Type` 继承即可恢复方法分派。
- **测试建议**：fixture `const ( A T = iota; B; C )` + 方法 + 调用 `pkg.B.M()/pkg.C.M()`，断言对 B、C 都产出 `method:pkg:T:M` 的 call 边且无 unresolved 诊断。

### P1-2 · `ValidateApplied` 放行"未应用的文件末尾删除 diff"，破坏输入确定性
- **位置**：[diff/validate.go:61](internal/diff/validate.go#L61)（`if len(change.ExpectedLines) == 0` 门控）+ [diff/validate.go:80](internal/diff/validate.go#L80) `deletedBlockStillPresent`。
- **机制**：纯删除 diff 的唯一防线"被删块仍原样存在"只在**零 ExpectedLine 时启用**。但 `git diff -U3` 删除文件尾部时只有**前导上下文行**（无尾随上下文），这些上下文行成为非空 ExpectedLine，逐行校验对未改源码同样成立，于是跳过块检查，**未应用删除被当成已应用**。
- **复现（已独立复现）**：最小工程 `f.go` 含 `func A`/`func B`，构造 `-U3` 删除尾部 `func B` 的 diff **但不应用**，`grpc-impact` **exit 0 并产出 JSON**（应拒绝），错误地把变更归因到 `func B`。
- **影响**：冲击 impact/grpc-impact 的"diff 必须已应用"输入保证；删除锚点按变更后行号映射到变更前源码，`MapChanges` 会归因到错误符号，产生 plausible-but-wrong 结论。
- **修复方向**：去掉 `len(ExpectedLines)==0` 门控，对每个 `DeletedBlock` 无条件运行 `deletedBlockStillPresent`（≥2 行块逐字命中近似确定未应用）。单行 EOF 删除仍残留，建议注释说明。
- **测试建议**：表驱动用例——N 行文件、仅含前导上下文的尾部删除 hunk，断言源码仍含被删行时报错、删除后放行。

### P1-3 · 为非路由函数伪造 `route_group` 事实（公开 facts 契约污染）
- **位置**：[extract/route/extractor.go:241-243](internal/extract/route/extractor.go#L241) `rootGroups`（`fieldIndex==0` 无条件作为根 group）+ [extract/route/rules.go:36-41](internal/extract/route/rules.go#L36) `isRouteGroupWrapper`（`HasPrefix("add")` 过宽）。
- **机制**：首参无条件当根 group（仅为补偿 `isRouterGroupType` 只认字面名 `RouterGroup`）；`AddProduct`/`AddKeyword` 等 gRPC/服务方法名命中 `"add"` 前缀；`resolveRouteFunctionCall` 无法解析 `var.Method(...)` 的包变量 receiver，使 `returnsGroup` 守卫失效。三者叠加导致普通函数被误判为 group wrapper。
- **复现（已独立验证）**：sl-sc2-admin-bff `service/cart/cart.go:107` `func AddProduct(ctx context.Context, in *cartProto.CartAddProductReq)` → 产出 `route_group:...cart::AddProduct:_:1`，**`parent_group_var: "ctx"`**（参数名，非路由 group）。同类污染在三个真实 BFF 均出现（middleware/remote-grpc/service 等处）。
- **影响**：公开 `route_groups` 数组跨所有真实 BFF 携带错误事实；任何消费/诊断若信任 route_groups 都会被误导。**注意：endpoint 身份不受影响**（这些函数不注册 HTTP 路由，annotation-first 完好）——故不升级结论严重度，但属"已发布契约数组中的错误数据"。
- **修复方向**：移除 `fieldIndex==0` 特例，要求每个根 group 参数都是 group-like；放宽 `isRouterGroupType` 识别 `*Group`/`RouterGroup`/`Router` 名（保 fixture 仍过）；收紧 `"add"` 规则（如 `add`+`guard/group/validator/middleware`）。
- **测试建议**：反例 fixture `func AddProduct(ctx, *T){ c.AddProduct(ctx,in) }`，断言 `store.RouteGroups` 为空。

---

## P2

### P2-1 · `facts` 命令不运行服务入口抽取器，却声明这些字段
- **位置**：[app/pipeline.go:280](internal/app/pipeline.go#L280) `buildFacts`；仅 [app/grpc_impact.go:157-181](internal/app/grpc_impact.go#L157) 运行 job/dubbo/grpc-server 抽取。
- **现象（已验证）**：sc1-server/sc2-server 跑 `facts` 时 `dubbo_providers/job_registrations/grpc_providers` 恒为 0，但 Schema 声明这些键。另用 `grpc-impact` 直接验证这些抽取器**本身工作正常**（见"正向验证"），故这是 facts 管线缺口而非协议 bug。
- **影响**：handoff 明示 `facts` 是"排障入口"，但它对 grpc-impact 最关键的 server-side 事实（dubbo/job/grpc-provider）零可见，排障价值被削弱。
- **修复方向**：要么 facts 也跑服务入口抽取（区分 BFF/服务项目类型），要么在 Schema/help 中显式标注 facts 仅含 BFF 域事实。
- **测试建议**：对 sc1-server 跑 facts 断言 dubbo/grpc-provider 非空（若采纳方案 A）。

### P2-2 · Dubbo 多 provider 分组布局下顺序绑定失效（false negative，潜伏）
- **位置**：[extract/dubbo/extractor.go:167-184](internal/extract/dubbo/extractor.go#L167) `providerServiceExpressionAfter`。
- **机制**：每个 ServiceConfig 绑定到自身 `end` 之后的**第一个** `SetProviderService`。分组布局（多 config 堆在前、多 call 堆在后）时，第 N 个 config 错绑到第一个 call：`config[Beta].end` 之后的首个 call 仍是 `SetProviderService(&AlphaAPI{})`，因 Alpha 无 `beta` 方法而静默丢弃 Beta 方法。
- **影响**：分组布局项目漏报 Dubbo provider。sc1-server 实际用**单文件单 export**（每函数一 config 一 call，已验证），故当前不触发；但属真实潜伏缺陷，违反协议规则"多 provider 按顺序绑定"。
- **修复方向**：按位置排序对 config 与 call 做配对（config[i]→call[i]），或消费后标记 call 已绑定。
- **测试建议**：`ExportGrouped(){ config[Alpha]; config[Beta]; SetProvider(Alpha); SetProvider(Beta) }`，断言 Beta 方法被抽取。

### P2-3 · `AddFact` 诊断插入 O(n²)
- **位置**：[diagnostics/facts.go:29-50](internal/diagnostics/facts.go#L29)。
- **机制**：每次 `AddFact` 新建 Collector、重加全部已存诊断、全量 `ToFact` 重转、重排。sc1-server 实测 410 条诊断（395 条 `symbol_reference_unresolved`），大仓数千条时为构建期热点。
- **修复方向**：Store 上持有持久 Collector（seen-set + 有序 slice），O(1) 追加、渲染时排序一次。
- **测试建议**：Benchmark 插入 N=10k 诊断，断言近线性。

### P2-4 · 链式 `.Group().Group()` 未解析 → `resolved_path` 不完整
- **位置**：[extract/route/extractor.go:819-826](internal/extract/route/extractor.go#L819)（Group 分支 `selector.X` 仅认 `*ast.Ident`）。
- **复现（真实）**：sl-sc1-admin-bff `router/router.go:56` `adminWithoutAuthGroup := g.Group(WEB_BFF_PREFIX).Group("")` 该 local 未被当 group，跨函数前缀 `/admin/api/bff-web` 缺失（如 `router/mc/broadcast.go:37`）。
- **影响**：`routes` 证据 resolved_path 混凝土但不完整。架构允许"不完整 resolved route"，且受影响路由均有 annotation（身份正确）；但无 annotation 的此类路由 fallback 身份会错。
- **修复方向**：Group 分支递归——`selector.X` 为 `*ast.CallExpr` 时经 `groupCall` 求父前缀再拼接。
- **测试建议**：`g.Group("/api").Group("").GET("/orders",h)` → 断言 `/api/orders`。

### P2-5 · entrySources 每条 source confidence 可高于其 contract 的合并值（内部矛盾）
- **位置**：[output/grpc_service_impact.go:340-402](internal/output/grpc_service_impact.go#L340) `buildEntrySourcesSummary`、[output/impact_tree.go:695](internal/output/impact_tree.go#L695) `addEndpointSourceFile`（用最短路径 `weakestConfidence`，独立于 contract 的跨全路径最弱值）。
- **现象**：真实数据 205/4325 source 行 confidence 强于其所属 contract（如 module source=high，contract=medium）。
- **影响**：不违反规则（不同字段），但人工按 confidence 分诊时会读到"自相矛盾"的行。属一致性/可读性问题，不改结论。
- **修复方向**：文档化该字段为"按最短链路"；或选置信度最弱的那条路径。
- **测试建议**：fixture——同一 contract 同时被 high 直根与 low 传递根命中，断言预期 verdict。

---

## P3（汇总，影响低/潜伏）

- **输入阶段错误未 typed**：[app/grpc_impact.go:55-82](internal/app/grpc_impact.go#L55)、[app/pipeline.go:114-142](internal/app/pipeline.go#L114) 的 diff 读/解析/校验/`validateChangedGoFiles` 失败不带 `error_code=`，而 gRPC 抽取失败却带——`grpc-impact` 最常见的"diff 未应用"反而不 typed，消费者无法分类。
- **impact-config 无关时仍加载校验**：[pipeline.go:114](internal/app/pipeline.go#L114)/[grpc_impact.go:50](internal/app/grpc_impact.go#L50)；纯 `--grpc` 或无 go.mod 变更也严格校验项目 `.analyzer/go-impact.config.json`，坏配置波及无关运行。
- **`remoteImports := imports[:0]` 原地别名过滤**：[grpc_impact.go:204-209](internal/app/grpc_impact.go#L204)，当前安全但若上游 slice 被缓存将静默损坏。
- **domain-fact 映射用首匹配而非最小 span**：[diff/mapper.go:141-181](internal/diff/mapper.go#L141)（仅 symbol 回退做最小 span）；重叠 span 时归因依赖上游顺序稳定。
- **纯重命名无法识别为未应用**：[validate.go:17-44](internal/diff/validate.go#L17)，无 ExpectedLine 时不校验 OldPath 已删。
- **value-kind 方法引用硬编码 `ConfidenceHigh`**：[reference/values.go:142](internal/extract/reference/values.go#L142)，丢弃经结构体字段链降级的 medium。
- **ReverseGraph 排序无 col/ID 终极 tie-break**：[graph/reverse.go:41](internal/graph/reverse.go#L41)，同 FromSymbol+StartLine 相对序不稳定（下游 merge 兜底，无害）。
- **节点合并保留首个 confidence 而非最弱**：[serviceimpact/tree.go:382](internal/serviceimpact/tree.go#L382)、[output/impact_tree.go:517](internal/output/impact_tree.go#L517)、[impact/tree_builder.go:596](internal/impact/tree_builder.go#L596)（权威 summary 不受影响）。
- **impact/grpc-impact 文档顶层无对齐守卫**：[output/contract_alignment_test.go](internal/output/contract_alignment_test.go) 仅守 facts 顶层与 impact/grpc-impact 的 `$def` 属性级，顶层 required/omitempty 不守。
- **Dubbo service-level 扇出可能产出幽灵方法**：[dubbo/extractor.go:284-309](internal/extract/dubbo/extractor.go#L284)（sc1/sc2 全用 method-level config，不触发）。
- **gRPC 流式模式漏认包限定 ServiceDesc**：[grpc/catalog.go:380-402](internal/extract/grpc/catalog.go#L380)，标准 protoc 输出不触发，仅 StreamingMode 错（canonical method 仍对）。
- **liveness 非递归**：[serviceimpact/tree.go:190-199](internal/serviceimpact/tree.go#L190)，被死函数调用的注册仍判活；真实项目均已核实为真活。
- **Job 抽取硬编码类型名/包名**：[job/extractor.go:18](internal/extract/job/extractor.go#L18)（`JobListener`/`TaskFunc`、`jobx`/`xxljob`），自定义 wrapper 项目为盲区。
- **`RouteGroupFlowFact` 文档与 tag 不符**：[facts/route_flow.go:8](internal/facts/route_flow.go#L8)。

---

## 正向验证（经真实项目实证为正确，给结论以置信）

- **Dubbo 抽取正确**：sc1-server 改 `LoginTokenApi.SaveOrUpdateLoginToken` → grpc-impact 正确产出 `com.shopline.uc.client.api.LoginTokenApi/saveOrUpdateLoginToken`，动态 version 标 `symbolic`（`dubboVersionExpression: apiVersion`，不伪造），仅命中被改方法。
- **gRPC server 抽取正确**：sc1-server 改 `InboxBizProvider.GetConversationList` → 正确产出 canonical `/gopkg...BizInboxService/GetConversationList`（来自 generated transport，非 Go selector 反推），liveness 通过 `wire_set.go` 注册。
- **gRPC 双向不变量成立**：sl-sc1-bff-service 上 `impact --grpc M`↔`endpoint-assets GET /path` 双向一致（M=`/gopkg...McFormClientService/GenerateUserId`），`relation=may_call`。
- **输出契约**：`grpc-impact` 顶层 `summary/entrySourcesSummary/fileSources[].impacts` 四数组恒在（空亦输出）、稳定排序；禁用的旧镜像字段全仓 grep 为 0；`impact` 不泄露 buildContext/diagnostics；`schema --type *` 与实际输出无漂移；summary↔entrySourcesSummary confidence 在真实 diff 上 499 契约 0 不一致。
- **route/annotation 健壮**：三个 BFF 路由全解析（581/35/136），无重复 route/annotation ID，HTTP method 注解与 route 两侧一致大写，annotation-first 身份正确。
- **排除虚警**：一度观测到 sc1-server `facts` 跨次输出不一致，查实为**首轮并行 agent 在共享真实仓注入探针**所致污染，**非分析器 bug**——源码稳定时输出可复现（已用隔离 GOCACHE 验证）。

---

## 未覆盖项与残余风险

- **Pulsar/IM producer 非 service-entry 终点**：sc2-server 有活跃 Pulsar producer（`util/pulsar.go`、`modules/medium/initializer/pulsar.go`），分析器无对应抽取器——出站消息路径不可见。这是已知能力缺口（非 bug）。
- **staticcheck 未执行**：环境 Go 1.24 不兼容当前 staticcheck，需在兼容环境补跑后合并结论。
- **grpc/dubbo/job contract 路径的真实覆盖有限**：经直接验证 dubbo + grpc-server 在 sc1 正确产出；但 job contract 仅 fixture 覆盖、IM `directProtocolSummary` 的 `Body` 字段配对启发式有罕见误配风险（34 event/31 resolved，3 条正确 unresolved）。
- **链式 group 前缀传播**只经 `callContexts` 不经 emitted flows；`g.Use(a,b,c)` 多参数、`NewGroup(g,"/x",mw)` 等仅代码处理无 fixture。
- **生成代码无 `Code generated` 识别**：generated `.go` 按普通源索引，未发现由此引发的正确性问题但属设计盲点。

---

## 优先级建议

三个 P1（**P1-1 iota 枚举丢边**、**P1-2 ValidateApplied 删除放行**、**P1-3 route_group 伪事实**）建议优先修，均已有清晰修复方向与测试建议。其余 P2/P3 可排期处理，其中 P2-2（Dubbo 分组顺序）虽当前不触发但属真实协议缺陷，建议在引入分组布局项目前修复。
