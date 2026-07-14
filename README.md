# go-analyzer

`go-analyzer` 是面向单个 Go 服务项目的影响范围分析工具。当前支持 BFF 的 HTTP/IM 与 gRPC 依赖分析，以及后端服务的 gRPC/HTTP/Dubbo/XXL-Job 入站契约影响分析。

当前已经完成基于变更后项目的符号级影响分析闭环：BFF 输出 HTTP/IM 影响；后端服务通过 `grpc-impact` 输出 gRPC、Dubbo、HTTP、XXL-Job 入站契约。

## 背景

我们已经在前端 `React + TypeScript` 项目里验证了影响范围分析模型：

```text
diff -> 变更语义节点 -> 依赖传播 -> 业务入口
```

在前端项目里，业务入口可能是页面、组件、API 调用点或手动指定 source。Go BFF 项目的入口更明确：最终需要回归的是 HTTP 接口和 BFF 主动发送给前端的 IM event。因此 Go 侧可以先独立完成：

```text
Go BFF diff -> 受影响 HTTP 接口 / IM event
```

后续如果要和前端影响分析打通，只需要把 Go analyzer 输出的 HTTP 接口作为前端 analyzer 的 `--api` 输入即可。这个桥接可以后置，MVP 不需要承担前端页面分析。

## MVP 目标

第一版回答一个问题：

```text
这次 Go BFF diff 影响了哪些 HTTP 接口和 IM event？
```

MVP 覆盖范围：

- Go 源码 diff 分析。
- diff 到 Go 语义节点的映射。
- 函数、方法、变量、常量、类型引用的反向传播。
- struct 字段和 tag 变更映射到所属 type，并沿类型依赖传播。
- controller 函数注释中的 HTTP 接口识别。
- route 注册关系、route group、中间件、guard/wrapper 的影响传播。
- 当前 Nexus/codegen 标准模板生成的 Lego route 注册链路。
- 删除 route registration 的单行/多行恢复。
- go.mod require/replace 变更到本仓使用点再到 endpoint 的传播。
- BFF 本仓 payload、event 常量或发送控制逻辑变更到出站 IM event 的传播。
- 基于 `broadcast://`、`/broadcast/send` 协议特征及常用 IM SDK 的零配置识别。
- 常见 package var / struct field middleware selector 的轻量类型推断。
- 输出完整的 symbol → route → annotation → endpoint / IM event 传播树及轻量摘要。

MVP 暂不覆盖：

- 前端页面影响范围分析。
- gRPC、BFF 与前端之间的跨仓自动编排。
- sc1-server 或其他上游仓变更到 BFF IM 的跨仓传播。
- 运行时 route table 抽取。
- AI 报告生成。
- 多实现接口分发、反射和复杂 DI 场景的精确分析；包级接口变量仅在生产源码可证明唯一具体实现时解析。
- 配置驱动的 middleware exclude 等 path-sensitive 控制流精确分析。

## 目标项目

第一批分析对象是前端团队维护的三个 Go BFF：

- `sl-sc1-admin-bff`
- `sl-sc1-bff-service`
- `sl-sc2-admin-bff`

smoke 脚本同时兼容 SC1 历史目录名 `sc1-admin-bff` / `sc1-bff-service`。

这些项目都大致遵循：

```text
router -> controller -> service -> remote
```

它们使用 `lego.RouterGroup` 做 Gin-like 路由注册，但具体前缀来源、wrapper、中间件写法并不完全一致。因此 analyzer 不能只为单个仓库写死规则，而要抽象出 BFF 项目族的通用分析模型。

## 核心思路

整体分析管线：

```text
diff
  -> 变更节点识别
  -> Go 语义索引
  -> 反向引用图
  -> route / IM 领域图
  -> 影响传播
  -> 受影响 HTTP 接口 / IM event
```

## 快速开始

前置条件：

- Go 1.24 或更高（见 `go.mod`）。
- 仅依赖 Go 标准库，无需额外安装第三方库。

构建与测试：

```bash
go build ./cmd/go-analyzer   # 产出 go-analyzer 二进制
go test ./...
```

首次影响分析（无需安装，直接 `go run`；路径参数必须为绝对路径）：

```bash
go run ./cmd/go-analyzer impact --project /absolute/path/to/sl-sc1-bff-service --diff /absolute/path/to/change.diff --format json

go run ./cmd/go-analyzer grpc-impact --project /absolute/path/to/sc1-server --diff /absolute/path/to/change.diff --format json
```

调试 facts（检查 symbol / route / reference / IM event / diagnostics 是否被正确抽取）：

```bash
go run ./cmd/go-analyzer facts --project /absolute/path/to/sl-sc1-bff-service
```

查询 BFF endpoint 的下游 gRPC，或将上游 gRPC operation 作为 impact source 反查 BFF endpoint：

```bash
go run ./cmd/go-analyzer endpoint-assets --project /absolute/path/to/sl-sc1-bff-service --endpoint "GET /orders/:id"
go run ./cmd/go-analyzer impact --project /absolute/path/to/sl-sc1-bff-service --grpc "/package.OrderService/GetOrder"
```

更详细的架构、模块职责、能力边界与调试指南见 [ARCHITECTURE.md](ARCHITECTURE.md)。

## CLI 使用

对外接入可以使用 `impact`、`endpoint-assets` 和 `grpc-impact`。CLI help 使用中文描述，命令名和参数名保持英文，便于脚本集成。CLI 边界要求输入路径使用绝对路径：

```bash
go-analyzer impact --project /absolute/path/to/sl-sc1-bff-service --diff /absolute/path/to/change.diff --format json
go-analyzer impact --project /absolute/path/to/sl-sc1-bff-service --diff /absolute/path/to/change.diff --impact-config /absolute/path/to/go-impact.config.json --format json
```

### 命令与参数参考

`impact`、`endpoint-assets`、`grpc-impact` 是对外接入命令；`facts`、`schema`、`--timings` 为开发调试能力（详见 ARCHITECTURE.md）。

| 命令 / 参数 | 用途 | 面向 |
| --- | --- | --- |
| `impact` | 从已应用 diff 和/或上游 gRPC operation 分析受影响 HTTP 接口 / IM event | 接入 |
| `grpc-impact` | 从已应用 diff 分析后端服务受影响的 gRPC/HTTP/Dubbo/XXL-Job 入口 | 接入 |
| `endpoint-assets` | 查询一个或多个精确 endpoint 的 gRPC 依赖 | 接入 |
| `facts` | 输出项目 facts JSON，用于调试抽取结果与 diagnostics | 调试 |
| `schema` | 输出 facts / impact / grpc-impact JSON Schema，校验稳定输出契约 | 调试 |
| `--project` | 目标项目根目录（绝对路径） | 接入 |
| `--diff` | 已应用到变更后源码的 unified diff（绝对路径） | 接入 |
| `--impact-config` | 可选 module 版本变更过滤配置（绝对路径） | 接入 |
| `--format` | 输出格式，默认 `json` | 接入 |
| `--goos` / `--goarch` / `--tags` / `--cgo` | 指定 Go build context，影响 build constraint 文件过滤；未指定按 `go/build` 默认值 | 调试 |
| `--timings` | 把各 pipeline stage 耗时写到 stderr | 调试 |

### 诊断与可观测性

- `impact` JSON **不含** diagnostics（设计如此，保证接入输出稳定）。
- 用 `facts` 查看项目级诊断，例如非变更文件解析失败产生的 `package_load_failed`。
- 用 `--timings` 查看各 stage 耗时（写到 stderr，不污染 stdout 的 JSON）。
- 用 `schema --type facts|impact|grpc-impact` 校验或对齐输出契约。

`endpoint-assets` 的 `--endpoint` 采用 controller annotation 格式，例如 `GET /orders/:id`；`impact` 的 `--grpc` 采用 canonical full method，例如 `/package.OrderService/GetOrder`，可与 `--diff` 组合。gRPC 关系仅在 generated client、静态 receiver 类型和项目内可执行调用链共同证明时输出；不穿透外部 SDK 的隐藏调用，也不进行跨 BFF 仓聚合。

`grpc-impact` 在单个后端服务项目内输出已注册 gRPC、HTTP、Dubbo 和 XXL-Job 入口。JSON 固定按 `grpc`、`dubbo`、`http`、`job` 分组；命令不查询 BFF，Pulsar/IM 为后续待办。详细边界见 [service external impact design](docs/service-external-impact/design.md)，下一步工作见 [handoff](handoff.md)。

lego BFF 的 route、annotation、handler wrapper、route group wrapper 写法由 analyzer 内置识别；业务方不需要维护语法配置。
提供 `--diff` 时，`impact` 要求 diff 已应用到 `--project` 对应的变更后源码；旧快照、空 diff、越界路径或变更文件语法错误会直接失败。`--diff` 与 `--grpc` 至少提供一个。
`--impact-config` 是可选配置；未传时会自动尝试读取项目内 `.analyzer/go-impact.config.json`，文件不存在则使用默认行为。
当前配置只用于 module 版本变更过滤，不开放 route、annotation、middleware 等业务语法配置；配置文件会严格校验字段，未知字段或旧 schema 会直接失败：

```json
{
  "analyzeModuleChanges": true,
  "ignoredModuleChanges": [
    "gopkg.inshopline.com/sc1/app/modules/*/proto"
  ]
}
```

impact 输出的顶层 `summary` 汇总影响接口和 IM event；每个 `fileSources[]` 分别保留普通文件变更的原始 `diff`，其 `symbols` 承载完整传播树；可选的 `moduleSources[].sourceFiles[].symbols` 承载 go.mod 模块语义变化从本仓使用入口开始的完整传播树。`impactedIMEvents` 只保留可静态确定的 event 字符串，不输出 appId、mode 或 payload 字段。输出契约见 `docs/contracts/output-contract.md`。

关键设计点是：Go BFF 不能只做调用图分析。route 注册里 controller 通常不是被调用，而是作为函数值被传给注册函数：

```go
broadcastGroup.GET(
  "/record",
  sa2.ControllerWithReqResp(broadcast.BroadcastAdminApi.QueryBroadcastRecord),
)
```

所以核心图应该是“反向引用图”，而不是单纯的 call graph：

```text
被引用节点 -> 引用它的节点或代码位置
```

这样 service 变更可以追到 controller，controller 又可以追到 route 注册位置。

## HTTP 接口出口

MVP 综合 controller 注释与 route 解析结果确定 HTTP 接口：

```go
// @Get /admin/api/bff-web/mc/broadcast/record
func (api *adminBroadcastApi) QueryBroadcastRecord(...) (...) {}
```

这样做是为了避免第一版强行通过 AST 拼接所有 route path。BFF 项目里 route 前缀可能来自常量、helper 参数、inline 字符串、guard wrapper、derived group，完整拼接容易出现“看似精确但实际错误”的结果。

因此当前策略是：

- 可完整解析前缀或明确属于兼容旧路径的 route 提供最终 method/path。
- route 只有局部路径而 annotation 补足父前缀时使用 annotation。
- route AST 负责证明 handler 被注册。
- route AST 负责传播 route group、middleware、wrapper 变更造成的影响。
- annotation 缺失时使用 route method/path fallback。

## 重要场景

普通业务逻辑变更：

```text
changed service method
  -> controller method
  -> route registration site
  -> controller endpoint annotation
  -> HTTP endpoint
```

controller 变更：

```text
changed controller method
  -> route registration site
  -> controller endpoint annotation
  -> HTTP endpoint
```

route group 前缀变更：

```text
changed group := g.Group("/merchant")
  -> route group context
  -> handlers registered under group
  -> handler endpoint annotations
```

中间件挂载关系变更：

```text
changed group.Use(middleware)
  -> middleware binding
  -> later handlers under route group
  -> handler endpoint annotations
```

中间件函数内部变更：

```text
changed middleware symbol
  -> middleware binding site
  -> affected route group handlers
  -> handler endpoint annotations
```

IM payload 变更：

```text
changed payload type / converter
  -> local sender function
  -> IM transport sink
  -> concrete IM event
```

IM 识别规则由 analyzer 内置。协议型实现必须同时存在 `broadcast://` 和
`/broadcast/send` 锚点；常用 SDK 使用精确 import path 和函数签名适配。动态 event
会作为 `im_event_unresolved` 保留在传播树中，但不会计入 `impactedIMCount`，避免误报。

## 文档

当前项目架构、模块职责、调试与接手指南见：

[ARCHITECTURE.md](ARCHITECTURE.md)

输出契约见：

[docs/contracts/output-contract.md](docs/contracts/output-contract.md)

gRPC 依赖能力的设计和实施记录见：

[docs/bff-grpc-dependency-assets/](docs/bff-grpc-dependency-assets/)

`docs/design/`、`docs/superpowers/specs/` 和 `docs/superpowers/plans/` 保存历史设计与实施过程，不作为当前实现状态真值。

## 后续路线

第一阶段先验证 BFF diff 到 HTTP 接口的静态分析闭环。等这个闭环稳定后，再考虑：

- 外部 Go module 两个版本之间的 API/source diff 分析。
- Base/Head 双快照与被删除声明的精确恢复。
- Nexus/codegen 模板变体的持续回归覆盖。
- 底层 gRPC 项目到 BFF HTTP 接口的跨仓传播。
- 与前端 analyzer 的 API 输入自动桥接。
- 面向 QA 的自然语言回归报告。
