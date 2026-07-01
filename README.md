# go-analyzer

`go-analyzer` 是面向 Go BFF 项目的影响范围分析工具项目。它服务于前端团队维护的 BFF 代码仓，目标是把一次 Go MR 的 diff 转换成“受影响的 HTTP 接口和出站 IM event 列表”，帮助测试、开发和后续自动化流程判断本次服务端改动需要重点回归哪些业务入口。

当前已经完成基于变更后项目的符号级影响分析闭环，并输出按 diff 来源组织的原始传播树、接口摘要和 IM event 摘要。

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
- 底层 gRPC 项目的跨仓传播。
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

## CLI 使用

对外接入只需要使用 `impact` 命令。CLI help 使用中文描述，命令名和参数名保持英文，便于脚本集成。CLI 边界要求输入路径使用绝对路径：

```bash
go-analyzer impact --project /absolute/path/to/sl-sc1-bff-service --diff /absolute/path/to/change.diff --format json
go-analyzer impact --project /absolute/path/to/sl-sc1-bff-service --diff /absolute/path/to/change.diff --impact-config /absolute/path/to/go-impact.config.json --format json
```

lego BFF 的 route、annotation、handler wrapper、route group wrapper 写法由 analyzer 内置识别；业务方不需要维护语法配置。
`impact` 要求 diff 已应用到 `--project` 对应的变更后源码；旧快照、空 diff、越界路径或变更文件语法错误会直接失败。
`--impact-config` 是可选配置；未传时会自动尝试读取项目内 `.analyzer/go-impact.config.json`，文件不存在则使用默认行为。
当前配置只用于 module 版本变更过滤，不开放 route、annotation、middleware 等业务语法配置：

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

`docs/design/`、`docs/superpowers/specs/` 和 `docs/superpowers/plans/` 保存历史设计与实施过程，不作为当前实现状态真值。

## 后续路线

第一阶段先验证 BFF diff 到 HTTP 接口的静态分析闭环。等这个闭环稳定后，再考虑：

- 外部 Go module 两个版本之间的 API/source diff 分析。
- Base/Head 双快照与被删除声明的精确恢复。
- Nexus/codegen 模板变体的持续回归覆盖。
- 底层 gRPC 项目到 BFF HTTP 接口的跨仓传播。
- 与前端 analyzer 的 API 输入自动桥接。
- 面向 QA 的自然语言回归报告。
