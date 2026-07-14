# go-analyzer 架构与审查交接

本文是下一位 agent 熟悉 `go-analyzer` 的长期入口，描述稳定的架构边界、模块职责、分析规则和全源码审查要求。

阅读顺序：先读本文和 [ARCHITECTURE.md](ARCHITECTURE.md)，随后以源码、测试、CLI 实际行为和 JSON Schema 为准。`docs/` 下的文件不属于本轮 Review 范围，也不作为架构或输出结论的事实依据。

## 1. 项目定位与边界

`go-analyzer` 是单个 Go 项目的静态影响范围分析器，采用“事实抽取优先（facts-first）”架构。

- 对 BFF 项目，分析代码变更或输入 gRPC operation 对 HTTP endpoint、IM event 与 gRPC 调用资产的影响。
- 对后端 Go 服务，分析代码变更对已注册 gRPC、HTTP、Dubbo、XXL-Job 入站契约的影响。
- 每次只分析一个仓库。跨仓的 `gRPC -> BFF -> frontend` 串联、多 BFF 汇总与最终回归编排由外部调用方消费多个 JSON 后完成。
- 分析结果只输出静态可证明的结论。动态 path、动态 version、外部 SDK 隐藏调用、反射或无法唯一定位的 handler 不得伪造成确定结果。

## 2. 命令边界

| 命令 | 项目类型 | 输入 | 正式输出 |
| --- | --- | --- | --- |
| `impact` | BFF | 已应用的 `--diff` 和/或多个 `--grpc` | 受影响 HTTP endpoint、IM event、可选上游 gRPC consumer 证据 |
| `endpoint-assets` | BFF | 一个或多个 `--endpoint` | endpoint 对 gRPC 的依赖资产 |
| `grpc-impact` | 后端 Go 服务 | 已应用的 `--diff` | 受影响的 gRPC、Dubbo、HTTP、XXL-Job 入站契约 |
| `facts` | 支持的 Go 项目 | `--project` | 原子 facts 与 diagnostics，仅用于排障 |
| `schema` | 无项目加载 | `--type facts|impact|grpc-impact` | 稳定 JSON Schema |

路径参数必须是绝对路径。`impact` 至少传 `--diff` 或 `--grpc` 之一，二者可以组合且只输出一份 JSON。`grpc-impact` 的 diff 必须已应用到 `--project` 的变更后源码。`--timings` 只写 stderr，JSON stdout 不得混入日志、timing、diagnostics 或 `buildContext`。

## 3. 分层架构

```text
project.Load + AST index + diff parser
        -> protocol extractors
        -> facts.Store
        -> reverse graph / route graph
        -> impact 或 serviceimpact
        -> internal/output
        -> 稳定 JSON / Schema
```

| 层 | 主要目录 | 责任 | 不应承担的责任 |
| --- | --- | --- | --- |
| CLI | `cmd/go-analyzer` | 参数校验、命令分派、stdout/stderr 边界 | AST 解析、协议匹配、JSON 业务拼装 |
| 应用编排 | `internal/app` | 加载项目、组织 facts 构建、调用分析与输出 | 协议专属 AST 规则 |
| 项目与索引 | `internal/project`、`internal/astindex` | package 加载、build constraint、源文件与符号定位 | 影响结论推导 |
| 事实抽取 | `internal/extract/*` | 从 AST 提取符号、引用、route、gRPC、Dubbo、Job、IM 等原子事实 | 跨协议最终影响聚合 |
| 事实存储 | `internal/facts` | fact ID、symbol、change、置信度、diagnostics 的统一模型 | 对外 JSON 兼容逻辑 |
| 图与连接 | `internal/graph`、`internal/link` | 反向引用、route 到 handler 的可达性与连接关系 | 输出字段组装 |
| BFF 分析 | `internal/impact` | 由 diff/gRPC root 向 BFF endpoint、IM 传播 | 后端服务入口投影 |
| 服务分析 | `internal/serviceimpact` | 由 diff root 投影到入站服务契约 | BFF controller annotation 语义 |
| 输出 | `internal/output` | 去重、排序、固定空数组、JSON 与 Schema | 重新解释 AST 或补全分析事实 |

核心约束：extractor 只产生协议 facts；图层只连接事实；`impact` 与 `serviceimpact` 只得出影响结论；`output` 只稳定呈现结论。不要为了“统一”而在 `facts.Store` 增加第二套泛化 contract fact，也不要让输出层倒推分析语义。

## 4. BFF 分析架构

`impact` 的根可以是代码 diff，也可以是外部给定的 canonical gRPC method，例如 `/package.OrderService/GetOrder`：

```text
diff / gRPC operation
  -> ChangeFact / gRPC consumer evidence
  -> ReverseGraph
  -> controller / route handler
  -> HTTP endpoint、IM event、source summary
```

- endpoint 的主要身份来自 controller annotation；route 解析提供注册证据与 `routes` 信息。
- annotation 路径与 route 路径不一致时，两者都属于证据，但不可让动态 route 推断覆盖 annotation 定义的 endpoint 身份。
- `endpoint-assets --endpoint` 采用 annotation 格式，例如 `GET /orders/:id`。
- BFF gRPC 调用需同时具备 generated client、静态 receiver 类型、项目内可执行调用链三类证据；不穿透外部 SDK，也不跨 BFF 仓分析。
- BFF 的 `impact` 通过一个 JSON 同时容纳 diff 模式和 gRPC 输入模式，不能重新拆成多个彼此孤立的正式结果。

## 5. 服务入口分析架构

`grpc-impact` 是后端服务的入站契约影响分析。命令名为兼容保留，语义覆盖 gRPC、HTTP、Dubbo、XXL-Job：

```text
diff
  -> ChangeFact
  -> ReverseGraph
  -> 已注册的 concrete handler
  -> grpc_operation / http_endpoint / dubbo_method / job
  -> 按协议分组的 service-entry JSON
```

该链路不查询 BFF，也不建模 outbound HTTP、gRPC、Dubbo。Pulsar/IM producer 与 consumer 尚未进入 service-entry 终点模型，需在后续独立定义事实、证据和输出语义，不能直接套用现有实现。

### 5.1 协议识别规则

| 协议 | 最低证据 | 身份与终点关系 | 审查要点 |
| --- | --- | --- | --- |
| gRPC | generated `ServiceDesc`、实际 `RegisterXxxServer`、具体实现、匹配 handler | `fullMethod` / `exposed_grpc_operation` | 注册与实现是否一一对应，未注册实现不能成为终点 |
| HTTP | route registration、verb、group/wrapper、可达 handler | 静态 `METHOD resolvedPath` / `exposed_http_endpoint` | 动态前缀必须标记 `symbolic` 并保留表达式，不伪造 URL |
| Dubbo | `ExportProviders`、`*ApiExport`、`ServiceConfig`、其后的 `SetProviderService`、`MethodMapper` 或唯一 Go method | `interface@version/method` / `exposed_dubbo_method` | 多 provider 顺序绑定；method 配置只影响单方法，service 配置影响对应 interface 的全部方法 |
| XXL-Job | 指定 map 类型的注册、静态 job name、可解析 handler | job name / `registered_job` | 动态任务名或无法唯一绑定 handler 不进入正式结果 |

HTTP、Dubbo、Job 还要求 registration liveness：注册函数有项目内引用，或符合 `main`、`Register*`、`Initialize*` 启动约定。未满足时不进入正式 summary；当前实现不为此独立输出 diagnostics。

## 6. 输出契约

### 6.1 BFF 输出

`impact` 输出 endpoint、IM event、按 diff 文件组织的传播树，以及在输入 `--grpc` 时的 consumer/call-site 证据。`fileSources` 保留原始 diff、变更根与递归影响树；`summary` 是全局去重的正式结论。字段定义以 `internal/output` 实现和 `schema --type impact` 为准。

### 6.2 服务入口输出

`grpc-impact` 只有一种正式聚合模型。`summary`、每个 `fileSources[].impacts`、`entrySourcesSummary` 都固定有 `grpc`、`dubbo`、`http`、`job` 四个数组：

```json
{
  "summary": {"grpc": [], "dubbo": [], "http": [], "job": []},
  "fileSources": [
    {
      "sourceFile": "...",
      "symbols": {},
      "impacts": {"grpc": [], "dubbo": [], "http": [], "job": []}
    }
  ],
  "entrySourcesSummary": {"grpc": [], "dubbo": [], "http": [], "job": []}
}
```

- `summary`：全局去重后的正式服务入口结论。
- `fileSources`：diff、变更符号、完整传播树、当前 source 的同形协议分组摘要。
- `moduleSources`：仅当 `go.mod` 形成 semantic module change 时出现。
- `entrySourcesSummary`：终点反查到 file/module source 的轻量视图。

四个数组即使为空也必须输出，且排序必须稳定。不得恢复 `impactedContracts`、`impactedGrpcOperations`、`contractSourcesSummary`、`grpcOperationSourcesSummary` 等旧 gRPC 镜像字段。它们会使同一结论存在两套竞争聚合方式。

真实实现位置：`internal/output/grpc_service_impact.go`、`internal/output/contract.go`。

## 7. 全源码深度 Review 工作规程

“深度 review”默认审查当前项目的**全部源码与输出契约**，不是只审当前 diff，也不能只跑测试后给出“无问题”的结论。除非用户要求修复，默认先输出 findings，再修改代码。

### 7.1 覆盖范围

| 范围 | 必查内容 |
| --- | --- |
| `cmd/go-analyzer` | 子命令分派、绝对路径校验、错误返回、help、stdout/stderr 边界 |
| `internal/app` | facts 构建隔离、diff 的变更后源码约束、错误与 diagnostics 传递 |
| `internal/project`、`internal/astindex` | package 加载、build constraint、符号 ID、跨 package 与生成代码 |
| `internal/extract/*` | AST 匹配证据、相邻/同名调用误匹配、动态表达式的保守处理 |
| `internal/facts`、`internal/graph`、`internal/link` | fact identity、反向引用方向、去重、循环保护、route-handler 连接 |
| `internal/impact`、`internal/serviceimpact` | changed root 可达性、终点过滤、liveness、confidence、terminal relation |
| `internal/output` | 去重、排序、空数组、Schema、BFF/service-entry 输出隔离 |
| `testdata`、`*_test.go` | 正反例覆盖、CLI help/Schema 与实现的一致性 |

真实验证至少覆盖 `sc1-server`、`sc2-server`。涉及 BFF 或 gRPC consumer 时，还应使用同级真实 BFF 项目。没有对应真实范式时，review 结论必须标为“未验证”，不能推断为已支持。`docs/` 下的文件不审查、不产生 findings，也不需要为其补齐漂移修复。

### 7.2 必须审查的维度

1. **架构与边界**：命令职责是否重叠；facts 是否被输出层污染；协议特例是否绕过统一 projection；跨仓能力是否错误混入单项目分析。
2. **正确性与健壮性**：追踪每一种 change kind 到 terminal；重点检查同名符号、多注册、同函数多 provider、wrapper、闭包、方法值、跨包引用、nil/空集合、循环引用和文件级变更。
3. **静态分析准确性**：每项影响结论必须可回溯到 AST、facts、调用链或注册证据；动态 route/version、反射、外部 SDK、无法唯一解析 receiver 均不得产出伪确定结论。
4. **协议语义**：gRPC server 注册、HTTP route、Dubbo ServiceConfig/provider 顺序绑定、XXL-Job map 注册，以及方法级与 service 级配置的影响范围。
5. **输出契约**：字段语义、协议分组、排序、空数组、source summary、Schema 与 CLI 实际输出一致性；重点检查旧镜像字段不会复活。
6. **失败语义与可观测性**：非法/未应用 diff、越界路径、解析失败、无结果与 diagnostics 是否稳定；JSON stdout 不得被日志、timing 或调试字段污染。
7. **性能与可维护性**：重复 AST 扫描、图遍历复杂度、map 遍历不稳定、隐式全局状态、难以扩展的协议分支。只报告能定位到代码的风险。
8. **测试有效性**：既审正例也审反例，包括多 provider、同 interface 多 method、service-level fan-out、动态 HTTP、未注册 handler、无引用注册函数、同名调用和真实多文件 diff。

### 7.3 推荐步骤与交付格式

```bash
rg --files -g '*.go' -g '*_test.go' -g '*.md' -g 'go.mod'
go test ./...
go vet ./...
go build ./cmd/go-analyzer
git diff --check
go run ./cmd/go-analyzer schema --type facts
go run ./cmd/go-analyzer schema --type impact
go run ./cmd/go-analyzer schema --type grpc-impact
```

按“CLI -> app -> extractor/facts -> graph/link -> analyzer -> output/schema”的数据流阅读，不要按目录随机抽读。协议改动或发现可疑路径后，构造已应用的真实项目 diff，覆盖单/多文件、handler 变更和注册配置变更。

使用以下格式交付 review：

- findings 优先，按 P0、P1、P2、P3 排序；
- 每条包含精确文件和行号、可复现路径或最小 AST/真实项目场景、风险说明、建议修复方向和测试建议；
- 未发现阻断问题时，明确列出未覆盖协议、真实样本缺口、未执行工具和残余风险；
- 不要以架构摘要代替 findings，也不要把代码风格偏好包装为高优先级缺陷。

若环境提供 `staticcheck`，也应执行 `staticcheck ./...`；工具不可用时须在报告中说明。
