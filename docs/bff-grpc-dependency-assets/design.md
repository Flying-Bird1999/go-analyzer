# BFF 接口与 gRPC 依赖资产技术方案

## 1. 背景

`go-analyzer` 当前已经具备项目 symbol、reference、route、HTTP annotation、middleware、
IM event 和 module usage 等事实的抽取与传播能力，也可以把源码变更向上追踪到受影响的
BFF HTTP 接口。

但当前 facts 中没有把“出站 gRPC operation”建模为一等事实，因此无法稳定回答两个真实
问题：

1. 后端通知一个或多个 gRPC 接口发生变化，需要找到当前 BFF 中消费这些 gRPC 的所有
   HTTP 接口，作为回归范围。
2. 从前端定位到一个或多个 BFF HTTP 接口后，需要知道这些接口底层调用了哪些 gRPC。

`go-analyzer` 继续只分析单个 BFF 项目。多个 BFF 项目的场景由平台或业务调用方多次执行
`go-analyzer`，再在外部聚合结果，不在 analyzer 内引入跨仓项目清单或中心化索引。

## 2. 目标

- 增加 BFF endpoint 资产查询，第一版资产只包含 gRPC 依赖。
- 增加从多个 gRPC operation 反查 BFF endpoint 的能力。
- 从项目实际选中的 generated gRPC Go 代码和精确 receiver 类型识别调用。
- 复用现有项目内 symbol reference、route 和 annotation 能力完成双向传播。
- 只输出已经静态证明的正式关系，不输出猜测结果或低置信度候选。
- 输出结构化调用链和源码位置，支持人工 review 和通用平台消费。
- 保持 endpoint 资产结构可扩展，后续可以增加出入参协议、错误码和其他下游依赖。
- 相同项目、build context 和输入必须产生字节级稳定的 JSON。

## 3. 非目标

第一版明确不处理：

- 单次命令分析多个 BFF 仓库。
- 分析后端 gRPC 仓库 diff 或比较两个 proto 版本。
- 下载并比较两个 module 版本的源码或 API。
- 递归分析外部 SDK 内部隐藏的 gRPC 调用。
- 根据 `remote/grpc` 目录、`ServiceClient` 后缀、变量名、注释或方法名猜测 gRPC。
- 把 protobuf request/response 类型引用视为 RPC 调用。
- 输出低置信度候选、模糊匹配或推测 operation。
- 为新命令增加 `schema --type` 类型。
- 实现 endpoint request、response、errors、HTTP dependency 或 event dependency。

## 4. 正确性原则

本能力采用 precision-first 策略。只有同时满足以下条件，才能形成正式的
`BFF endpoint -> gRPC operation` 关系：

1. BFF 当前依赖版本中的 generated gRPC client 源码，能够证明某个 Go client method
   对应一个 canonical gRPC full method。
2. BFF 调用表达式的 receiver 能精确解析到该 generated client 的 Go package 和 client
   interface。
3. 调用表达式位于一个已知的项目 symbol 中。
4. endpoint handler 与该 caller symbol 之间存在可证明的项目内执行调用链。

普通 type/value reference 不属于执行调用链。例如：

```go
var req *order.GetOrderRequest
```

只引用 request 类型不能证明调用了 `GetOrder`。以下调用只有在 `client` 的静态类型精确
命中 generated catalog 后，才会成为正式依赖：

```go
resp, err := client.GetOrder(ctx, req)
```

如果 BFF 只调用外部 SDK，而 SDK 内部再调用 gRPC，第一版不把该 operation 归属于 BFF。
这是经过确认的能力边界。

## 5. gRPC 唯一标识

对外输入、facts 主键和双向查询统一使用 gRPC canonical full method：

```text
/<protobuf-package>.<service>/<rpc-method>
```

例如：

```text
/gopkg.inshopline.com.sc1.app.modules.mc.mc_channel.proto.McChannelService/GetMerchantChannels
```

该值是 gRPC transport 实际使用的 operation identity，不受以下因素影响：

- Go import alias。
- BFF client 变量名。
- wrapper 函数名。
- generated Go package 的目录布局。
- Go method 与 proto rpc method 的大小写差异。

输入格式必须满足：以 `/` 开头，包含一个非空 service 全名和一个非空 method，中间只用
一个 `/` 分隔。RPC method 的拼写和大小写以 generated transport 代码中的值为准，不能
从导出的 Go method 名反推。

内部 operation fact ID 固定为：

```text
grpc:<canonical-full-method>
```

## 6. CLI 设计

### 6.1 Endpoint 资产查询

```bash
go-analyzer endpoint-assets \
  --project /absolute/path/to/bff \
  --endpoint "GET /orders/:orderId" \
  --endpoint "POST /orders"
```

`--endpoint` 可重复传入，格式与 controller annotation 协议对齐：

```go
// @Get /orders/:orderId
```

method 会归一化为大写；path 必须与 analyzer 已链接的 canonical endpoint 精确一致。
第一版不自动转换：

- 运行时路径 `/orders/123`。
- `{orderId}` 风格路径。
- 正则路径。
- 模糊或前缀路径。

### 6.2 gRPC 变更影响源

```bash
go-analyzer impact \
  --project /absolute/path/to/bff \
  --grpc "/package.OrderService/GetOrder" \
  --grpc "/package.OrderService/CreateOrder"
```

`--grpc` 可重复传入，只接受 canonical full method；可与 `--diff` 组合。gRPC operation 是
impact source，不再作为独立的对外查询命令。

### 6.3 公共参数

两个命令复用现有参数：

```text
--project
--format json
--goos
--goarch
--tags
--cgo
--timings
```

输入先去重再稳定排序。`--timings` 继续写入 stderr，stdout 只允许输出最终 JSON。

第一版不扩展 `schema` 命令。输出稳定性通过 Go contract 类型、contract test、golden JSON、
排序规则和本文档共同保证。未来出现明确的机器校验需求时再补 schema。

## 7. Endpoint 资产输出

正向查询输出 endpoint 资产。第一版只包含 `dependencies.grpc`：

```json
{
  "project": {
    "module": "sc1-admin-bff"
  },
  "endpointAssets": [
    {
      "endpoint": {
        "method": "GET",
        "path": "/admin/api/bff-app/mc/merchant/channels"
      },
      "handlers": [
        {
          "id": "func:sc1-admin-bff/controller/merchant::GetChannels",
          "kind": "func",
          "name": "GetChannels",
          "file": "controller/merchant/channel.go"
        }
      ],
      "dependencies": {
        "grpc": [
          {
            "fullMethod": "/gopkg.inshopline.com.sc1.app.modules.mc.mc_channel.proto.McChannelService/GetMerchantChannels",
            "protoPackage": "gopkg.inshopline.com.sc1.app.modules.mc.mc_channel.proto",
            "service": "McChannelService",
            "method": "GetMerchantChannels",
            "clients": [
              {
                "goPackage": "gopkg.inshopline.com/sc1/app/modules/mc/proto/gen/mc_channel",
                "clientType": "McChannelServiceClient",
                "goMethod": "GetMerchantChannels"
              }
            ],
            "chains": [
              {
                "symbols": [
                  {
                    "id": "func:sc1-admin-bff/controller/merchant::GetChannels",
                    "kind": "func",
                    "name": "GetChannels",
                    "file": "controller/merchant/channel.go"
                  },
                  {
                    "id": "func:sc1-admin-bff/service/merchant::GetMerchantChannels",
                    "kind": "func",
                    "name": "GetMerchantChannels",
                    "file": "service/merchant/merchant.go"
                  },
                  {
                    "id": "func:sc1-admin-bff/remote/grpc/mc::McChannelServiceClientGetMerchantChannels",
                    "kind": "func",
                    "name": "McChannelServiceClientGetMerchantChannels",
                    "file": "remote/grpc/mc/channel.go"
                  }
                ],
                "callSite": {
                  "file": "remote/grpc/mc/channel.go",
                  "line": 87,
                  "column": 19
                }
              }
            ]
          }
        ]
      }
    }
  ]
}
```

字段语义：

- `project`：标识当前单 BFF 项目的 module。
- `endpoint`：controller annotation / route link 确定的 canonical method/path。
- `handlers`：该 endpoint 下的全部静态 handler，不假设 method/path 只对应一个 handler。
- `dependencies.grpc`：按 canonical full method 聚合的 gRPC operation。
- `clients`：该 endpoint 实际使用的 generated Go client binding。
- `chains`：endpoint handler 到 gRPC call site 的结构化最短调用链。
- `callSite`：最终 generated client method 调用的项目相对源码位置。

同一个 canonical operation 可能通过多个 generated Go binding 被使用，因此 `clients` 必须
是数组，不能假设 `goPackage + clientType` 唯一。

未来可以在 endpoint asset 中增加：

```json
{
  "contract": {
    "request": {},
    "response": {},
    "errors": []
  },
  "dependencies": {
    "grpc": [],
    "http": [],
    "events": []
  }
}
```

这些字段只是扩展方向，第一版不定义也不输出空占位字段。

## 8. gRPC Impact Source 输出

`impact --grpc` 在既有 impact 文档中增加 `grpcSources`。反向关系复用 endpoint、handler、client
和 chain 结构：

```json
{
  "grpcSources": [
    {
      "grpc": {
        "fullMethod": "/package.OrderService/GetOrder",
        "protoPackage": "package",
        "service": "OrderService",
        "method": "GetOrder"
      },
      "consumers": [
        {
          "endpoint": {
            "method": "GET",
            "path": "/orders/:orderId"
          },
          "relation": "may_call",
          "handlers": [],
          "clients": [],
          "chains": []
        }
      ]
    }
  ]
}
```

即使输入源是 gRPC，`chains` 仍统一按 `endpoint -> gRPC` 方向输出。`relation: "may_call"`
表示静态可达，不承诺每次 HTTP 请求都会执行该调用。

## 9. 内部 Fact 模型

新增两个原子事实。endpoint asset 和 consumer document 都是查询投影，不作为 facts 存储。

### 9.1 `GrpcOperationFact`

```text
ID
FullMethod
ProtoPackage
Service
Method
StreamingMode
ClientBindings[]
Evidence[]
```

`StreamingMode` 区分：

- unary。
- client streaming。
- server streaming。
- bidirectional streaming。

一个 `ClientBinding` 包含 generated Go package、client interface 和 Go method。多个 binding
可以指向同一个 canonical operation；同一个 binding key 指向不同 operation 属于 catalog
冲突，必须失败。

### 9.2 `GrpcCallFact`

```text
ID
CallerSymbol
OperationID
ClientBinding
Span
Evidence[]
```

call fact 归属 enclosing project symbol。evidence 同时记录 BFF call expression 和用于证明
关系的 generated catalog entry。

现有 `facts` JSON 增加稳定排序的：

```json
{
  "grpc_operations": [],
  "grpc_calls": []
}
```

这样可以独立调试识别结果，查询层不承担事实抽取职责。

## 10. 依赖包发现

generated client 必须来自 BFF 当前实际选择的依赖图，不能直接遍历 `$GOMODCACHE` 并猜测
版本。

`internal/project` 增加 dependency package discovery，等价于在项目根目录执行：

```bash
go list -deps -json ./...
```

要求：

- 透传与 source loader 相同的 GOOS、GOARCH、tags 和 CGO。
- 项目处于 Go active vendor mode 时使用 vendor；否则使用 readonly module mode。
- 正确处理 `replace` 和 local replace。
- 不加载 test package，与当前跳过 `_test.go` 的行为一致。
- 至少保留 import path、package dir、参与构建的 Go files、module path/version 和 replace 信息。
- 不修改目标项目的 `go.mod` 或 `go.sum`。

依赖发现必须屏蔽调用环境中可能改变 module 选择的隐式状态：

- 显式设置 `GOWORK=off`。`go-analyzer` 的输入边界是 `--project` 指定的单个 module，不能
  因为开发机恰好存在 ambient `go.work` 而得到与 CI 不同的结果。
- 清理继承环境中的 `GOFLAGS`，再由 analyzer 显式传入 build tags 和 module mode。
- 符合 Go active vendor 条件时显式使用 `-mod=vendor`，其他情况使用 `-mod=readonly`。
- 依赖发现前后校验 `go.mod`、`go.sum` 不变；相关测试必须覆盖只读保证。

第一版不支持用 ambient workspace 覆盖 module 依赖。需要分析 workspace 中的本地依赖时，
应通过目标 module 的 `replace` 明确表达，保证本地和 CI 可复现。

依赖图无法完整解析时，两个新命令都直接失败。禁止降级为扫描 module cache 中其他版本。

## 11. Generated gRPC Catalog

`internal/extract/grpc` 解析选定依赖包中的 generated Go source，不绑定单一 generator 版本。

新版 unary 形式：

```go
const Service_Get_FullMethodName = "/package.Service/Get"

func (c *serviceClient) Get(ctx context.Context, in *Request, opts ...grpc.CallOption) (*Response, error) {
    err := c.cc.Invoke(ctx, Service_Get_FullMethodName, in, out, opts...)
    return out, err
}
```

旧版 literal 形式：

```go
func (c *serviceClient) Get(ctx context.Context, in *Request, opts ...grpc.CallOption) (*Response, error) {
    err := c.cc.Invoke(ctx, "/package.Service/get", in, out, opts...)
    return out, err
}
```

streaming method 通过 generated `NewStream` 调用识别。constructor 返回值用于把 generated
concrete receiver 映射到导出的 client interface：

```go
func NewServiceClient(cc grpc.ClientConnInterface) ServiceClient
```

catalog key：

```text
Go import path + exported client interface + exported Go method
```

catalog value：

```text
canonical full method + proto/service/method + streaming metadata
```

识别必须同时具有 generated marker、client interface/constructor 关系和 `Invoke`/`NewStream`
transport 证据。单纯名为 `ServiceClient` 的业务接口不能进入 catalog。

## 12. BFF gRPC 调用抽取

extractor 遍历项目调用表达式，复用并增强 `astindex` 的静态 receiver type 解析。

必须覆盖真实项目中的：

- package-level generated client 变量。
- struct field 中的 generated client。
- scoped local variable。
- 项目内 constructor/getter 返回值。
- import alias。
- 静态唯一的项目内 interface binding。
- controller 直接调用。
- 项目内 `remote/grpc` wrapper 调用。
- generated controller 中的 `c.getXxxClient().Method()`。

receiver 类型只能来自以下静态证据：

- 声明中的显式 type expression，并通过当前文件 import map 还原完整 package path。
- package-level var、struct field 或 scoped local variable 的显式/可证明初始化类型。
- 项目内 function/method 的声明返回类型。
- getter/constructor 调用对应 callable 的精确返回类型。
- 项目 interface 的全部静态赋值能够收敛到同一个 generated client binding。

不允许根据 getter 名、变量名、field 名或 selector method 名补全 receiver 类型。多个可能
类型无法收敛时不生成正式 fact；如果候选中包含 generated client 且不同候选会映射到不同
operation，则记录稳定的 `grpc_call_ambiguous` 分析错误，严格查询不得返回部分结果。

对每个 selector call 解析：

```text
receiver package + receiver type + selected Go method
```

只有该三元组精确命中 generated catalog 时才生成 `GrpcCallFact`。目录名和 identifier 文本
不参与判断。

第一版不识别 BFF 业务源码直接调用 `grpc.ClientConnInterface.Invoke` 或 `NewStream` 的形式。
这类调用缺少 generated client binding，并且 direct `NewStream` 不能仅凭 full method 可靠
确定 streaming mode，会破坏统一事实模型。真实 BFF 当前均通过 generated client interface
发起调用；未来如出现实际项目，再为 direct transport 定义独立证据模型。

当已经识别到精确 generated client binding，但 catalog 无法映射被调用 method 时，视为
catalog 不完整并失败，禁止静默遗漏。

## 13. Executable Call Graph

现有 `ReferenceFact` 同时服务于影响分析，包含 call、type、value 等多类关系。endpoint 与
gRPC 的消费关系必须使用更窄的 executable call graph：

```text
caller project symbol -> called project symbol
caller project symbol -> gRPC operation
```

允许进入图的边：

- direct call。
- 已解析 receiver method call。
- 静态解析的 function-value invocation。
- 精确 `GrpcCallFact` terminal。

禁止进入图的边：

- type reference。
- 普通 value reference。
- struct field type。
- protobuf message usage。
- 未解析的动态 dispatch。

route/annotation link 只负责连接 endpoint identity 和 handler symbol，不伪装成普通函数调用。

## 14. 双向查询算法

### 14.1 Endpoint 到 gRPC

对每个精确 endpoint 输入：

1. 在当前 build context 下解析全部 route/annotation linked handlers。
2. 从每个 handler 开始执行 forward BFS。
3. 只沿 executable project-call edge 遍历。
4. 每遇到一个 gRPC terminal，记录 operation、binding 和 call site；其他分支继续遍历。
5. 按 canonical full method 汇总 terminal。
6. 每个不同 gRPC call site 保留一条确定性的最短 symbol chain。

### 14.2 gRPC 到 Endpoint

对每个 canonical operation 输入：

1. 查找当前 BFF 中全部对应的 `GrpcCallFact`。
2. 从每个 caller symbol 开始执行 reverse BFS。
3. 只沿 executable project-call edge 的反向索引遍历。
4. 将到达的 handler 通过 route/annotation link 转为 endpoint。
5. 按 endpoint method/path 汇总结果。
6. 输出时把 chain 统一转换为 endpoint 到 gRPC 方向。

遍历没有任意 `maxDepth`，通过 visited state 处理递归和环。同一个 handler/call-site pair
存在多条等长路径时，按 symbol ID 字典序选择一条，保证稳定且限制 JSON 大小。不同 call
site 必须分别保留。

核心不变量：

```text
endpoint-assets(A) 包含 gRPC B
当且仅当
impact --grpc B 的 grpcSources 包含 endpoint A
```

该不变量只在相同项目快照和 build context 下成立。

## 15. 失败策略

两个命令采用 all-or-nothing：stdout 要么输出完整正式 JSON，要么不输出 JSON。

- 缺少必填参数、endpoint 格式非法、canonical gRPC 格式非法：失败。
- endpoint 无法在项目中解析：失败，不能与“endpoint 存在但没有 gRPC”混淆。
- endpoint 存在但不依赖 gRPC：正常返回 `"grpc": []`。
- canonical gRPC 格式合法但当前 BFF 无调用：正常返回 `"consumers": []`。
- 多个 endpoint 输入中任意一个不存在：整批失败。
- active project source 解析失败：失败。
- dependency discovery 或 generated source 解析失败：失败。
- catalog binding 冲突：失败。
- 已确定 generated binding 但 operation 无法映射：失败。
- 不把候选项或低置信度关系混入正式数组。

所有错误和 timings 写入 stderr。必须在全部输入和结果校验完成后一次性渲染 stdout，禁止
先输出部分 JSON。

错误必须携带稳定 code，不能要求调用方解析自然语言 message：

```text
invalid_endpoint
endpoint_not_found
invalid_grpc_method
project_load_failed
dependency_load_failed
grpc_catalog_failed
grpc_call_ambiguous
```

CLI stderr 使用稳定前缀：

```text
error_code=<code> message=<human-readable-message>
```

Go app API 返回带 `Code` 的 typed error；CLI 只负责稳定渲染。message 面向人工排查，可以
演进；code 属于命令契约。成功 JSON 不增加 diagnostics 或候选字段。

## 16. 稳定排序

所有 facts 和公开输出必须字节级稳定：

- endpoint result 按 method、path 排序。
- gRPC result 按 canonical full method 排序。
- handler 和 symbol node 按稳定 symbol ID 排序。
- gRPC dependency 按 canonical full method 排序。
- client binding 按 Go package、client type、Go method 排序。
- consumer 按 endpoint method、path 排序。
- chain 按 terminal call-site file、line、column、symbol ID 序列排序。
- input、binding、call site、handler 和 chain 全部去重。

所有输出路径必须是 slash-normalized 的项目相对路径。禁止暴露 module cache 或 workspace
绝对路径。

## 17. 模块边界

```text
internal/project
  dependency package discovery
  锁定项目实际选择的 package dir/version

internal/extract/grpc
  generated catalog
  BFF gRPC call-site extraction

internal/facts
  GrpcOperationFact
  GrpcCallFact

internal/graph
  executable forward/reverse call graph

internal/dependency
  FindEndpointAssets
  FindGrpcConsumers
  双向一致性与最短链算法

internal/output
  endpoint-assets JSON
  impact grpcSources JSON
  排序与 contract

internal/app
  endpoint asset 与 impact source pipeline 编排

cmd/go-analyzer
  endpoint-assets
  impact --grpc
```

新增可观测阶段：

```text
dependency_list
grpc_extract
dependency_query
dependency_render
```

两个 app entry 复用一次 facts 构建：

```text
RunEndpointAssets
RunGrpcConsumers
```

`internal/dependency` 负责查询语义；`internal/output` 只做投影、排序和渲染。两个命令都不
创建 synthetic diff 或 `ChangeFact`。

当前 `facts` 与 `impact` 共享 `buildFacts`，因此必须显式增加内部 feature options，不能
在共享流水线中无条件执行 dependency discovery：

```text
buildFactsOptions.grpcMode = off | diagnostic | strict
```

- 未提供 `--grpc` 的 `impact` 使用 `off`：完全跳过 dependency list 和 gRPC extraction，保持
  现有 diff 性能与失败语义。
- `facts` 使用 `diagnostic`：执行 gRPC extraction；失败时写入稳定 diagnostics，并保留其他
  facts，符合 facts 现有的调试定位职责。
- `endpoint-assets` 和提供 `--grpc` 的 `impact` 使用 `strict`：dependency、catalog 或相关歧义
  失败直接返回 typed error，不进入 query/render。

只有 mode 非 `off` 时才注册和记录 `dependency_list`、`grpc_extract` timing。

## 18. 测试矩阵

### 18.1 Generated Catalog

- 新版 `FullMethodName` 常量。
- 旧版 literal `Invoke`。
- unary、client streaming、server streaming、bidirectional streaming。
- Go method 与 canonical rpc method 大小写不同。
- 不同 service 存在同名 method。
- 一个 operation 对应多个 Go binding。
- 一个 binding key 错误映射多个 operation。
- local replace、ambient workspace 被隔离，以及 active vendor mode。
- `GOFLAGS` 中 module mode/build tag 不得污染 analyzer 的显式参数。
- dependency discovery 前后 `go.mod`、`go.sum` 零修改。
- 没有 generated transport 证据的 `ServiceClient` decoy。

### 18.2 Call Extraction

- package-level client variable。
- controller direct call。
- project-local gRPC wrapper。
- multi-layer service forwarding。
- import alias。
- struct field 和 scoped local value。
- constructor/getter return type。
- generated controller getter chain。
- statically unique interface binding。
- ambiguous interface binding 指向多个 generated operation 时严格失败。
- getter/field/method 名相同但没有类型证据时不得按名称补全。
- BFF 业务源码 direct `Invoke` / `NewStream` 不得绕过 generated client binding 进入结果。
- 只引用 protobuf message，不调用 RPC。
- HTTP/Redis client 存在同名 method。
- dynamic full method 无法求值。
- `_test.go` mock 被排除。
- external SDK hidden gRPC 被排除。

### 18.3 Graph 与 Query

- endpoint 直接调用 operation。
- endpoint 经过 service 和 remote wrapper。
- 一个 endpoint 对应多个 operation。
- 一个 operation 被多个 endpoint 消费。
- 同一个 operation 存在多个 call site。
- 相同 endpoint method/path 存在多个 handler。
- 递归与调用环。
- 重复输入去重。
- annotation method 归一化和 path 精确匹配。
- 空 dependency 和空 consumer。
- batch all-or-nothing failure。
- typed error code 与稳定 stderr 前缀。
- 每条正式关系的 forward/reverse invariant。

### 18.4 Contract 与 CLI

- endpoint asset golden JSON。
- `impact --grpc` source JSON。
- facts 中 operation/call facts。
- structured chain 和 source position。
- 只输出项目相对路径。
- `--timings` 的 stdout/stderr 隔离。
- build-context 参数一致性。
- `impact` schema 包含 `grpcSources`，endpoint assets 不增加 schema 类型。
- 未提供 `--grpc` 的 `impact` 使用 grpc mode `off`；提供 `--grpc` 时 dependency/catalog 故障必须使该次 impact 原子失败。
- `facts` diagnostic mode、endpoint assets strict mode 与 `impact --grpc` strict mode 的失败传播差异。
- 现有 facts、impact、route、IM 和 output 测试无回归。

## 19. 真实 BFF 验收

扩展 checked-in smoke baseline：

- `sl-sc1-admin-bff`：覆盖 package-level client、project wrapper、controller direct call 和
  generated controller getter chain。
- `sl-sc2-admin-bff`：覆盖旧版 generated code，以及 Go method 与 canonical rpc method
  大小写不一致。
- `sl-sc1-bff-service`：覆盖 controller direct call 和 project-local wrapper。

baseline 记录：

- operation 数量。
- gRPC call-site 数量。
- endpoint-operation relation 数量。
- 若干固定 endpoint 到 operation 的完整链路。
- 对应 operation 的反向 consumers。

验收必须断言固定链路满足双向不变量，并继续运行全部现有真实项目 facts/impact/IM/route
smoke。性能通过 timings 记录和 review，不设置容易抖动的 wall-clock 硬阈值。

## 20. 验收标准

满足以下条件后能力才算完成：

1. 两个独立命令支持重复输入并输出稳定 JSON。
2. 两代已验证 generator 的 unary 和 streaming client 都能映射到 exact canonical operation。
3. 真实 BFF 中 direct、wrapper 和 generated getter 调用不依赖命名猜测即可识别。
4. 所有正式 endpoint-operation 关系都满足双向查询不变量。
5. endpoint asset 只输出当前实现的 `dependencies.grpc`，不输出未实现资产字段。
6. 输入或分析不完整时不产生 partial stdout。
7. unit、golden、现有 regression 和三个真实 BFF smoke 全部通过。
