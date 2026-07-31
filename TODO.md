# go-analyzer 技术方案一致性 TODO

> 目的：记录源码与 `TECH-DESIGN-impact-analysis.md` 目标架构之间已经确认的差距。
>
> 约束：本文只放需要实施或验证的工作，不改写技术方案，不记录开发流水账。完成一项时必须同时补测试、Schema 或文档契约，再勾选状态。

## 优先级

| 级别 | 含义 |
| --- | --- |
| P1 | 会造成不同命令结论不一致、证据丢失、安全边界失效或不可控资源消耗 |
| P2 | 会降低可观测性、契约完整性、可复现性或架构约束强度 |
| P3 | 健壮性和维护性增强，不阻断主流程 |

## P1：正确性与安全

### [x] GA-001 统一 EndpointCatalog

**差距**

`internal/impact/tree_builder.go` 实现了 Annotation-first、Route Fallback 和 Route Alias 规则。

`internal/dependency/query.go` 的 `endpointHandlers` 又独立实现了一套更简化的 Endpoint 解析。

后者在 Handler 有 Annotation 时只输出 Annotation，可能遗漏额外 Route Alias，也没有完整复用 Annotation/Route 漂移规则。

**影响**

- 同一个 Handler 在 Diff Impact、`endpoint-assets` 和 `impact --grpc` 中可能得到不同 Endpoint 集合。
- gRPC 双向查询不变量可能在 Alias 场景失效。
- 后续修改 Endpoint 规则时需要同步多个实现点。

**建议**

- 新增 `internal/endpoint`，由 Annotation、Route、Link 和 Handler 构建只读 `EndpointCatalog`。
- 把 Annotation-first、Fallback、Alias 和 Route 候选聚合全部迁入 Catalog。
- `internal/impact` 和 `internal/dependency` 只消费 Catalog，不再判断 Endpoint 身份。

**验收**

- 同 Handler 无 Annotation、Annotation/Route 一致、路径漂移、多 Annotation、多 Route Alias 均有正反例。
- `endpoint-assets(A)` 包含 gRPC B，当且仅当 `impact --grpc B` 包含 A。
- 三类查询输出的 Endpoint Key 与 Route 候选完全一致。

### [x] GA-002 统一 `endpointSourcesSummary` Chain 方向

**差距**

File/Module 来源的 Chain 从代码变化根走向 Endpoint；gRPC 来源在 `internal/output/grpc_impact.go` 中从 Handler 走向 gRPC，并把 Handler 写入 `rootSymbols`。不同 Source Type 的 Chain 方向和根语义不一致。

**影响**

- 消费方无法用一套规则解释 `chains`。
- “某接口为什么受影响”的阅读方向会随 Source Type 反转。
- gRPC Source 的真正根是 Full Method，而不是 Handler Symbol。

**建议**

- 所有 Chain 统一为 `source -> ... -> endpoint`。
- gRPC Chain 使用 `grpc full method -> generated client -> call site -> caller -> handler -> endpoint`。
- Module Chain 显式包含 Module 和 Import Usage。
- `rootSymbols` 只用于 File/Module 代码变化根；gRPC 来源输出空数组。

**验收**

- File、Module、gRPC 各有完整 Golden。
- 每条 Chain 首节点可由 Source 元数据验证，末节点必须等于所属 Endpoint。
- 同一输入重复执行得到字节相同的 Chain。

### [x] GA-003 合并同一 Endpoint 的全部 Route 证据

**差距**

`internal/output/impact_tree.go` 在构建全局和单 Source Endpoint Map 时按 Endpoint Key 直接赋值。

多个 Handler、Change Root 或 Source 命中同一 Endpoint 时，后写入的 `EndpointSummary` 可能覆盖先前的 `routes`，而不是取并集。

**影响**

- Summary 仍可能保留 Endpoint，但丢失部分 Route Alias 或注册候选。
- File、Module、gRPC 视图对同一 Endpoint 的 Route 证据可能不一致。

**建议**

- 为 `EndpointSummary` 提供唯一的合并函数。
- 所有 Map 写入统一执行 `routes` 去重并集。
- 合并键使用规范化 Method 和 Path，Route 列表稳定排序。

**验收**

- 两个 Handler 以不同 Route 注册到同一 Annotation Endpoint 时，Summary 保留两条 Route。
- 跨 File/Module/gRPC Source 合并时不丢 Route。
- `summary`、Source 摘要和 `endpointSourcesSummary` 满足技术方案第 8.8 节集合不变量。

### [x] GA-004 加固符号链接路径边界

**差距**

`internal/diff/validate.go` 使用词法清理检查 `..`，但没有解析符号链接后的真实路径；`internal/project/loader.go` 也可能解析名称以 `.go` 结尾、实际指向项目外部的符号链接文件。

**影响**

- Diff 或项目源码可以通过符号链接读取项目根之外的文件。
- “Diff 路径不能逃逸项目根”的安全契约不完整。

**建议**

- 对项目根和已存在目标执行 `EvalSymlinks`，再使用 `filepath.Rel` 验证真实路径包含关系。
- 明确新增文件、删除文件和不存在目标的安全校验策略。
- Project Loader 拒绝或安全处理符号链接源码。

**验收**

- 普通 `../`、绝对路径、目录符号链接和 `.go` 文件符号链接逃逸全部失败。
- 指向项目内部的合法符号链接行为有明确测试。
- 错误映射为稳定的输入安全错误码。

### [x] GA-005 增加取消、超时和资源预算

**差距**

影响树只有 DFS Path Cycle 检测，没有节点、深度、Diff 总量或阶段超时预算；应用入口使用无取消能力的调用方式，Dependency Discovery 从 `context.Background()` 启动。

**影响**

- 大型有向无环依赖图可能产生路径组合爆炸。
- `go list`、项目加载或传播阶段无法被上层可靠取消。
- 进程可能在异常输入下长时间占用 CPU 或内存。

**建议**

- 对外 API 接收 `context.Context`，CLI 使用信号派生 Context。
- 定义单 Root、全局节点数、最大深度、Diff 大小和文件数预算。
- 预算或取消发生时整体失败，不输出截断 JSON。

**验收**

- Cycle、超深链、宽菱形图、超大 Diff 和取消均有测试。
- 超预算返回 `analysis_budget_exceeded`，取消返回 `analysis_cancelled`。
- 正常 Fixture 和真实 BFF 结果不受预算机制影响。

### [x] GA-006 统一 Fatal Error Code

**差距**

只有部分 gRPC/Dependency 错误使用 `AnalysisError`。项目加载、Diff、配置和输出等错误仍可能以自然语言直接离开 `internal/app`。

**影响**

- CI 只能解析易变的错误文本，无法稳定区分输入错误、快照错误和分析器故障。
- 不同命令的失败语义不一致。

**建议**

- 在 `internal/app` 边界统一包装全部 Fatal Error。
- 错误码覆盖参数、项目、Diff、配置、依赖、预算、取消和输出。
- CLI 只负责稳定渲染，不在命令层猜测错误类型。

**验收**

- 每类 Fatal Error 都有 CLI 测试，stderr 包含稳定 `error_code`。
- stdout 在任何失败场景下为空。
- 调整内部错误文本不会破坏错误分类测试。

## P2：契约与可观测性

### [x] GA-007 为会话级 Diagnostic 提供结构化通道

**差距**

项目加载和事实提取 Diagnostic 可以由 `facts` 输出。

Diff 映射、删除恢复和 Module Usage Diagnostic 只存在于 `impact` 会话内，而正式 Impact JSON 不输出它们，另跑一次无 Diff 的 `facts` 也无法重建。

**影响**

- 零结果或降级结果可能缺少可消费的解释。
- Nexus/CI 无法区分“确定没有影响”和“会话内证据恢复失败”。

**建议**

- 应用 API 返回正式文档、会话 Diagnostic 和 Metrics 三类独立数据。
- CLI 保持 JSON stdout 不变，通过显式开关或独立输出文件交付结构化 Diagnostic。
- Diagnostic 必须有稳定 Code、Severity、Span 和 Related Fact。

**验收**

- Deleted Symbol Unresolved、删除 Route 恢复降级、Module File Fallback 等会话 Diagnostic 可被调用方读取。
- 默认 stdout 仍只包含正式 JSON。
- Diagnostic 的开关与输出格式有契约测试。

### [x] GA-008 补齐 `endpoint-assets` Schema 与确定性

**差距**

`schema` 只支持 `facts`、`impact` 和 `grpc-impact`；`endpoint-assets` 没有独立 Schema。`internal/dependency/query.go` 从 Map 聚合 Handler，部分切片顺序依赖 Map 遍历。

**影响**

- 一个公开命令缺少机器可校验契约。
- 相同输入存在字节输出抖动风险。

**建议**

- 增加 `schema --type endpoint-assets`。
- Schema、Go Output Struct 和 Golden 三方对齐。
- 在 Query 或 Output 边界对 Handler、Client、Chain 和 Endpoint 稳定排序并去重。

**验收**

- `endpoint-assets` 输出通过自身 Schema 校验。
- 重复执行和随机化 Fact 插入顺序仍得到字节相同 JSON。
- CLI Help、README 和技术方案中的 `--type` 值域一致。

### [x] GA-009 将 Store Builder 与只读 Snapshot 分离

**差距**

查询阶段仍持有可写的 `*facts.Store`。所谓 Freeze 主要依赖 Pipeline 顺序约定，没有类型或 API 边界阻止 Graph、Impact 或 Output 修改事实。

**影响**

- 后续并发、缓存或增量分析容易引入隐式写入。
- 模块职责只能靠 Review 维护，无法由编译器约束。

**建议**

- 事实构建阶段使用 Builder。
- Freeze 后返回只读 Snapshot 或最小查询接口。
- Slice/Map 在边界处复制或封装，禁止调用方修改底层集合。

**验收**

- Graph、EndpointCatalog、Dependency、Impact 和 Output 不接收可写 Builder。
- Freeze 后追加 Fact 的测试明确失败或无法编译。
- 现有 Golden 结果保持不变。

### [x] GA-010 gRPC-only Impact 跳过 Module Config

**差距**

`RunImpactWithMetrics` 在判断是否有 Diff 之前加载 Impact Config。仅执行 `impact --grpc` 时，一个与 gRPC 查询无关的无效 `.analyzer/go-impact.config.json` 仍会导致命令失败。

**影响**

- 独立 gRPC 查询被无关配置耦合。
- 技术方案中“没有 Diff 时跳过 Module 分支”的执行边界不成立。

**建议**

- 仅在存在 Diff 时加载配置；gRPC-only 分支完全跳过。
- 显式传入 `--impact-config` 但没有 Diff 时，选择直接拒绝无效组合或明确忽略，CLI Help 必须说明。

**验收**

- gRPC-only 查询不读取自动发现的 Module Config。
- Diff 查询仍严格校验配置。
- 组合输入只加载一次配置。

## P3：健壮性

### [x] GA-011 Impact Config 拒绝尾随 JSON

**差距**

配置解码启用了未知字段校验，但只调用一次 `Decode`，没有确认输入在第一个 JSON Value 后已经到达 EOF。

**影响**

多个拼接 JSON Value 可能被部分接受，严格配置语义不完整。

**建议与验收**

- 首次解码后再次解码并要求 `io.EOF`。
- 增加尾随对象、尾随数组和非空垃圾文本测试。

### [x] GA-012 明确 Facts 绝对路径脱敏策略

**差距**

`facts` 面向调试输出项目根目录；Impact 产物不包含绝对项目路径。两类产物进入共享平台时的脱敏责任尚未形成统一机器规则。

**影响**

- Facts 可能泄漏 CI Runner 或开发机目录结构。
- 调用方容易误把调试产物按普通影响报告公开存储。

**建议与验收**

- 明确 Facts 是本地调试契约，或增加受控的路径脱敏投影。
- Smoke Test 校验对外产物不出现绝对路径。
- 接入文档定义 Facts 与 Impact 的访问级别和保留周期。
