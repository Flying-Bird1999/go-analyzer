# Go BFF 影响范围分析技术方案

## 1. 背景

当前前端 analyzer 已经验证了一条有效路径：

```text
diff -> 变更语义节点 -> 依赖传播 -> 业务入口
```

在 React + TypeScript 项目中，业务入口通常是页面、组件、API 调用点或手动指定 source。对于 Go BFF 项目，第一阶段可以把目标收敛得更清晰：

```text
Go BFF diff -> 受影响 HTTP 接口
```

第一批目标项目是：

- `/Users/bird/Desktop/agent-factory/projects/sc1-admin-bff`
- `/Users/bird/Desktop/agent-factory/projects/sc1-bff-service`

这两个项目都由前端团队维护，使用 `lego.RouterGroup` 这一类 Gin-like 路由抽象，整体分层接近：

```text
router -> controller -> service -> remote
```

MVP 不分析底层 gRPC 项目，也不直接调用前端 analyzer。前后端后续可以通过 HTTP 接口天然打通，因此本项目第一阶段只关注 Go BFF 自身的依赖影响分析。

## 2. MVP 目标

构建一个独立 Go analyzer，回答：

```text
这次 Go BFF diff 影响了哪些 HTTP 接口？
```

MVP 需要覆盖：

- Go 源码 diff 分析。
- `go.mod` 依赖变更分析。
- Go 变更声明识别。
- Go 符号反向引用传播。
- route 注册、route group、中间件、wrapper 影响传播。
- controller 注释中的 HTTP 接口识别。
- 最终受影响 HTTP 接口发现。

MVP 暂不覆盖：

- 前端页面影响范围。
- 跨仓 gRPC 传播。
- 运行时 route table 抽取。
- AI 报告生成。
- 完整动态分发精度。

## 3. 核心架构

整体管线：

```text
diff
  -> change detector
  -> Go semantic index
  -> reverse reference graph
  -> route domain graph
  -> impact propagation
  -> impacted HTTP endpoints
```

每一层都应该职责单一，并且产物可单独检查。这样后续即使要接 gRPC、前端 analyzer 或自然语言报告，也不会污染基础分析链路。

## 4. 目标项目画像

### 4.1 `sc1-admin-bff`

已观察到的特点：

- module 名为 `sc1-admin-bff`。
- 主路由入口是 `router.InitRouter`。
- 路由模块分布在大量 `router/*` package 中。
- 常见前缀通过代码常量声明，例如 `WEB_BFF_PREFIX = "/admin/api/bff-web"` 和 `APP_BFF_PREFIX = "/admin/api/bff-app"`。
- route group 经常通过 `createAdminAuthGroup` 这类 helper 创建。
- handler 常被 `sa2.ControllerWithReqResp`、`sa2.ControllerWithResp`、`lego.MiddlewareController`、guard helper、本地 util wrapper 包裹。
- controller 方法经常带有 `// @Get /admin/api/bff-web/...` 这类接口注释。
- 同时存在旧路径兼容路由。
- `nexus/codegen/apis.RegisterRouters(g)` 会注册生成路由，需要作为单独 route source family 处理。

### 4.2 `sc1-bff-service`

已观察到的特点：

- module 名为 `sc1-client-bff-service`。
- 主路由入口是 `router.InitRouter`。
- 路由树规模更小，但同样是 `router -> controller -> service -> remote` 分层。
- 前缀常直接写在业务 router 中，例如 `/api/bff-web/...` 和 `/sc1-internal/app-proxy/api`。
- 使用 `standard-api/v2` wrapper 和 app-proxy 中间件模式。
- 中间件可能通过对象方法绑定，例如 `AppProxyAuthOptionalLogin.Middleware()`。
- 同样包含 `nexus/codegen/apis.RegisterRouters(g)`。

### 4.3 设计结论

Analyzer 不能写死某一个项目的路由常量或目录结构。它应该具备：

- 项目级默认配置。
- 可配置 route entry function。
- 可配置 handler wrapper。
- 可配置 route wrapper / guard wrapper。
- 可配置 generated route 处理方式。
- 能同时解析常量前缀和 inline 前缀的 route context 引擎。

## 5. 项目模型

### 5.1 Go 语义索引

基础能力应该建立在 Go 官方分析栈上：

- 使用 `go/packages` 加载 package、syntax、types、imports、module 上下文。
- 使用 `go/types` 解析 identifier、selector、receiver、method、field、package-level object。
- 使用 AST 遍历 route 注册和 wrapper 模式。
- 使用注释遍历识别 controller endpoint annotation。
- SSA 可以放到 MVP 后，用于更精准的函数值和 interface 传播。

语义索引需要记录：

- package path 和 package name。
- 文件声明。
- 函数和方法声明。
- receiver 类型。
- type 声明、struct field、interface method。
- package-level var / const。
- import alias。
- identifier / selector 到 `types.Object` 的解析结果。
- controller endpoint annotation，例如 `@Get`、`@Post`、`@Put`、`@Delete`、`@Patch`。

尽量使用 Go 语义对象身份，而不是字符串匹配。

### 5.2 变更节点识别

Diff 不应该只停留在 changed files，而应该映射到 Go 语义节点。

变更节点包括：

- `go.mod` module dependency change。
- 函数声明。
- 方法声明。
- type 声明。
- struct field。
- interface method。
- package-level var / const。
- route registration statement。
- route group creation statement。
- middleware binding statement。

示例：

```go
func (s *AdminBroadcastService) QueryBroadcastRecord(...) {}
```

应该识别为 changed method node。

```go
group := g.Group("/merchant")
```

如果 diff 命中 `Group` 表达式、prefix 参数或赋值目标，应该识别为 changed route group context node。

```go
group.Use(AuthMiddleware())
```

如果 diff 命中 `Use` 表达式或 middleware 参数，应该识别为 changed middleware binding node。

## 6. 依赖模型

这个项目不应该只是 call graph analyzer。

纯 call graph 不够，因为 HTTP route 注册通常不是调用 controller，而是把 controller 作为函数值引用：

```go
broadcastGroup.GET(
  "/record",
  sa2.ControllerWithReqResp(broadcast.BroadcastAdminApi.QueryBroadcastRecord),
)
```

这里 controller 没有被调用，但它被 route registration site 引用。对影响分析来说，这仍然是一条依赖边。

因此核心图应该是 reverse reference graph。

### 6.1 反向引用图

图方向是：

```text
被引用节点 -> 引用它的节点或代码位置
```

需要包含：

- 函数调用。
- 方法调用。
- 函数值作为参数传递。
- selector 引用。
- 变量引用。
- 类型引用。
- struct field 引用。
- interface method implementation link，前提是能可靠解析。
- route registration site。
- middleware binding site。

示例：

```text
service.AdminBroadcastService.QueryBroadcastRecord
  -> controller.BroadcastAdminApi.QueryBroadcastRecord
  -> route registration site
```

route registration site 不是 Go 函数，但它是引用 handler 的代码位置，因此应该作为图节点。

### 6.2 Route 领域图

Routing 有自己的领域语义，不能全部塞进通用引用图。

Route 领域图需要建模：

- route root。
- route group。
- route group prefix。
- middleware binding。
- route registration site。
- handler endpoint annotation。
- annotation 中的 HTTP endpoint。

示例：

```text
RouteRoot(g)
  -> RouteGroup(adminWebGroup, "/admin/api/bff-web")
  -> RouteGroup(broadcastGroup, "/mc/broadcast")
  -> RouteRegistrationSite(GET, "/record", handler)
  -> HandlerAnnotation(GET, "/admin/api/bff-web/mc/broadcast/record")
```

Route 领域图负责解释 route context 如何从 parent group 流到 child group，再流到 route registration site。MVP 中最终 HTTP endpoint 优先来自 handler annotation，因为 route path 拼接可能包含复杂前缀、helper 和 wrapper 组合。

## 7. Route 与 Endpoint 分析

### 7.1 Endpoint 来源策略

MVP 优先使用 controller 注释作为 HTTP endpoint 出口：

```go
// @Get /admin/api/bff-web/mc/broadcast/record
func (api *adminBroadcastApi) QueryBroadcastRecord(...) (...) {}
```

原因：

- route path composition 可能很复杂。
- prefix 可能来自常量、helper 参数、inline 字符串或 wrapper-derived group。
- 强制把 AST path 拼接作为唯一出口，容易产生“看起来精确但实际错误”的结果。
- controller annotation 离 handler 最近，通常表达的是稳定 API contract。

因此 MVP 应该：

- 解析 controller function / method 上的 endpoint annotation。
- 使用 annotation endpoint 作为最终 HTTP endpoint。
- 使用 route AST 验证 handler 已被注册。
- 使用 route AST 传播 route group、中间件、wrapper 变更对 handler 的影响。
- route-derived path 只在能可靠解析时作为辅助证据。
- annotation 缺失或疑似过期时进入 diagnostics。

### 7.2 支持的 Route 模式

目标项目中存在这类模式：

```go
adminWebGroup := createAdminAuthGroup(g, WEB_BFF_PREFIX)
broadcastGroup := adminWebGroup.Group("/mc/broadcast")
broadcastGroup.GET("/record", sa2.ControllerWithReqResp(broadcast.BroadcastAdminApi.QueryBroadcastRecord))
```

Analyzer 需要支持：

- `g.Group("/prefix")`
- `group.Group("/prefix")`
- `group.GET/POST/PUT/DELETE/PATCH("/path", handler)`
- 常量路径前缀，例如 `WEB_BFF_PREFIX`
- handler wrapper，例如 `sa2.ControllerWithReqResp(handler)`
- group guard wrapper，例如 `AddLiveReadGuard(group).GET(...)`
- `group.Use(...)` 中间件绑定
- `Group(...)` 中传入中间件
- middleware object method，例如 `auth.OptionalLogin.Middleware()`
- `apis.RegisterRouters(g)` 这类 generated route registration，作为单独 route source family

### 7.3 Route Group Context

route group 变量携带 route context：

```text
route group = parent group + path prefix + middleware stack + statement order
```

例如：

```go
group := g.Group("/merchant")
group.GET("/setting/:code", handler)
group.POST("/settings", handler)
```

Analyzer 应该建模为：

```text
group prefix "/merchant"
  -> route registration for handler A
  -> route registration for handler B
```

如果 diff 改了：

```go
group := g.Group("/merchant/v2")
```

通过 `group` 注册的所有 handler 都受影响。每个 handler 最终对应的 HTTP endpoint 优先从 handler annotation 读取。

这需要在 router function 内做局部数据流分析，跟踪 route group 变量、派生 group 和 statement order。

### 7.4 Middleware Binding Impact

中间件通过 route context 影响接口。

场景一：挂载关系变化。

```go
group.Use(AuthMiddleware())
group.GET("/a", handler)
```

如果 diff 改了 `Use` 语句，应该标记这个 binding 之后注册的 handler 受影响。

场景二：中间件函数内部逻辑变化。

```go
func AuthMiddleware() lego.MiddlewareFunc {
  return func(ctx *lego.Context) {
    ...
  }
}
```

如果 diff 改了 `AuthMiddleware`，反向引用图应该找到：

```text
AuthMiddleware symbol
  -> group.Use(AuthMiddleware()) binding site
  -> route group
  -> affected handlers
  -> handler annotations
```

中间件同时有两个身份：

- Go symbol，在 reverse reference graph 中传播。
- route middleware binding，在 route domain graph 中影响 handler 集合。

### 7.5 Middleware Order

语句顺序很重要：

```go
group.GET("/a", h1)
group.Use(m)
group.GET("/b", h2)
```

这个 middleware binding 应该影响 `h2`，不影响 `h1`。最终 endpoint 从受影响 handler 的 annotation 读取。

MVP 应支持单个 router function 内的 statement-order propagation。更复杂的跨函数顺序关系可以降级为 diagnostics。

### 7.6 Route Wrapper Function

一些路由 helper 会返回派生 group：

```go
func AddLiveReadGuard(g *lego.RouterGroup) *lego.RouterGroup {
  group := g.Group("")
  group.Use(...)
  return group
}

AddLiveReadGuard(g).GET("/statistics", handler)
```

Analyzer 应该生成 route wrapper summary：

```text
AddLiveReadGuard(input group) -> derived group with middleware bindings
```

如果 wrapper 实现发生变化，通过这个 wrapper 注册的所有 handler 都应该受影响：

```text
changed AddLiveReadGuard
  -> wrapper call sites
  -> derived route groups
  -> registered handlers
  -> handler annotations
```

MVP 支持这类 wrapper：

- 入参包含 `*lego.RouterGroup`。
- 创建或返回 `*lego.RouterGroup`。
- 内部调用 `Group` 或 `Use`。
- 在 route registration chain 中被直接调用。

复杂 wrapper 进入 diagnostics。

## 8. `go.mod` 依赖变更分析

依赖变更应该作为一等影响源。

当 `go.mod` 变化时，Analyzer 需要识别：

```text
module path
old version
new version
change kind: added | removed | upgraded | downgraded | replaced
```

MVP 不尝试分析外部 module 版本间 diff，而是把 changed module 映射到本项目 import 使用点。

对每个 changed module，查找本项目中 import 该 module 或其 subpackage 的文件：

```text
changed module gopkg.inshopline.com/sc1/commons/utils
  -> local file imports gopkg.inshopline.com/sc1/commons/utils/common
  -> local declarations referencing imported symbols
  -> reverse reference graph
  -> registered handlers
  -> handler annotations
```

如果能解析到 symbol-level import usage，就从直接引用该依赖的本地 declaration 开始传播。

如果 symbol-level usage 不够精确，就兜底到 import 该 module 的文件内所有本地 declaration，再传播到 handler 和 endpoint。

依赖影响依据需要明确标识：

```text
module_reference_precise
module_reference_file_fallback
module_unreferenced
module_diff_unavailable
```

未来可以支持外部 module diff：

```text
changed external module version
  -> external changed exported symbols
  -> local imported symbols that match changed exports
  -> local reverse propagation
  -> HTTP endpoints
```

这类似前端二方包分析，但应该作为后续阶段。MVP 在没有外部仓库权限的情况下也要可用。

## 9. 影响传播

传播从 changed node 开始，最终走向 HTTP endpoint。

### 9.1 业务逻辑变更

```text
changed service method
  -> controller method referencing it
  -> route registration site referencing controller
  -> controller endpoint annotation
  -> HTTP endpoint
```

### 9.2 Controller 变更

```text
changed controller method
  -> route registration site referencing controller
  -> controller endpoint annotation
  -> HTTP endpoint
```

Controller method 不是终点。它只是普通 Go symbol，只是恰好被 route registration site 引用。

### 9.3 Route Prefix 变更

```text
changed Group("/merchant")
  -> route group context
  -> handlers registered under group
  -> handler endpoint annotations
```

### 9.4 Route Registration 变更

```text
changed group.GET("/path", handler)
  -> route registration site
  -> handler endpoint annotation
```

这覆盖 method、path、handler 替换。

### 9.5 Middleware Binding 变更

```text
changed group.Use(m)
  -> middleware binding
  -> later handlers under the route group
  -> handler endpoint annotations
```

### 9.6 Middleware Function 变更

```text
changed middleware symbol
  -> middleware binding site
  -> affected route group handlers
  -> handler endpoint annotations
```

### 9.7 Shared Utility 变更

```text
changed util function
  -> service/controller callers
  -> route registration sites
  -> handler endpoint annotations
```

Shared utility 可能影响大量接口，Analyzer 需要压缩和去重证据链。

### 9.8 Dependency 变更

```text
changed go.mod module
  -> local import usage
  -> local declaration using the dependency
  -> reverse reference graph
  -> registered handler
  -> handler endpoint annotation
```

Dependency change propagation 需要明确标识，因为真实行为变化来自本仓外部。

## 10. 精度与降级

Analyzer 应优先输出有明确证据的结果。

MVP 可稳定支持：

- 直接函数调用和方法调用。
- selector reference。
- handler function value 通过常见 wrapper 传递。
- router function 内的 route group 变量。
- route group prefix 变更。
- route registration 变更。
- middleware binding 变更。
- 被 `Use` 或 `Group` 引用的 middleware function 变更。
- 简单 route wrapper summary。
- controller endpoint annotation。
- `go.mod` changed module 到本地 import usage。

需要 diagnostics 降级：

- 反射式路由。
- 运行时计算 path string。
- 缺失或疑似过期的 controller endpoint annotation。
- 通过复杂循环构造 middleware list。
- 函数值先存 map 再注册。
- 多实现 interface dispatch。
- 跨 module 或 generated gRPC propagation。
- 没有本地 module source 的外部依赖版本 diff。
- 当前 package config 未加载的 build tags 或平台特定文件。

降级结果也应该有用：

```text
changed node found, but no confident path to HTTP endpoint
```

或：

```text
route group changed, but some affected handlers are missing endpoint annotations
```

## 11. 模块边界

建议项目模块：

```text
cmd/go-bff-impact
  CLI 入口。

internal/diff
  解析 git diff，映射 changed line ranges。

internal/goindex
  加载 packages，构建语义索引。

internal/change
  将 changed ranges 映射为 Go declaration、route statement、go.mod dependency change。

internal/modimpact
  将 go.mod dependency change 解析到本地 import usage 和 fallback source。

internal/refgraph
  构建 reverse reference graph。

internal/route
  构建 route domain graph、wrapper summary、handler registration site。

internal/endpoint
  解析 controller endpoint annotation，将 handler 映射为 HTTP endpoint。

internal/impact
  将 source、route、middleware、module changed node 传播到 HTTP endpoint。

internal/report
  渲染机器可读和人类可读输出。
```

route 特定逻辑不要污染通用 Go reference graph。未来做 gRPC 影响分析时，可以复用 `diff`、`goindex`、`change`、`modimpact`、`refgraph`，再新增 gRPC domain graph。

## 12. 测试策略

测试应以 fixture 驱动。每个 fixture 是一个小型 Go module，只覆盖一个清晰场景。

MVP 必备 fixture：

- 直接 handler 注册。
- handler 被 `ControllerWithReqResp` 包裹。
- service method 变更传播到 endpoint。
- controller method 变更传播到 endpoint。
- route group prefix 变更影响 group 下所有 handler。
- route registration path 变更影响对应 handler。
- `group.Use` 只影响后续 handler。
- middleware function 变更影响已绑定 endpoint。
- `AddLiveReadGuard` 这类 route wrapper。
- utility function fan-out 到多个 endpoint。
- 从 controller annotation 识别 endpoint。
- route-derived path 与 controller annotation 不一致时输出 diagnostic。
- `go.mod` dependency upgrade 精准 import usage。
- `go.mod` dependency upgrade 文件级 fallback。
- `go.mod` dependency change 未被本地 import。
- 动态 route path 无法解析 diagnostic。

单元 fixture 稳定后，再使用真实 `sc1-admin-bff` 和 `sc1-bff-service` 做集成验证。

## 13. MVP 交付阶段

### Phase 1: Endpoint Annotation Inventory

解析 controller functions / methods，输出 annotation 中声明的 HTTP endpoints 及 handler symbols。

成功标准：

- 支持 `@Get`、`@Post`、`@Put`、`@Delete`、`@Patch`。
- 支持 `(*adminBroadcastApi).QueryBroadcastRecord` 这类 receiver method。
- 已被 route 注册但缺少 endpoint annotation 的 handler 能进入 diagnostics。

### Phase 2: Route Registration Inventory

解析 `sc1-admin-bff` 和 `sc1-bff-service`，输出静态发现的 route registrations 和 handler symbols。

成功标准：

- 不依赖完整 path reconstruction 也能发挥作用。
- 支持 `sa2.ControllerWithReqResp` 等常见 wrapper。
- 支持 route group 变量和常见 guard chain。
- 使用 controller annotation 作为最终 endpoint path。

### Phase 3: Changed Symbol Detection

将 diff ranges 映射到 Go declaration 和 route statement。

成功标准：

- 能识别 function、method、type、route group、route registration、middleware binding、`go.mod` dependency change。

### Phase 4: Reverse Reference Propagation

构建 reference graph，将普通 Go symbol 变更传播到 route registration site。

成功标准：

- service、controller、util 变更能到达受影响 HTTP endpoint。

### Phase 5: Route Context Propagation

支持 route group prefix、middleware binding、wrapper function 变更。

成功标准：

- route 层间接影响不依赖 handler 自身变更也能被表达。

### Phase 6: Dependency Change Propagation

支持 `go.mod` dependency change 作为影响源。

成功标准：

- 能识别 changed modules。
- 能找到 changed modules 的本地 imports。
- 能将引用该依赖的本地 declarations 传播到 endpoint。
- 文件级 fallback、unreferenced module 有清晰输出。

### Phase 7: Real Project Validation

基于 `sc1-admin-bff` 和 `sc1-bff-service` 真实 diff 验证。

成功标准：

- 结果能通过证据链解释。
- 不支持的模式有 diagnostics，不静默漏掉。

## 14. 待确认问题

1. `sc1-admin-bff` MVP 必须支持哪些 route wrappers？除了 `AddLiveReadGuard`、`AddLiveWriteGuard`、常见 `guard.*` helper 外还需要继续盘点。
2. 旧路径兼容 route 是否需要和新 BFF route 合并展示，还是按 route family 分开？
3. `nexus/codegen/apis.RegisterRouters(g)` MVP 是忽略，还是作为外部 route inventory source？
4. 配置文件变更是作为 unresolved global impact，还是只有映射到 route / middleware 行为时才报告？
5. 真实 BFF 项目分析的可接受运行时上限是多少？
6. `go.mod` dependency change 如果只被 generated code import，是否报告？
7. MVP 是否强制要求所有 route handler 都有 controller endpoint annotation，还是允许 route AST reconstruction 作为缺失 annotation 的 fallback？

## 15. 推荐定位

第一版应该严格、可解释：

```text
Only report endpoints reached through confident static evidence.
Surface everything else as diagnostics.
```

核心原则是先可信，再扩展。等 endpoint annotation inventory、route registration inventory 和 symbol propagation 稳定后，再逐步补充更多启发式规则、跨仓 gRPC linkage、前端 analyzer 自动桥接和 QA 友好报告。
