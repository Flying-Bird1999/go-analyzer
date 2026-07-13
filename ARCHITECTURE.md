# go-analyzer Architecture

> 状态：当前实现基线，更新于 2026-07-13。
> 读者：维护者、评审者和后续迭代 agent。

## 1. 定位

`go-analyzer` 面向**单个 Go BFF 项目**进行静态分析。它不聚合多个 BFF 仓；调用方需要多仓结果时，应分别执行并自行汇总。

核心问题：

```text
一份 BFF 源码快照中，某个代码 diff 和/或上游 gRPC operation 变更，
最终关联哪些 HTTP endpoint 与出站 IM event？
```

系统只输出可由静态事实证明的关系，不猜测运行时注入、反射、外部 SDK 内部调用或动态路由。

## 2. 对外边界

| 命令 | 用途 | 输入 |
| --- | --- | --- |
| `impact` | 统一影响分析主入口 | `--diff` 和/或可重复的 `--grpc` |
| `endpoint-assets` | 查询 BFF endpoint 依赖的 gRPC | 可重复的 `--endpoint` |
| `facts` | 提取与调试项目事实 | `--project` |
| `schema` | 输出 facts/impact JSON Schema | `--type facts|impact` |

所有路径参数必须为绝对路径。`impact` 至少需要一个 `--diff` 或 `--grpc`；两者可组合，输出一份 JSON。`--timings` 仅写 stderr，绝不污染 JSON stdout。

```text
impact --diff
  -> BFF 代码或 go.mod 变更影响

impact --grpc
  -> 上游 gRPC operation 在当前 BFF 的静态 consumer

impact --diff --grpc
  -> 同一份 JSON 中合并两类 source
```

`endpoint-assets` 的输入格式为 `METHOD /path`，与 controller annotation 协议对齐；gRPC 输入必须是 canonical full method：`/package.Service/Method`。

## 3. 核心模型

分析器采用 facts-first 模型：extractor 只提取事实，graph 与查询层只消费 facts，output 只负责稳定投影。

```text
project source
  -> project loader + AST index
  -> facts extraction
  -> linking / query graphs
  -> diff and/or gRPC source projection
  -> stable JSON
```

`facts.Store` 是模块间唯一共享总线。模块不得直接依赖其他 extractor 的私有 AST 状态。

主要事实：

| Fact | 作用 |
| --- | --- |
| `SymbolFact` | 稳定声明 identity |
| `ReferenceFact` | 项目内 call/type/value 引用 |
| `AnnotationFact` | controller annotation 与 endpoint 协议 |
| `RouteRegistrationFact` | router 注册、local/resolved path、handler |
| `GrpcOperationFact` | generated client transport 中的 canonical gRPC operation |
| `GrpcCallFact` | BFF 调用点、静态 client binding 与 operation 关联 |
| `IMEventFact` | 出站 IM event 及其依赖 |
| `ChangeFact` | diff 映射得到的传播根 |

## 4. Pipeline

### 4.1 Facts 构建

`internal/app/buildFacts` 是共享构建入口：

```text
project.Load
  -> astindex.Build
  -> annotation / route / reference / IM extraction
  -> route-handler-annotation linking
  -> optional gRPC dependency discovery and extraction
```

gRPC extraction mode：

| 场景 | mode | 失败行为 |
| --- | --- | --- |
| `impact` 仅 diff | `off` | 不加载 gRPC dependency，保持 diff 性能与语义 |
| `facts` | `diagnostic` | 写入 diagnostic，保留其他 facts |
| `endpoint-assets`、`impact --grpc` | `strict` | 失败即返回 typed error，无部分 JSON |

### 4.2 Impact

`RunImpactWithMetrics` 按输入执行可选分支，但只构建一次 facts：

```text
impact(project, [diff], [grpc...])
  -> optional diff parse + applied snapshot validation
  -> build facts
  -> optional diff map / deleted route recovery / module usage / impact tree
  -> optional FindGrpcImpactSources
  -> BuildImpactDocument
  -> JSON
```

diff 必须已应用到 `--project` 指向的变更后源码快照。未应用、过期或不匹配的 diff 直接失败，避免以错误行号产生看似有效的结论。

## 5. Endpoint 语义

endpoint identity 采用 annotation-first 规则：

1. handler 存在 controller annotation 时，annotation 的 `method/path` 是正式 endpoint。
2. 缺少 annotation 时，使用已解析的 route method/path 作为 fallback。
3. 已解析 route 总以同级 `routes` 输出，作为辅助证据；它可为空或不完整，不能覆盖完整 annotation。

原因是 route 注册可能经过动态拼接、wrapper 或跨函数 group flow，静态解析不能被当成唯一真值；同时 annotation 与 route 漂移需要对调用方可见。

```json
{
  "method": "POST",
  "path": "/admin/api/bff-web/orders",
  "routes": [
    {"method": "POST", "path": "/api/bff-web/orders"}
  ]
}
```

`method/path` 是正式结论，`routes` 是已静态解析的注册候选。一个 endpoint 可以有多个 route 候选，输出不能任意选择或丢弃其中之一。

## 6. gRPC 关系

gRPC 主键只能来自 generated transport 的 canonical full method，不能通过 Go selector method、变量名或目录名反推。

一条 BFF -> gRPC 关系必须同时满足：

1. generated client catalog 存在该 canonical operation。
2. 调用 receiver 可静态解析为对应 generated client binding。
3. endpoint handler 到调用点存在项目内 executable call chain。

不支持或拒绝猜测：

- 外部 SDK 内部隐藏的 gRPC 调用。
- 反射、运行时注入、未解析 interface dispatch。
- 业务代码直接调用 `Invoke` / `NewStream`。
- 仅使用 protobuf message、相同 method 名或相似变量名的代码。

查询方向：

```text
endpoint-assets
  annotation endpoint -> handler -> project callees -> GrpcCallFact

impact --grpc
  canonical operation -> GrpcCallFact -> project callers -> handler -> annotation endpoint
```

两条查询在同一项目快照和 build context 下必须满足双向不变量：

```text
endpoint-assets(A) contains gRPC B
iff
impact --grpc B contains endpoint A
```

`impact --grpc` consumer 的 `relation` 固定为 `may_call`：静态调用链可达，但不承诺每次 HTTP 请求都会执行 RPC。

## 7. 模块边界

| 模块 | 职责 | 不应承担 |
| --- | --- | --- |
| `cmd/go-analyzer` | 参数、绝对路径校验、stdout/stderr | 分析规则 |
| `internal/app` | pipeline 编排、mode、typed error | AST 细节与 JSON 结构拼装 |
| `internal/project` | Go module、build context、文件与 AST 加载 | 传播和业务语义 |
| `internal/extract/*` | 从 AST / dependency 提取原子 facts | 跨事实传播、JSON 渲染 |
| `internal/link` | 连接 route、handler、annotation 等事实 | 产生业务 endpoint 结论 |
| `internal/graph` | ReverseGraph、RouteGraph、CallGraph、IMGraph 查询视图 | 修改 facts |
| `internal/dependency` | endpoint <-> gRPC 双向查询 | CLI、diff 解析 |
| `internal/impact` | 从 ChangeFact 构造传播树 | gRPC catalog 猜测 |
| `internal/output` | 稳定排序、JSON、Schema | 生产或补全业务事实 |

依赖方向：

```text
project / astindex
  -> extract / facts
  -> link / graph / dependency / impact
  -> output
  -> app
  -> cmd
```

不得反向引入 `output -> extract`、`extract -> impact` 或 CLI 内分析逻辑的依赖。

## 8. 输出契约

`impact` 顶层固定包含：

```json
{
  "summary": {},
  "fileSources": [],
  "grpcSources": [],
  "endpointSourcesSummary": []
}
```

- `summary`：全局去重后的正式 endpoint 和 IM event 结论；endpoint 条目包含 `method`、`path`、`routes`。
- `fileSources`：diff source、原始 diff 与完整传播树。
- `moduleSources`：仅 go.mod 形成语义 module change 时输出。
- `grpcSources`：输入 gRPC operation、consumer 证据、client binding、call-site chain 与 endpoint 摘要。
- `endpointSourcesSummary`：endpoint -> file/module/grpc source 的轻量反查。

`impact` 不输出 `buildContext` 或 diagnostics。需要排查事实和 diagnostics 时使用 `facts`。字段级 schema、排序规则和兼容性要求以 [output contract](docs/contracts/output-contract.md) 为准。

## 9. 扩展原则

新增能力时先判断它属于哪一层：

1. 新语法模式：在对应 extractor 中提取 facts，并增加最小 fixture。
2. 新静态关系：在 linker 或 graph 中建立查询视图，不在 output 补推。
3. 新 impact source：复用 `RunImpact` 与统一 `ImpactDocument`，不要新增孤立报告命令。
4. 新输出字段：同步 Go struct、Schema、output contract、golden；只输出已证明的事实。
5. 新 gRPC pattern：先证明 generated transport、receiver binding、project call chain 三类证据仍完整；否则拒绝或诊断，不降级猜测。

修改 endpoint identity、传播方向、严格失败语义或 JSON 顶层 shape 属于架构变更，必须更新本文件、专项设计和真实项目验证。

## 10. 维护入口

| 主题 | 文档或入口 |
| --- | --- |
| 字段级 JSON 与 Schema | [docs/contracts/output-contract.md](docs/contracts/output-contract.md) |
| gRPC 专项设计 | [docs/bff-grpc-dependency-assets/design.md](docs/bff-grpc-dependency-assets/design.md) |
| gRPC 实施与验收记录 | [docs/bff-grpc-dependency-assets/implementation-plan.md](docs/bff-grpc-dependency-assets/implementation-plan.md) |
| 真实 BFF 验证 | [docs/validation/real-project-validation.md](docs/validation/real-project-validation.md) |
| CLI 用法 | [README.md](README.md) |
| 完整测试 | `go test ./...` |
| 真实项目 smoke | `bash scripts/smoke-real-projects.sh` |

本文件只维护稳定的架构决策。测试命令细节、fixture 清单、真实项目基线、历史案例和调试步骤应维护在对应专项文档或脚本中。
