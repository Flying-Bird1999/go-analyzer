# go-analyzer

`go-analyzer` 是面向 Go BFF 项目的影响范围分析工具项目。它服务于前端团队维护的 BFF 代码仓，目标是把一次 Go MR 的 diff 转换成“受影响的 HTTP 接口列表”，帮助测试、开发和后续自动化流程判断本次服务端改动需要重点回归哪些接口。

当前项目先沉淀技术方案，不急于进入编码。第一阶段重点验证分析架构是否成立。

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
- `go.mod` 依赖变更分析。
- diff 到 Go 语义节点的映射。
- 函数、方法、变量、类型引用的反向传播。
- controller 函数注释中的 HTTP 接口识别。
- route 注册关系、route group、中间件、guard/wrapper 的影响传播。
- 输出受影响 HTTP 接口及证据链。

MVP 暂不覆盖：

- 前端页面影响范围分析。
- 底层 gRPC 项目的跨仓传播。
- 运行时 route table 抽取。
- AI 报告生成。
- 所有动态分发、反射和复杂 DI 场景的精确分析。

## 目标项目

第一批分析对象是前端团队维护的两个 Go BFF：

- `../sc1-admin-bff`
- `../sc1-bff-service`

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

`go.mod` 依赖变更：

```text
changed module
  -> local import usage
  -> local declaration using dependency
  -> reverse reference graph
  -> registered handler
  -> handler endpoint annotation
```

## 文档

最终架构技术方案见：

[docs/design/go-analyzer-mvp-architecture.md](docs/design/go-analyzer-mvp-architecture.md)

模块级开发计划见：

[docs/superpowers/plans](docs/superpowers/plans)

## 后续路线

第一阶段先验证 BFF diff 到 HTTP 接口的静态分析闭环。等这个闭环稳定后，再考虑：

- 更精准的外部 Go module diff 分析。
- 生成代码和 `nexus/codegen` 路由的专项支持。
- 底层 gRPC 项目到 BFF HTTP 接口的跨仓传播。
- 与前端 analyzer 的 API 输入自动桥接。
- 面向 QA 的自然语言回归报告。
