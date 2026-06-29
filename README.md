# go-analyzer

`go-analyzer` 是面向 Go BFF 项目的影响范围分析工具项目。它服务于前端团队维护的 BFF 代码仓，目标是把一次 Go MR 的 diff 转换成“受影响的 HTTP 接口列表”，帮助测试、开发和后续自动化流程判断本次服务端改动需要重点回归哪些接口。

当前已经完成基于变更后项目的符号级影响分析闭环，并输出按 diff 来源组织的紧凑影响图和接口摘要。

## 背景

我们已经在前端 `React + TypeScript` 项目里验证了影响范围分析模型：

```text
diff -> 变更语义节点 -> 依赖传播 -> 业务入口
```

在前端项目里，业务入口可能是页面、组件、API 调用点或手动指定 source。Go BFF 项目的入口更明确：最终需要回归的是 HTTP 接口。因此 Go 侧可以先独立完成：

```text
Go BFF diff -> 受影响 HTTP 接口
```

后续如果要和前端影响分析打通，只需要把 Go analyzer 输出的 HTTP 接口作为前端 analyzer 的 `--api` 输入即可。这个桥接可以后置，MVP 不需要承担前端页面分析。

## MVP 目标

第一版只回答一个问题：

```text
这次 Go BFF diff 影响了哪些 HTTP 接口？
```

MVP 覆盖范围：

- Go 源码 diff 分析。
- diff 到 Go 语义节点的映射。
- 函数、方法、变量、常量、类型引用的反向传播。
- struct 字段和 tag 变更映射到所属 type，并沿类型依赖传播。
- controller 函数注释中的 HTTP 接口识别。
- route 注册关系、route group、中间件、guard/wrapper 的影响传播。
- 删除 route registration 的单行/多行恢复。
- go.mod require/replace 变更到本仓使用点再到 endpoint 的传播。
- 常见 package var / struct field middleware selector 的轻量类型推断。
- 输出去重后的 symbol → route → annotation 传播图及接口摘要。

MVP 暂不覆盖：

- 前端页面影响范围分析。
- 底层 gRPC 项目的跨仓传播。
- 运行时 route table 抽取。
- AI 报告生成。
- 所有动态分发、反射和复杂 DI 场景的精确分析。

## 目标项目

第一批分析对象是前端团队维护的两个 Go BFF：

- `sc1-admin-bff`
- `sc1-bff-service`

两个项目都大致遵循：

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
  -> route 领域图
  -> 影响传播
  -> 受影响 HTTP 接口
```

## CLI 使用

CLI 边界要求输入路径使用绝对路径：

```bash
go-analyzer facts --project /absolute/path/to/sc1-bff-service --format json
go-analyzer impact --project /absolute/path/to/sc1-bff-service --diff /absolute/path/to/change.diff --format json
go-analyzer schema --type facts
go-analyzer schema --type impact
```

lego BFF 的 route、annotation、handler wrapper、route group wrapper 写法由 analyzer 内置识别；业务方不需要维护语法配置。

impact 输出的顶层 `summary` 汇总影响接口数量和接口列表；`fileSources` 承载普通文件逻辑变更及原始 diff，`moduleSources` 承载 go.mod 模块升级及其本仓使用入口，`nodes` 保存去重后的共享传播图。输出契约见 `docs/contracts/output-contract.md`。

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

MVP 优先使用 controller 函数注释作为 HTTP 接口出口：

```go
// @Get /admin/api/bff-web/mc/broadcast/record
func (api *adminBroadcastApi) QueryBroadcastRecord(...) (...) {}
```

这样做是为了避免第一版强行通过 AST 拼接所有 route path。BFF 项目里 route 前缀可能来自常量、helper 参数、inline 字符串、guard wrapper、derived group，完整拼接容易出现“看似精确但实际错误”的结果。

因此第一版策略是：

- controller 注释提供最终 HTTP method/path。
- route AST 负责证明 handler 被注册。
- route AST 负责传播 route group、middleware、wrapper 变更造成的影响。
- route path 拼接只作为辅助证据，不作为唯一出口。
- 缺失或疑似过期的 controller 注释进入 diagnostics。

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

## 文档

当前项目架构、模块职责、调试与接手指南见：

[ARCHITECTURE.md](ARCHITECTURE.md)

输出契约见：

[docs/contracts/output-contract.md](docs/contracts/output-contract.md)

`docs/design/` 和 `docs/superpowers/plans/` 保存历史设计与实施过程，不作为当前实现状态真值。

## 后续路线

第一阶段先验证 BFF diff 到 HTTP 接口的静态分析闭环。等这个闭环稳定后，再考虑：

- 外部 Go module 两个版本之间的 API/source diff 分析。
- Base/Head 双快照与被删除声明的精确恢复。
- 生成代码和 `nexus/codegen` 路由的专项支持。
- 底层 gRPC 项目到 BFF HTTP 接口的跨仓传播。
- 与前端 analyzer 的 API 输入自动桥接。
- 面向 QA 的自然语言回归报告。
