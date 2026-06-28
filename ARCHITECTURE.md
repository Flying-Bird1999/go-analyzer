# go-analyzer 架构与开发指南

> 文档状态：当前实现基线，更新于 2026-06-27。
> 适用读者：项目维护者、代码评审者、首次接手的开发者，以及需要继续迭代本项目的 agent。
> 当前输出协议：`go-impact/v1alpha1`。

## 1. 项目定位

`go-analyzer` 是面向 Go BFF 项目的静态影响范围分析工具。它读取“变更后的项目源码”和一份 unified diff，将 diff 映射到 Go 语义符号，再沿项目内依赖反向传播，最终输出受影响的 HTTP endpoint 及完整传播路径。

核心问题只有一个：

```text
这次 Go BFF 变更，最终会影响哪些 HTTP 接口？
```

当前分析模型是：

```text
diff
  -> changed symbol / route / middleware / annotation
  -> project-local reverse references
  -> route registration
  -> controller annotation
  -> HTTP endpoint
```

项目刻意不分析“函数内部具体改了什么”，也不输出字段级变化描述。diff 的作用是定位最小完整语义根；影响分析从这个语义根向外扩散。

例如 struct 字段、字段类型或 tag 的变更统一归属于该 struct 的 `type` symbol：

```text
Address.City json tag changed
  -> type Address
  -> type CreateOrderRequest references Address
  -> controller OrderAPI.Create references CreateOrderRequest
  -> route registration
  -> POST /orders
```

对应端到端测试位于 `internal/app/pipeline_test.go` 的 `TestRunImpactMapsStructChangeToEndpointTree`。

## 2. 阅读路线

### 2.1 首次接手

建议依次阅读：

1. 本文第 3、4 节：建立系统和目录地图。
2. 第 6、7 节：理解 facts 与 impact 两条主流程。
3. 第 8、9、10 节：理解 diff、route、传播算法。
4. 第 13、14 节：运行命令并调试 fixture/真实项目。
5. 第 16 节：确认当前能力边界，避免错误假设。

### 2.2 代码评审

优先检查：

1. `internal/facts` 的数据契约是否变化。
2. extractor 是否只提取事实，没有混入传播逻辑。
3. 新关系是否进入 reverse graph 或 route graph。
4. 新 change kind 是否能在 `internal/impact` 中形成可解释路径。
5. JSON shape 是否同步 `internal/output/contract.go` 和 `docs/contracts/output-contract.md`。
6. 是否有最小 fixture、端到端测试和 smoke 场景。

### 2.3 排查“为什么没有命中接口”

按以下顺序定位：

1. `facts` 输出中是否存在目标 symbol。
2. 是否提取到 symbol reference。
3. route 是否存在、handler 是否解析为正确 symbol。
4. handler 是否存在 annotation。
5. diff 是否映射到预期 change root。
6. impact tree 在哪个节点终止，diagnostics 给出了什么原因。

## 3. 系统上下文与边界

```mermaid
flowchart LR
    MR["MR unified diff"] --> Analyzer["go-analyzer"]
    Head["变更后 Go BFF 源码"] --> Analyzer
    Config["可选 JSON 配置"] --> Analyzer
    Analyzer --> Facts["facts JSON<br/>提取与调试"]
    Analyzer --> Impact["go-impact/v1alpha1<br/>原始影响树"]
    Impact --> Human["开发 / QA 人工 review"]
    Impact --> Agent["后续 skill / agent 消费"]
```

当前输入：

- 变更后的单份 Go module 源码。
- 一份标准 unified diff。
- 可选 analyzer JSON 配置。

当前输出：

- `facts`：完整事实库，用于 extractor/linker 调试。
- `impact`：按 diff 源文件组织的原始影响树和 endpoint 摘要。
- `schema`：facts/impact 的 JSON Schema。

当前不负责：

- 生成面向 QA 的自然语言报告。
- 分析前端页面。
- 还原运行时 route table。
- 下载并比较依赖包两个版本的源码/API。
- base/head 双快照对比。

## 4. 总体架构

```mermaid
flowchart TB
    CLI["cmd/go-analyzer<br/>CLI 参数与绝对路径校验"]
    APP["internal/app<br/>pipeline 编排"]
    CONFIG["internal/config<br/>默认规则 + JSON override"]
    PROJECT["internal/project<br/>module/file/AST 加载"]
    INDEX["internal/astindex<br/>symbol + lightweight value type index"]
    FACTS["internal/facts<br/>统一事实模型与 Store"]
    ANNO["extract/annotation<br/>HTTP annotation"]
    ROUTE["extract/route<br/>route/group/middleware"]
    REF["extract/reference<br/>call/type/value references"]
    GOMOD["extract/gomod<br/>dependency/diff/usage"]
    LINK["internal/link<br/>handler、annotation、middleware 关联"]
    DIFF["internal/diff<br/>unified diff + semantic root mapping"]
    GRAPH["internal/graph<br/>reverse graph + route graph"]
    IMPACT["internal/impact<br/>deleted route recovery + tree propagation"]
    OUTPUT["internal/output<br/>JSON projection + schema"]
    DIAG["internal/diagnostics<br/>非致命诊断"]

    CLI --> APP
    CONFIG --> APP
    APP --> PROJECT
    PROJECT --> INDEX
    INDEX --> FACTS
    PROJECT --> ANNO
    PROJECT --> ROUTE
    PROJECT --> REF
    PROJECT --> GOMOD
    ANNO --> FACTS
    ROUTE --> FACTS
    REF --> FACTS
    GOMOD --> FACTS
    INDEX --> LINK
    LINK --> FACTS
    APP --> DIFF
    DIFF --> FACTS
    FACTS --> GRAPH
    GRAPH --> IMPACT
    IMPACT --> OUTPUT
    DIAG --> FACTS
```

纯终端下可简化理解为：

```text
CLI
  -> app
     -> config
     -> project loader
     -> AST symbol/type index
     -> annotation/route/reference/gomod extractors
     -> linker
     -> diff parser and semantic mapper
     -> deleted route and go.mod special roots
     -> reverse/route graphs
     -> impact tree
     -> JSON contract
```

### 4.1 贯穿真实 BFF 的分析链路

下面用 `sl-sc1-bff-service` 的真实代码说明“diff 到 endpoint”的完整链路。假设 MR 改到了 `service.GetMerchantInfo`：

- service 声明：`/Users/zxc/Desktop/go-analyzer-factory/sl-sc1-bff-service/service/merchant.go:18`
- controller 调用：`/Users/zxc/Desktop/go-analyzer-factory/sl-sc1-bff-service/controller/live/view/redirect.go:38`
- route 注册：`/Users/zxc/Desktop/go-analyzer-factory/sl-sc1-bff-service/router/live/view.go:13`
- endpoint 注释：`/Users/zxc/Desktop/go-analyzer-factory/sl-sc1-bff-service/controller/live/view/redirect.go:28`

真实代码关系：

```go
// service/merchant.go
func GetMerchantInfo(c context.Context, merchantId string) (*api.Merchant, error) { ... }

// controller/live/view/redirect.go
// @Get /api/bff-web/live/view/:salesId/redirect
func LiveViewRedirect(c context.Context, ctx *lego.RequestContext, req LiveViewRedirectReq) (*string, error) {
    merchantInfo, err := service.GetMerchantInfo(c, resp.MerchantId)
    ...
}

// router/live/view.go
group.GET("/:salesId/redirect", sa.ControllerWithReqResp(live_view.LiveViewRedirect))
```

对应 facts/impact 关系：

```text
astindex
  func:sc1-client-bff-service/service::GetMerchantInfo
  func:sc1-client-bff-service/controller/live/view::LiveViewRedirect

extract/reference
  FromSymbol = func:sc1-client-bff-service/controller/live/view::LiveViewRedirect
  ToSymbol   = func:sc1-client-bff-service/service::GetMerchantInfo
  Kind       = call

extract/annotation
  handler_symbol = func:sc1-client-bff-service/controller/live/view::LiveViewRedirect
  method/path    = GET /api/bff-web/live/view/:salesId/redirect

extract/route
  handler_raw    = live_view.LiveViewRedirect
  route          = GET /:salesId/redirect
  group prefix   = /api/bff-web/live/view

internal/link
  route -> handler symbol
  handler symbol -> annotation

internal/graph + internal/impact
  changed GetMerchantInfo
    -> ReverseGraph 找到 LiveViewRedirect
    -> RouteGraph 找到 route/annotation
    -> endpoint GET /api/bff-web/live/view/:salesId/redirect
```

同一条链路的图形化版本：

```mermaid
flowchart LR
    DIFF["diff<br/>service/merchant.go<br/>GetMerchantInfo"] --> CHANGE["ChangeFact<br/>func:.../service::GetMerchantInfo"]
    CHANGE --> REV["ReverseGraph<br/>ToSymbol -> FromSymbol"]
    REV --> HANDLER["func:.../controller/live/view::LiveViewRedirect"]
    HANDLER --> ROUTE["RouteGraph<br/>route for handler"]
    ROUTE --> ANNO["@Get annotation"]
    ANNO --> ENDPOINT["GET<br/>/api/bff-web/live/view/:salesId/redirect"]
```

这也是当前实现的核心策略：`buildFacts` 先把项目里的声明、引用、路由和 link 都算出来；`RunImpact` 再把 diff 命中的 symbol 放进这些索引里反向传播。

## 5. 目录与模块职责

```text
go-analyzer/
├── cmd/go-analyzer/              CLI 入口和参数测试
├── internal/
│   ├── app/                      facts/impact 主流程编排
│   ├── astindex/                 declaration symbol 与轻量 value type 索引
│   ├── config/                   默认分析规则和 JSON 配置合并
│   ├── diagnostics/              可恢复问题的标准诊断模型
│   ├── diff/                     unified diff 解析、删除块保存、语义映射
│   ├── extract/
│   │   ├── annotation/           controller HTTP 注释提取
│   │   ├── gomod/                go.mod dependency、diff 和本仓 usage
│   │   ├── reference/            call/type/value 依赖提取
│   │   └── route/                route/group/middleware/wrapper 提取
│   ├── facts/                    跨模块共享的数据模型和事实 Store
│   ├── graph/                    反向引用图与 route 领域图
│   ├── impact/                   传播树、endpoint、删除路由恢复
│   ├── link/                     route-handler-annotation 与 middleware symbol 关联
│   ├── output/                   JSON 文档、排序、schema contract
│   └── project/                  Go module 扫描与 AST 加载
├── testdata/
│   ├── fixtures/                 最小可复现 Go 项目
│   ├── diffs/                    fixture 对应 unified diff
│   └── golden/                   稳定 JSON 输出样本
├── docs/
│   ├── contracts/                输出契约
│   ├── design/                   历史设计与专项技术方案
│   ├── examples/                 配置示例
│   ├── superpowers/plans/        历史实施计划
│   └── validation/               真实项目验证记录
└── scripts/                      smoke/验收脚本
```

模块速查表：

| 模块                            | 做什么                                                             | 真实 BFF 示例                                                                                                        |
| ------------------------------- | ------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------- |
| `internal/project`            | 加载 Go module、文件、AST                                          | 读取`sl-sc1-bff-service` 的 `service/merchant.go`、`controller/live/view/redirect.go`、`router/live/view.go` |
| `internal/astindex`           | 给声明建立稳定 symbol ID                                           | `GetMerchantInfo` -> `func:sc1-client-bff-service/service::GetMerchantInfo`                                      |
| `internal/extract/reference`  | 找代码声明之间的依赖边                                             | `LiveViewRedirect` 调 `service.GetMerchantInfo` -> `call` reference                                            |
| `internal/extract/annotation` | 识别 handler 上的 HTTP 注释                                        | `@Get /api/bff-web/live/view/:salesId/redirect` -> annotation fact                                                 |
| `internal/extract/route`      | 识别 route group、route、middleware、wrapper                       | `group.GET("/:salesId/redirect", sa.ControllerWithReqResp(live_view.LiveViewRedirect))` -> route fact              |
| `internal/link`               | 把 route raw handler、handler annotation、middleware symbol 接起来 | `live_view.LiveViewRedirect` -> `func:...::LiveViewRedirect`，再连到 `@Get`                                    |
| `internal/diff`               | 把 unified diff 映射到语义 root                                    | 改`service/merchant.go` 的函数体 -> `symbol_changed` root                                                        |
| `internal/graph`              | 构造传播查询视图                                                   | `GetMerchantInfo` <- `LiveViewRedirect`；handler -> route/annotation                                             |
| `internal/impact`             | 从 change root 扩散并产出 endpoint                                 | `GetMerchantInfo` 变更最终落到 `GET /api/bff-web/live/view/:salesId/redirect`                                    |
| `internal/output`             | 输出稳定 JSON/schema                                               | 输出`go-impact/v1alpha1` 的 roots/endpoints/diagnostics                                                            |

### 5.1 `cmd/go-analyzer`

入口是 `cmd/go-analyzer/main.go`。

职责：

- 注册 `facts`、`impact`、`schema`、`help` 命令。
- 校验 CLI 的 project/diff/config 路径必须为绝对路径。
- 将参数转换为 `internal/app` options。
- 只负责进程输入输出，不承载分析逻辑。

### 5.2 `internal/app`

主编排位于：

- `internal/app/pipeline.go:24`：`RunFacts`。
- `internal/app/pipeline.go:45`：`RunImpact`。
- `internal/app/pipeline.go:128`：共享事实构建。

它是唯一应了解完整 pipeline 顺序的模块。extractor 之间不应互相调用，特殊 diff 逻辑也不应下沉到 CLI。

`RunFacts` 和 `RunImpact` 不是两套独立分析能力，而是同一条 pipeline 的两个输出层级：

```text
RunFacts
  = buildFacts(project)
  -> 输出完整项目事实快照

RunImpact
  = buildFacts(project)
  -> parse diff
  -> diff 映射到 ChangeFact
  -> deleted route / go.mod 等 diff 补偿
  -> impact tree
  -> 输出从 diff root 到 endpoint 的影响链路
```

因此 `RunFacts` 更像“项目事实调试/观测入口”，用于确认当前项目中 symbol、route、annotation、reference、link 是否被正确抽取；`RunImpact` 在这份事实基础上叠加 MR diff，并产出最终人/agent 关心的接口影响结果。保留两者的原因是调试边界更清晰：如果 impact 不对，先看 facts 是否对；facts 对但 impact 不对，再看 diff 映射和传播逻辑。

### 5.3 `internal/project`

入口：`internal/project/loader.go:13`。

职责：

- 读取根目录 `go.mod` 的 module path。
- 递归扫描 `.go` 文件。
- 跳过 `_test.go` 和配置的目录。
- 使用 `go/parser` + `parser.ParseComments` 生成 AST。
- 单个源码解析失败时保留 `package_load_failed` diagnostic，继续分析其他文件。

所有 `project.File.Path` 在内存中是绝对路径；进入 facts/output 后统一转换为项目相对路径。

### 5.4 `internal/astindex`

入口：`internal/astindex/index.go:26`。

索引以下 declaration：

- function。
- receiver method。
- type。
- package-level var/const。

稳定 symbol ID 形式：

```text
func:<package>::<name>
method:<package>:<receiver>:<name>
type:<package>::<name>
var:<package>::<name>
const:<package>::<name>
```

同一索引还保存轻量 value type：

- `var X T`
- `var X = T{}`
- `var X = &T{}`
- `var X = NewT()`
- imported type，如 `var X auth.Auth`
- struct field type，如 `type Dependencies struct { Auth auth.Auth }`

该索引用于解析常见的：

```text
pkg.Var.Method
pkg.Var.Field.Method
```

这里的设计重点是：`astindex` 首先是“声明符号索引”，其次才带一层“常见 selector receiver 解析”能力。

当前的 impact 粒度只需要定位到声明级 symbol：function、receiver method、type、package-level var/const。函数内部某一行、某个字段的精细 diff 不是本项目当前目标；结构体字段、tag、协议相关注释等落在 type declaration 内时，会先映射为 `type:<package>::<name>` 这个 symbol，再沿 type reference 继续传播。

轻量 value type 是为了解决 BFF 项目里非常常见的 package-level singleton / provider 写法。例如：

```go
var Default auth.Auth
g.Use(provider.Default.Middleware)

type Dependencies struct { Auth auth.Auth }
var Default = Dependencies{}
g.Use(provider.Default.Auth.Middleware)
```

如果只保存 declaration symbol，`provider.Default.Auth.Middleware` 只能看到一串 selector，无法知道最终是 `method:<auth package>:Auth:Middleware`。因此索引里额外保存：

- package-level var 的静态类型。
- struct field 的静态类型。

解析时从 package-level var 开始，按 field 链一路走到最终 receiver type，再拼出 method symbol。它不是 `go/types` 的替代品，只覆盖项目内、静态可解释的常见 BFF pattern；local var、接口动态派发、复杂工厂返回值、运行时注入不在当前精度目标内。

以当前“diff 发生在哪个符号，然后扩散到 endpoint”的目标来看，这组 declaration symbol 是足够的；后续如果要做更精细的动态绑定或接口实现分析，才需要引入 `go/types` / SSA。

### 5.5 `internal/facts`

`internal/facts/store.go:17` 定义统一 Store。主要事实如下：

| Fact                      | 含义                              | 主要生产者                               |
| ------------------------- | --------------------------------- | ---------------------------------------- |
| `ProjectFact`           | project root/module               | app                                      |
| `SymbolFact`            | declaration symbol                | astindex                                 |
| `AnnotationFact`        | handler HTTP method/path          | annotation extractor                     |
| `RouteGroupFact`        | route group/prefix/parent         | route extractor                          |
| `RouteRegistrationFact` | method/path/handler/wrapper       | route extractor / deleted route recovery |
| `MiddlewareBindingFact` | group middleware 及顺序           | route extractor + linker                 |
| `ReferenceFact`         | call/type/value 边                | reference extractor                      |
| `LinkFact`              | route-handler、handler-annotation | linker                                   |
| `ChangeFact`            | diff 对应的传播根                 | diff/app/impact                          |
| `Module*Fact`           | dependency/change/local usage     | gomod extractor                          |
| `DiagnosticFact`        | 可恢复的不确定性                  | 所有阶段                                 |

`facts.Store` 是 pipeline 内的共享事实总线；模块间通过 facts 通信，而不是直接共享私有 AST 状态。

### 5.6 `internal/extract/annotation`

入口：`internal/extract/annotation/extractor.go:19`。

支持配置的 `@Get`、`@Post` 等注释，输出 method/path/handler symbol。

annotation span 精确到注释行，而不是整个函数体：

- 改注释行 -> `annotation_changed`。
- 改函数签名或函数体 -> 所属 function/method symbol。

这条约束保证“diff 定位符号”的核心语义不被 annotation 覆盖。

### 5.7 `internal/extract/route`

入口：`internal/extract/route/extractor.go:21`。

提取：

- `g.Group("/prefix")`。
- `g.GET/POST/...("/path", handler)`。
- `g.Use(middleware)`。
- Group 调用中的 middleware 参数。
- handler wrapper stack。
- route group wrapper。
- statement order。

`internal/extract/route/call.go` 是正常 AST 提取和 deleted-route recovery 共用的 route call parser，避免两套语法漂移。

### 5.8 `internal/extract/reference`

入口：`internal/extract/reference/extractor.go:16`。

输入是当前项目的完整 AST、`astindex.Index` 和共享 `facts.Store`：

```text
project.Project + astindex.Index + facts.Store
```

它在 `buildFacts` 阶段执行，也就是 `RunFacts` 和 `RunImpact` 都会先做这一步；并不是等 diff 出现以后才分析引用关系。

提取四类 reference：

- `call`：函数/方法调用。
- `type`：参数、返回值、字段、组合字面量、泛型参数等类型引用。
- `value`：var/const/function value。
- `selector`：模型保留类型；当前常见 selector 主要输出为 call/value。

边方向是：

```text
FromSymbol depends on ToSymbol
```

其中 `FromSymbol` 是当前正在扫描的声明：

- 函数/方法签名或函数体内的引用，`FromSymbol` 是该 function/method。
- type declaration 内的字段、嵌入、组合等引用，`FromSymbol` 是该 type。
- package-level var/const 的类型或初始化表达式引用，`FromSymbol` 是该 var/const。

`ToSymbol` 是通过 `astindex` 能解析到的项目内目标 symbol。举例：

```go
func (c *OrderController) Get() {
    service.QueryOrder()
}
```

会产生类似：

```text
FromSymbol = method:<controller package>:OrderController:Get
ToSymbol   = func:<service package>::QueryOrder
Kind       = call
```

这条边的含义是“controller method 依赖 service function”。当 service function 被 diff 命中时，impact 传播要反向找依赖者，因此后续 `internal/graph` 会把它转成 `ToSymbol -> []FromSymbol` 查询视图。

传播时 `internal/graph` 会构造反向索引：

```text
ToSymbol -> all FromSymbol
```

### 5.9 `internal/extract/gomod`

职责分三层：

1. `extractor.go`：读取当前 `go.mod` dependency/replace。
2. `diff.go:12`：从 go.mod diff 的新增/删除行恢复 module changes。
3. `usage.go:16`：把 changed module 映射到本仓 import usage。

支持：

- 单行 require。
- require block 中单独变化的依赖行，即使 hunk 不包含 `require (`。
- replace-only hunk。
- added/removed/upgraded/downgraded/replaced。

本仓 usage 精度：

- 函数/方法体直接使用 import alias -> precise symbol。
- 只能确认 importing file -> file/declaration fallback。
- 本仓没有 import -> unreferenced，不产生 endpoint root。

### 5.10 `internal/link`

入口：`internal/link/linker.go:10`。

职责：

- route handler raw expression -> handler symbol。
- route -> handler link。
- handler -> annotation link。
- middleware raw expression -> middleware function/method symbol。

handler/middleware selector 共用 `astindex` 的 value type 解析，因此可以跨包解析：

```go
var Default auth.Auth
g.Use(provider.Default.Middleware)
```

也可以解析一层或多层已索引 struct field：

```go
var Default = Dependencies{}
g.Use(provider.Default.Auth.Middleware)
```

它解决的是“不同 extractor 产出的事实如何接到同一条 endpoint 链路上”的问题。

几个 extractor 的关注点不同：

- `route` extractor 只负责从路由注册语法里抽出 `GET /path -> handler raw expression`。
- `annotation` extractor 只负责从 handler 注释里抽出 `@Get /path -> handler symbol`。
- `reference` extractor 只负责代码 symbol 之间的依赖边。

这些事实如果不 link，route 里只会留下 `controller.GetOrder` 这样的 raw string，无法稳定传播到 handler symbol；annotation 也无法和 route 注册关联起来。`internal/link` 的作用就是把 raw expression 解析成 symbol，并补充：

```text
route registration -> handler symbol
handler symbol -> annotation
middleware binding -> middleware function/method symbol
```

后续 `RouteGraph` 才能回答：

- 某个 handler 变了，影响哪些 route。
- 某个 middleware 方法变了，影响哪些挂载了该 middleware 的 route。
- route 找到 handler 后，优先用 handler annotation 确认 endpoint。

真实 BFF 例子：

```go
// router/live/view.go
group.GET("/:salesId/redirect", sa.ControllerWithReqResp(live_view.LiveViewRedirect))

// controller/live/view/redirect.go
// @Get /api/bff-web/live/view/:salesId/redirect
func LiveViewRedirect(...) (*string, error) { ... }
```

`route` extractor 只能先得到：

```json
{
  "method": "GET",
  "local_path": "/:salesId/redirect",
  "resolved_path": "/api/bff-web/live/view/:salesId/redirect",
  "handler_raw": "live_view.LiveViewRedirect",
  "wrappers": [
    {
      "name": "ControllerWithReqResp",
      "raw": "sa.ControllerWithReqResp(live_view.LiveViewRedirect)"
    }
  ]
}
```

这里的 `handler_raw` 仍然只是 route 文件里的表达式文本。`link` 会结合 route 文件 import：

```go
import live_view "sc1-client-bff-service/controller/live/view"
```

把它解析成稳定 handler symbol，并写回 route：

```json
{
  "handler_symbol": "func:sc1-client-bff-service/controller/live/view::LiveViewRedirect"
}
```

同时补两条 link：

```json
{
  "kind": "route_to_handler",
  "from_id": "route:func:sc1-client-bff-service/router/live::InitLiveViewRouter:GET:/:salesId/redirect:2",
  "to_id": "func:sc1-client-bff-service/controller/live/view::LiveViewRedirect"
}
```

```json
{
  "kind": "handler_to_annotation",
  "from_id": "func:sc1-client-bff-service/controller/live/view::LiveViewRedirect",
  "to_id": "annotation:func:sc1-client-bff-service/controller/live/view::LiveViewRedirect:GET:/api/bff-web/live/view/:salesId/redirect:0"
}
```

因此 `internal/link` 不是做“函数调用依赖”的模块；函数调用依赖归 `internal/extract/reference`。`link` 解决的是 route 领域里的身份对齐：把 route 文件里的 handler 表达式、controller 文件里的 handler 声明、handler 注释里的 endpoint 归并到同一个 handler symbol 上。

### 5.11 `internal/diff`

入口：

- `internal/diff/parser.go:14`
- `internal/diff/mapper.go:11`

parser 保存：

- old/new path。
- added/deleted/modified status。
- 新版本行号范围。
- deletion-only anchor。
- 连续删除块的旧行号、新版本锚点和原文本。
- 每个文件的原始 patch。

mapper 按领域事实优先级选择最精确 root：

```text
annotation
  -> route group
  -> route
  -> middleware
  -> smallest containing symbol
  -> file fallback
```

### 5.12 `internal/graph`

包含两个运行时查询视图。它们都基于同一个 `facts.Store` 临时构造，不是新的事实来源，也不会写回业务事实：

- `ReverseGraph`：被依赖 symbol -> 依赖它的 symbol references。
- `RouteGraph`：handler/group/middleware -> routes/annotations。

graph 不生产业务事实，只对 Store 建立高效查询视图。

需要拆成两个视图，是因为两类关系的语义不同：

```text
ReverseGraph: 代码依赖传播
  changed service symbol
    -> caller controller symbol
    -> caller route init symbol ...

RouteGraph: HTTP 路由域传播
  handler symbol
    -> route registration
    -> annotation
    -> endpoint

  middleware binding
    -> same group 且 statement order 更靠后的 routes

  route group
    -> descendant routes
```

impact tree 构造时会同时使用它们：

```text
ChangeFact(symbol)
  -> ReverseGraph.ReferencesTo(symbol)
  -> 上游 symbols
  -> RouteGraph.RoutesForHandler(handler)
  -> annotation / endpoint

ChangeFact(route/middleware/group/annotation)
  -> 直接进入 RouteGraph
  -> endpoint
```

如果没有 `ReverseGraph`，从 service/type 无法反推到 controller；如果没有 `RouteGraph`，从 handler/middleware/group 无法进入最终 HTTP endpoint。拆开后每个视图都保持简单，也避免在传播阶段反复 O(n) 扫描 Store。

### 5.13 `internal/impact`

入口：

- `internal/impact/deleted_route.go:22`
- `internal/impact/tree_builder.go:26`

职责：

- 从 diff 删除块恢复已删除 route registration。
- 为每个 ChangeFact 构造独立 impact tree。
- 从 symbol 传播到引用者。
- 从 handler、route group、middleware 进入 route graph。
- 以 annotation 为首选 endpoint。
- 在缺少 annotation 时保留 route method/path fallback。
- 处理 cycle、maxDepth、stopPropagation。

### 5.14 `internal/output`

入口：

- `internal/output/impact_tree.go:68`
- `internal/output/impact_tree.go:152`
- `internal/output/contract.go`

职责：

- 把内部 tree 投影为稳定 JSON。
- 按 source file 聚合 change roots。
- 去重 endpoint 与 diagnostics。
- 稳定排序，降低 golden/consumer 抖动。
- 根据配置裁剪 raw evidence 和 raw diff。
- 暴露 facts/impact JSON Schema。

### 5.15 `internal/diagnostics`

diagnostic 是可恢复的不确定性，不等同于程序失败。

典型情况：

- route 动态 path。
- handler/symbol/type 无法精确解析。
- 删除声明只能降级。
- go.mod diff 无法识别 module。
- module usage 只能 file fallback。
- 传播被 maxDepth 截断。

诊断码定义以 `internal/diagnostics/codes.go` 为准。

## 6. Facts 构建流程

```mermaid
sequenceDiagram
    participant CLI
    participant App
    participant Project
    participant Index
    participant Extractors
    participant Linker
    participant Store
    participant Output

    CLI->>App: facts(project, config)
    App->>Project: Load Go module
    Project-->>App: packages/files/AST/diagnostics
    App->>Index: Build declaration + value type index
    Index-->>Store: SymbolFacts
    App->>Extractors: annotation / route / gomod
    Extractors-->>Store: domain facts
    App->>Linker: route-handler-annotation + middleware
    Linker-->>Store: links / resolved symbols
    App->>Extractors: reference extraction
    Extractors-->>Store: ReferenceFacts
    App->>Output: RenderJSON(Store)
    Output-->>CLI: facts JSON
```

实际调用顺序见 `internal/app/pipeline.go:128`：

1. `project.Load`。
2. `astindex.Build`。
3. 当前 go.mod dependencies。
4. project load diagnostics。
5. symbols。
6. annotation。
7. route。
8. link。
9. reference。

## 7. Impact 构建流程

```mermaid
sequenceDiagram
    participant CLI
    participant App
    participant Facts
    participant Diff
    participant DeletedRoute
    participant GoMod
    participant Impact
    participant Output

    CLI->>App: impact(project, diff, config)
    App->>Facts: build post-change fact store
    App->>Diff: ParseUnified
    Diff-->>App: FileChanges + DeletedBlocks
    App->>Diff: MapChanges
    Diff-->>Facts: normal ChangeFacts
    App->>DeletedRoute: RecoverDeletedRoutes
    DeletedRoute-->>Facts: synthetic routes + route_deleted roots
    App->>GoMod: DiffModulesFromFileChanges
    GoMod-->>Facts: ModuleChangeFacts
    App->>GoMod: MapModuleUsage
    GoMod-->>Facts: ModuleUsageFacts
    App->>Facts: usage -> symbol/file ChangeFacts
    App->>Impact: AnalyzeTrees
    Impact-->>App: roots + endpoints + diagnostics
    App->>Output: BuildImpactDocument
    Output-->>CLI: go-impact/v1alpha1 JSON
```

`RunImpact` 的源码入口是 `internal/app/pipeline.go:45`。

## 8. Diff 到语义根

### 8.1 普通新增/修改

新增行使用新版本行号直接命中变更后 AST 的领域事实或 symbol。

如果一个 range 同时落在多个 declaration span 中，选择行跨度最小的 symbol，保证优先命中最具体 declaration。

### 8.2 struct 字段和 tag

astindex 不生成 field symbol。整个 `type` declaration 的 span 包含字段：

```text
field/tag diff -> owning type symbol
```

随后 type references 会将影响传播到使用该类型的其他 type/function/method。

### 8.3 删除普通声明

当前没有完整 base snapshot。删除行先映射到新版本 anchor：

- anchor 仍位于 surviving declaration -> medium confidence symbol root。
- declaration 整体已不存在 -> file root + `deleted_symbol_unresolved`。

这是单快照的明确边界，不应把 file fallback 解释成精确 symbol 恢复。

### 8.4 删除 route registration

删除 route 是定向增强：

1. diff parser 保留删除块原文。
2. deleted route parser 将完整删除块包装成临时 Go function。
3. 支持单行和多行 route call。
4. 复用正常 route call parser。
5. 尝试从变更后 route/group facts 恢复 group/prefix。
6. 尝试恢复 handler symbol 与 annotation。
7. 添加 synthetic route 和 `route_deleted` root。

优先 endpoint：

```text
deleted route -> handler annotation -> endpoint
```

无法解析 annotation 时：

```text
deleted route -> route method/path fallback endpoint
```

fallback relation 是 `deleted_route_endpoint`，confidence 为 `medium`。

### 8.5 go.mod

go.mod 不直接形成普通 file root，而是：

```text
changed module
  -> local import usage
  -> local symbol/file ChangeFact
  -> normal impact propagation
```

因此 go.mod 可以在 `fileSources` 中同时出现：

- `go.mod` 原始 diff source。
- 实际被 module usage seed 的本仓源码 source。

顶层 `module_changes` / `module_usages` 保留这段转换的解释证据。

## 9. Route 与 endpoint 语义

当前以 controller annotation 作为 endpoint method/path 的首选真值。

原因：

- BFF route prefix 可能来自常量、wrapper、generated helper。
- 静态拼接 route path 容易产生“看似精确、实际错误”的路径。
- annotation 更接近外部 HTTP contract。

route facts 的作用：

- 证明 handler 被注册。
- 建立 route group/middleware/wrapper 传播关系。
- 在 annotation 缺失时提供 method/path fallback。

正常链路：

```text
changed symbol
  -> dependent symbols
  -> registered route
  -> handler annotation
  -> endpoint
```

middleware 链路：

```text
changed middleware method
  -> middleware binding
  -> same group and later statement routes
  -> handler annotation
  -> endpoint
```

`statement_index` 只在同一个 route function 内用于 middleware 顺序判断。

## 10. Impact Tree 算法

每个 `ChangeFact` 独立生成一个 root，多个 roots 不会互相覆盖。

symbol 展开规则：

1. 通过 ReverseGraph 找引用当前 symbol 的 symbols。
2. 通过 RouteGraph 找以当前 symbol 为 handler 的 routes。
3. 查找引用当前 symbol 的 middleware bindings。
4. 递归展开。

领域 root：

- route -> annotation/endpoint。
- route group -> group 内及 child group routes。
- middleware -> 在其后注册的 routes。
- annotation -> endpoint。
- file fallback -> 无子节点，但仍保留在报告。

终止策略：

- 当前 DFS path 已存在 symbol -> child 标记 `cycle`。
- node 文件命中 `stopPropagation` -> 标记 `stopBoundary`。
- 达到 `maxDepth` -> 截断并输出 `propagation_depth_truncated`。

## 11. 输出契约

### 11.1 Facts

```bash
go run ./cmd/go-analyzer facts \
  --project /absolute/path/to/project \
  --format json
```

适合：

- 检查 symbol/route/reference 是否被提取。
- 调试 linker。
- 统计真实项目覆盖情况。

### 11.2 Impact

```bash
go run ./cmd/go-analyzer impact \
  --project /absolute/path/to/project \
  --diff /absolute/path/to/change.diff \
  --format json
```

顶层：

```json
{
  "meta": {
    "schemaVersion": "go-impact/v1alpha1",
    "projectRoot": "/absolute/path/to/project",
    "diagnostics": []
  },
  "module_changes": [],
  "module_usages": [],
  "summary": {
    "impactedEndpointCount": 0,
    "impactedEndpoints": []
  },
  "fileSources": []
}
```

每个 `fileSources[]` 保留：

- `sourceFile`。
- 可选原始 `diff`。
- `symbols`：changed roots 到递归 impact nodes。
- `impactedEndpoints`：去重 endpoint 摘要。

顶层 `summary` 是全局去重后的轻量结果，面向默认消费场景：`impactedEndpointCount`
表示影响接口数量，`impactedEndpoints` 列出这些接口；`fileSources` 继续保留调试和溯源需要的完整树。

`confidence` 表示 analyzer 对某个 fact、change root 或传播节点的静态证据强度，不是概率分数，也不会自动控制传播是否继续：

- `high`：来自明确 AST/fact 证据，例如 diff 命中现存 symbol/route/annotation、reference/link 精确解析。
- `medium`：来自定向 fallback 或推断，例如 deletion anchor 命中 surviving declaration、deleted route 用 method/path fallback endpoint、go.mod usage 降级到 importing file declarations。
- `low`：只能保留弱 fallback，例如无法映射到语义 fact 的 file-level root。

下游消费者可以用它做展示和人工复核优先级：`high` 正常采信，`medium` 标记为建议复核，`low` 视为分析器未能精确定位的信号。

详细字段见 `docs/contracts/output-contract.md`。

### 11.3 Schema

```bash
go run ./cmd/go-analyzer schema --type facts
go run ./cmd/go-analyzer schema --type impact
```

新增/修改 JSON 字段时必须同步：

1. Go output struct。
2. `internal/output/contract.go`。
3. `docs/contracts/output-contract.md`。
4. output tests/golden。

## 12. 配置

示例：`docs/examples/go-analyzer.config.json`。

支持项：

| 配置                            | 用途                        |
| ------------------------------- | --------------------------- |
| `project.skipDirs`            | 追加跳过目录                |
| `route.httpMethods`           | 追加 HTTP 注册方法          |
| `route.handlerWrappers`       | 追加 handler wrapper        |
| `route.routeGroupWrappers`    | 追加 group wrapper 匹配规则 |
| `annotation.methods`          | 追加 annotation method      |
| `analysis.maxDepth`           | 最大传播深度；0 为不限      |
| `analysis.stopPropagation`    | 文件 glob 截断边界          |
| `analysis.includeRawEvidence` | impact 是否保留 raw         |
| `analysis.includeDiff`        | impact 是否保留原始 diff    |

配置是“默认规则 + override 追加/覆盖”，不是完全替换默认列表。

## 13. 构建、运行和测试

前置条件：

- Go 1.24 或更高。
- 项目本身只有 Go 标准库依赖。
- CLI path 参数使用绝对路径。

常用命令：

```bash
go test ./...
go vet ./...
go build ./cmd/go-analyzer
gofmt -l .
git diff --check
```

严格 lint（本机已安装兼容版本时）：

```bash
golangci-lint run --no-config --go 1.24 ./...
```

本机 `staticcheck` 如果由低于 Go 1.24 的 toolchain 构建，会报 unsupported version；应升级工具，而不是将该错误解释为源码失败。

## 14. 调试指南

### 14.1 从最小 fixture 调试

facts：

```bash
PROJECT="$(pwd)/testdata/fixtures/type-impact"
go run ./cmd/go-analyzer facts \
  --project "${PROJECT}" \
  --format json > /tmp/go-analyzer-facts.json
python3 -m json.tool /tmp/go-analyzer-facts.json > /dev/null
```

impact：

```bash
PROJECT="$(pwd)/testdata/fixtures/type-impact"
PATCH="$(pwd)/testdata/diffs/type-impact.diff"
go run ./cmd/go-analyzer impact \
  --project "${PROJECT}" \
  --diff "${PATCH}" \
  --format json > /tmp/go-analyzer-impact.json
python3 -m json.tool /tmp/go-analyzer-impact.json > /dev/null
```

三个专项 fixture：

```text
deleted-route       多行删除 route + handler annotation 恢复
gomod-impact        require block 版本变化 -> local usage -> endpoint
middleware-selector package var/struct field/method -> middleware -> endpoint
```

替换上面的 fixture/diff 名即可单独复现。

### 14.2 聚焦单元测试

```bash
go test ./internal/diff -run TestParseUnifiedPreservesDeletedBlocks -v
go test ./internal/extract/gomod -run TestDiffModulesFromFileChanges -v
go test ./internal/link -run TestRunLinksPackageVar -v
go test ./internal/impact -run TestRecoverDeletedRoutes -v
go test ./internal/app -run TestRunImpact -v
```

### 14.3 使用 Delve

```bash
dlv debug ./cmd/go-analyzer -- \
  impact \
  --project "$(pwd)/testdata/fixtures/type-impact" \
  --diff "$(pwd)/testdata/diffs/type-impact.diff" \
  --format json
```

推荐断点：

- `internal/app/pipeline.go:45`
- `internal/diff/mapper.go:11`
- `internal/impact/tree_builder.go:26`
- `internal/impact/deleted_route.go:22`

### 14.4 基于真实项目生成 diff

分析器要求 project 是“变更后状态”。在目标项目已经 checkout 到 MR head 后：

```bash
git diff --no-ext-diff --unified=3 <base-ref>...HEAD > /tmp/mr.diff

go run /absolute/path/to/go-analyzer/cmd/go-analyzer impact \
  --project "$(pwd)" \
  --diff /tmp/mr.diff \
  --format json > /tmp/mr-impact.json
```

注意：

- `--project` 和 `--diff` 都必须是绝对路径。
- diff path 应相对目标项目根目录。
- 不要用未应用到 working tree 的 head diff 配合旧源码，否则行号和 AST 不匹配。
- 如果分析 deletion-only 变更，保留标准 hunk context 有助于 anchor 与 group 恢复。

### 14.5 两个真实 BFF smoke

项目集合目录应为：

```text
workspace/
├── go-analyzer/
├── sl-sc1-bff-service/   # legacy 名 sc1-bff-service 也支持
└── sl-sc1-admin-bff/     # legacy 名 sc1-admin-bff 也支持
```

执行：

```bash
bash scripts/smoke-real-projects.sh
```

脚本会：

1. 对两个真实项目运行 facts。
2. 校验 JSON。
3. 输出 symbol/annotation/route/diagnostic 数量。
4. 运行 type-impact、deleted-route、gomod-impact、middleware-selector。
5. 验证每个专项 fixture 的 endpoint。

输出写入 `.analyzer-smoke/`，该目录被 git ignore。

### 14.6 推荐排查命令

检查目标 symbol：

```bash
python3 - <<'PY'
import json
data = json.load(open("/tmp/go-analyzer-facts.json"))
for item in data["symbols"]:
    if "Order" in item["id"]:
        print(item)
PY
```

检查 endpoint 和 diagnostics：

```bash
python3 - <<'PY'
import json
data = json.load(open("/tmp/go-analyzer-impact.json"))
print(data["summary"]["impactedEndpointCount"])
for endpoint in data["summary"]["impactedEndpoints"]:
    print(endpoint["method"], endpoint["path"])
for diagnostic in data["meta"]["diagnostics"]:
    print(diagnostic["code"], diagnostic["message"])
PY
```

## 15. 测试与验证矩阵

| 层级            | 位置                                     | 验证内容                           |
| --------------- | ---------------------------------------- | ---------------------------------- |
| parser/index    | 各 package`_test.go`                   | AST、diff、配置、module            |
| extractor       | `internal/extract/*`                   | annotation/route/reference/gomod   |
| linker/graph    | `internal/link`, `internal/graph`    | handler/middleware/route 关联      |
| impact          | `internal/impact`                      | tree、cycle、depth、deleted route  |
| pipeline E2E    | `internal/app/pipeline_test.go`        | diff -> endpoint 完整闭环          |
| contract/golden | `internal/output`, `testdata/golden` | JSON shape 和稳定排序              |
| real smoke      | `scripts/smoke-real-projects.sh`       | 两个真实 BFF + 四个 impact fixture |

2026-06-27 验证快照：

| Project/Fixture         | 结果                                                       |
| ----------------------- | ---------------------------------------------------------- |
| `sl-sc1-bff-service`  | symbols=781, annotations=32, routes=32, diagnostics=20     |
| `sl-sc1-admin-bff`    | symbols=5137, annotations=463, routes=490, diagnostics=213 |
| `type-impact`         | 1 endpoint (`POST /orders`)                              |
| `deleted-route`       | 1 endpoint (`POST /public/orders`)                       |
| `gomod-impact`        | 1 endpoint (`GET /api/checkIn`)                          |
| `middleware-selector` | 1 endpoint (`GET /orders`)                               |

## 16. 当前能力边界

### 16.1 明确支持

- 单快照 Go AST/facts 构建。
- function/method/type/var/const symbol。
- call/type/value 反向传播。
- struct 字段/tag 归属 type 后传播。
- controller HTTP annotation。
- route/group/middleware/wrapper。
- route handler 与 common package-var method。
- package var、constructor、imported explicit type、struct field 的轻量 receiver 推断。
- 删除 route registration 的单行/多行恢复。
- go.mod require/replace diff 到本仓 usage 和 endpoint。
- cycle/maxDepth/stopPropagation。
- 原始可追踪 impact tree 和 endpoint 摘要。

### 16.2 有降级但不会静默丢失

- 单个 Go 文件语法错误 -> `package_load_failed`。
- 动态 route path -> raw + diagnostic。
- 无法解析 handler/symbol/type -> diagnostic。
- 删除普通 declaration -> anchor 或 file fallback。
- module import 只能定位文件 -> fallback usage。

### 16.3 尚不支持

- base/head 双快照。
- 删除整个普通 function/type/service/controller 的旧 AST 精确恢复。
- 外部 module 两个版本之间的 API/source diff。
- 二方包跨仓传播。
- `go/types`、SSA、interface 动态分发和完整 call graph。
- 反射、运行时 DI、运行时 route 构造。
- flow-sensitive local variable receiver type inference。
- build tags、不同 GOOS/GOARCH 的条件编译模型。
- `_test.go` 分析。
- 任意控制流中的完整 route table 还原；当前 route 提取重点覆盖 route function 的顺序式注册。

这些限制不会阻止当前目标：

```text
post-change diff -> project-local semantic propagation -> affected BFF endpoints
```

## 17. 扩展与修改原则

### 17.1 新语法场景

优先顺序：

1. 添加最小 fixture。
2. 写失败测试。
3. 扩展 config 或可复用 parser。
4. 输出明确 fact。
5. 无法精确时添加 diagnostic。
6. 增加 E2E impact 测试。
7. 必要时加入 smoke。

不要把真实业务仓的 package/path/controller 名硬编码进 analyzer。

### 17.2 新传播关系

必须回答：

- source fact 是什么。
- target fact 是什么。
- edge relation 名称是什么。
- confidence 是什么。
- cycle/depth 如何处理。
- output 如何解释。

### 17.3 新输出字段

必须同步 Go struct、schema、contract 文档、测试和 golden。`go-impact/v1alpha1` 仍处于 alpha，但消费者需要确定性排序和明确迁移说明。

### 17.4 保持模块边界

- `project/astindex/extract/link` 负责“代码中有什么”。
- `diff` 负责“哪里变了”。
- `graph/impact` 负责“影响如何传播”。
- `output` 负责“如何稳定表达”。
- `app` 负责“按什么顺序执行”。

不要在 extractor 中直接计算 impacted endpoints，也不要在 output 中补业务关系。

## 18. Agent 接手清单

开始修改前：

```bash
git status --short --branch
go test ./...
bash scripts/smoke-real-projects.sh
```

开发时：

- 先定位属于哪个事实层。
- 先写失败测试。
- fixture 保持最小，不复制真实业务项目。
- 复用 route parser/value type resolver，不建立平行实现。
- 新的不确定场景输出 diagnostic。

完成前：

```bash
gofmt -l .
go test -count=1 ./...
go vet ./...
golangci-lint run --no-config --go 1.24 ./...
git diff --check
bash scripts/smoke-real-projects.sh
```

再检查：

- 当前工作区是否只包含本任务改动。
- JSON schema/contract 是否同步。
- README、本架构文档、验证记录是否仍然准确。
- `.analyzer-smoke/` 是否未进入 git。

## 19. 相关文档

- `README.md`：项目定位和快速使用。
- `docs/contracts/output-contract.md`：JSON 输出契约。
- `docs/examples/go-analyzer.config.json`：配置示例。
- `docs/validation/real-project-validation.md`：真实项目验收记录。
- `docs/design/`：历史设计与专项方案。
- `docs/superpowers/plans/`：历史实施计划，不作为当前状态真值。

当前实现状态、模块边界和接手说明以本文件为准。
