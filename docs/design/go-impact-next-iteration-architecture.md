# go-analyzer 下一阶段影响分析技术方案

## 1. 文档状态

本文定义 `go-analyzer` 在 `go-impact/v1alpha1` 之后的下一轮能力增强方案。

本轮 scope 采用方案 B：

1. deleted route registration 影响分析。
2. go.mod diff 到 endpoint 的影响传播。
3. 轻量 receiver/type inference，优先解决 middleware selector 和常见 BFF selector pattern。

本轮仍不实现：

- 完整 base/head 双快照。
- 完整 deleted symbol 恢复。
- go/types / SSA / call graph。
- 二方包源码 diff 分析。

## 2. 目标

当前 `go-analyzer` 已经可以处理“变更后项目中仍存在的 symbol / route / annotation / middleware”。
下一阶段要补齐三个实际 MR 场景：

```text
删除路由注册
  -> 输出被删除/受影响的接口

go.mod 依赖版本变更
  -> 找到本仓 import usage
  -> 从使用点继续传播到 endpoint

middleware selector / receiver method 变更
  -> 能从方法实现反向找到 route group 使用点
```

这些能力继续遵守当前核心原则：

- diff 只定位语义根。
- impact tree 保留传播路径。
- annotation 仍是 endpoint method/path 真值；缺 annotation 时才降级使用 route method/path。
- 不能精确分析时输出 diagnostics，不静默丢弃。

## 3. Deleted Route Registration

### 3.1 当前问题

当前 diff parser 能识别删除行，并为 deletion-only hunk 生成 `deletion_anchor`。
但事实库来自变更后的 AST：

```text
route registration deleted from source
  -> post-change AST no longer has RouteRegistrationFact
  -> deletion anchor can only map to nearby surviving declaration
  -> cannot reconstruct deleted method/path/handler
```

这会导致“删除接口路由注册”无法稳定输出对应 endpoint。

### 3.2 设计

新增 diff 删除块模型：

```go
type DeletedBlock struct {
    OldStartLine int
    NewAnchorLine int
    Lines []string
}
```

`diff.ParseUnified` 在保留当前 `Ranges` 行为的同时，额外保存每个 hunk 中连续的删除行。
删除行保留去掉 `-` 前缀后的原始文本。

新增 deleted route recovery：

```text
FileChange.DeletedBlocks
  -> deleted route parser
  -> synthetic RouteRegistrationFact
  -> ChangeKindRouteDeleted
  -> impact tree root kind route
```

恢复策略：

1. 只对 `.go` 文件生效。
2. 对删除块中的每一行尝试按 Go expression 解析；不能识别成 route call 的行跳过。
3. 先从 `internal/extract/route` 抽出可复用的 route call parser helper，再由正常 AST extractor 和 deleted route recovery 共用，避免维护两套路由语法。
4. 使用删除行的 receiver/group var 在变更后 facts 中匹配仍存在的 route group 或同组 route，恢复 `GroupID` 和 prefix。
5. 如果 group prefix 无法恢复，但 method/local path 可解析，仍输出 deleted route node，并用 route local path 作为 endpoint fallback。
6. handler symbol/annotation 不作为 deleted route 输出 endpoint 的前置条件；删除路由本身的 method/path 足以产生 endpoint。

### 3.3 输出语义

新增 change kind：

```text
route_deleted
```

输出树示例：

```text
deleted_route:router.go:GET:/orders:anchor
  -> annotation:OrderAPI.List GET /orders   // handler annotation 仍存在时
  -> endpoint:GET:/orders
```

如果 annotation 不存在：

```text
deleted_route:router.go:GET:/orders:anchor
  -> endpoint:GET:/orders   // confidence=medium, relation=deleted_route_endpoint
```

新增 diagnostics：

- `deleted_route_unresolved`: 删除块看起来像 route，但无法解析 method/path/handler。
- `deleted_route_handler_unresolved`: route 可解析，但 handler 无法映射到 symbol。
- `deleted_route_endpoint_fallback`: group prefix 无法恢复，使用 route local path 输出 fallback endpoint。

## 4. go.mod diff 到 endpoint

### 4.1 当前问题

当前 facts 输出已经能读取当前 go.mod dependencies。
`internal/extract/gomod.MapModuleUsage` 也已有单元能力。

但 `RunImpact` 没有把 go.mod diff 接到 impact tree：

```text
go.mod diff
  -> no ModuleChangeFact in impact store
  -> no ModuleUsageFact
  -> no ChangeFact for local usage symbol
  -> no endpoint propagation
```

所以现在升级 package 版本不会产生 endpoint 影响链路。

### 4.2 边界

本轮只分析“本仓代码使用了哪个 changed module”。

不分析：

- 依赖包内部源码变化。
- 二方包 API diff。
- go.sum 独立变化。
- indirect dependency 的传递影响。

### 4.3 设计

新增 go.mod diff parser：

```text
FileChange.Raw for go.mod
  -> parse require/replace deleted and added lines
  -> ModuleChangeFact
```

支持：

- 单行 `require example.com/pkg v1.0.0`
- block require 中的 `example.com/pkg v1.0.0`
- `replace old => new version`
- added / removed / upgraded / downgraded / replaced

第一版不做完整 patch apply；只从 diff 中的 require/replace 行提取 changed module。
这是和当前目标一致的 YAGNI 方案：我们只需要知道“哪个 module path 发生变化”，再映射本仓 import usage。

传播流程：

```text
go.mod FileChange
  -> ModuleChangeFact
  -> gomod.MapModuleUsage
  -> ModuleUsageFact
  -> ChangeFact
  -> impact.AnalyzeTrees
```

`ModuleUsageFact` 到 `ChangeFact` 的映射：

| usage basis | ChangeFact |
| --- | --- |
| `module_reference_precise` with `SymbolID` | `symbol_changed`, source=`go_mod_diff`, confidence=`medium` |
| `module_reference_file_fallback` with `SymbolID` | `symbol_changed`, source=`go_mod_diff`, confidence=`medium` |
| `module_reference_file_fallback` without `SymbolID` | `file_changed`, source=`go_mod_diff`, confidence=`low` |
| `module_unreferenced` | keep diagnostic, no endpoint |

输出树示例：

```text
module_usage:gopkg.in/foo/jsonx -> func:service::Encode
  -> method:controller:OrderAPI:Create
  -> route
  -> annotation
  -> endpoint
```

### 4.4 Diagnostics

已有 diagnostics 继续使用：

- `module_usage_file_fallback`
- `module_unreferenced`

新增：

- `module_diff_unresolved`: go.mod diff 无法解析出 module path。

## 5. Lightweight Receiver / Type Inference

### 5.1 当前问题

真实 BFF smoke 中 unresolved 主要来自 selector 链：

```text
appProxyAuth.AppProxyAuthOptionalLogin.Middleware
merchantGrpc.MerchantTokenClientServiceClient.GetToken
bfferror.UNAUTHORIZED.Code
```

其中最影响 endpoint 传播的是 middleware selector：

```go
g.Use(appProxyAuth.AppProxyAuthOptionalLogin.Middleware)
```

如果改的是 `Middleware` 方法本身，当前 AST-only resolver 不一定能反向找到这类 route group 使用点。

### 5.2 本轮目标

不引入 go/types。

只做项目内、可解释、低成本的类型推断：

1. package-level var/const 的显式类型。
2. package-level var 的 composite literal 类型。
3. struct field 的显式类型。
4. selector chain 中 `pkg.Var.Method` 到 `method:<pkg>:<VarType>:Method` 的解析。
5. route middleware 参数中的 selector method 复用同一套 resolver。

### 5.3 不支持

- interface 动态分发。
- DI container 绑定。
- 运行时赋值后类型变化。
- 反射。
- 跨 module 外部包 method 精确解析。
- SSA call graph。

这些场景继续输出 unresolved diagnostics。

### 5.4 设计

新增轻量类型索引：

```go
type TypeIndex struct {
    Values map[facts.SymbolID]ValueType
    StructFields map[facts.SymbolID]map[string]ValueType
}

type ValueType struct {
    PackagePath string
    TypeName string
    Confidence facts.Confidence
}
```

构建来源：

- `var X T`
- `var X = T{}`
- `var X = &T{}`
- `type API struct { Svc service.OrderService }`

resolver 增强：

```text
pkg.Var.Method
  -> resolve pkg alias
  -> resolve var symbol pkg.Var
  -> lookup var type
  -> resolve method symbol on type
```

示例：

```go
var AppProxyAuthOptionalLogin = AppProxyAuth{}

func (a AppProxyAuth) Middleware(ctx context.Context) {}

g.Use(appProxyAuth.AppProxyAuthOptionalLogin.Middleware)
```

解析为：

```text
method:<appProxyAuth package>:AppProxyAuth:Middleware
```

然后现有 reverse graph 可从 `Middleware` 方法变更传播到 middleware binding，再到受影响 route。

## 6. Pipeline 调整

当前：

```text
build fact store
  -> parse diff
  -> map changes
  -> AnalyzeTrees
```

下一阶段：

```text
load project
  -> build AST index
  -> build lightweight type index
  -> extract/link/reference facts using type index
  -> parse diff
  -> recover deleted route facts
  -> recover go.mod module changes
  -> map normal changes
  -> map module usages to local changes
  -> AnalyzeTrees
```

关键约束：

- `facts` 命令不读取 diff，不输出 deleted route。
- `impact` 命令可以向 store 追加 synthetic facts，但 synthetic facts 必须有明确 `source_family` 或 ID 前缀。
- output schema 不引入新顶层结构；只扩展 node kind / relation / diagnostics。

## 7. 测试策略

新增 fixtures：

- `deleted-route`: 删除单行 route registration。
- `deleted-route-wrapper`: 删除多行 wrapped route registration。
- `gomod-impact`: go.mod require version 变化影响 service 函数，再传播到 endpoint。
- `middleware-selector`: middleware method 变更传播到 route。

重点测试：

- deleted route 能输出 endpoint。
- deleted route 无 annotation 时输出 fallback endpoint 和 diagnostic。
- go.mod changed module 能映射 precise module usage。
- go.mod unused module 不输出 endpoint，只输出 diagnostic。
- middleware selector 能解析到 receiver method。
- schema 仍保持 `go-impact/v1alpha1` 顶层稳定。

## 8. 风险和取舍

### 8.1 为什么不先做双快照

deleted route registration 是当前最明确的删除类需求。
完整双快照会引入 base/head 项目加载、facts diff、symbol identity merge 和输出解释，成本更高。
本轮先用 deleted route recovery 覆盖最有价值场景。

### 8.2 为什么不先做 go/types / SSA

真实 BFF 当前 unresolved 主要是可通过轻量类型索引改善的 selector pattern。
直接上 go/types 会引入外部依赖加载、build tags、生成代码、私有 module 解析等工程复杂度。
本轮先解决可控的项目内 receiver/type inference。

### 8.3 go.mod impact 的精度

go.mod 版本变化不代表所有 import usage 都真实受影响。
但对于回归范围分析，保守地把 changed module 的本仓使用点传播到 endpoint 是合理策略。
输出 confidence 使用 `medium/low` 区分 precise 和 fallback。
