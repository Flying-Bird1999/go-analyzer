# 服务入口契约影响分析设计

> 状态：已实现并通过真实项目验证。更新于 2026-07-14。
> 范围：扩展现有 `grpc-impact`，面向单个后端服务项目输出代码变更的入站业务影响。

## 1. 结论

当前 `grpc-impact` 已能可靠回答“变更影响哪些已注册 gRPC operation”，但 `sc1-server` 和 `sc2-server` 同时暴露 lego HTTP、Dubbo 和 XXL-Job；只输出 gRPC 会遗漏业务实际可被调用的入口。

保留 `grpc-impact` 命令名和单项目边界，将其升级为**服务入口契约影响分析**：一次 diff 分析输出所有静态可证明的 gRPC、HTTP、Dubbo、Job 契约。命令名保留是为了兼容现有接入和“gRPC Provider 项目”这一入口分类，输出语义不再仅限 gRPC。

本期不新建 HTTP、Job 或 Dubbo 子命令，也不处理 Pulsar/IM 出站事件，避免让调用方拼接多份孤立 JSON。

## 2. 真实项目依据

| 契约 | `sc1-server` | `sc2-server` | 可行性 |
| --- | --- | --- | --- |
| gRPC provider | generated `RegisterXxxServer` + provider 注册器 | 同类 generated 注册与容器包装 | 已实现 |
| HTTP | `lego.RouterGroup`、`RegisterAPI`、group/wrapper | `Register*Routes`、`lego.RouterGroup` | 复用 route facts，高 |
| Dubbo provider | `ExportProviders` -> `*ApiExport` -> `ServiceConfig` + `SetProviderService` | 同类 `*ApiExport` 与 `SetProviderService` | 规则稳定，中高 |
| XXL-Job | `InitJob() map[string]JobListener` 汇总 | `tasks["name"] = handler` | 规则稳定，高 |
| Pulsar producer | 统一 `Producer/ProducerFill/ProducerDelayed` | 当前样本以 consumer 为主 | 后续待办 |

Pulsar consumer 也不属于本期入口契约。不能因代码中出现 `Subscribe` 就输出业务影响结论。

明确待办：后续单独设计 Pulsar/IM producer 与 consumer 的事实、传播方向、topic identity 和跨仓串联。本期只预留 contract kind 的扩展位置，不实现 extractor、传播或 JSON 字段。

## 3. 术语与边界

服务入口契约是服务进程之外可调用、且在代码中实际注册的入口：

| kind | 含义 |
| --- | --- |
| `grpc_operation` | 已注册 gRPC 服务方法 |
| `http_endpoint` | 已注册 HTTP method/path 与 handler |
| `dubbo_method` | 已导出的 Dubbo interface/method |
| `job` | 已注册 XXL-Job 名称与 handler |

HTTP 仅指服务对外注册的 route，不把普通出站 HTTP 请求误作本服务暴露的接口。Dubbo consumer、出站 gRPC、Pulsar/IM、数据库和缓存是依赖事实或后续能力，不是本期影响终点。

- 仍只分析一个代码仓库，不自动串联 `gRPC -> BFF -> 前端`。
- 不执行代码、不读取运行时 Nacos 配置、不推断反射或动态注册。
- 无法唯一绑定 handler、service 或 method 时，不输出为正式结论。
- 不做 proto 字段兼容性分类；这是 gRPC 契约变更分析的独立后续能力。

## 4. 证据标准

正式结论必须包含从变更根到入口契约的可复核静态链路，且每种协议有额外注册证据。

| kind | 最低证据 |
| --- | --- |
| gRPC | `ServiceDesc`、实际 `RegisterXxxServer`、唯一 concrete provider、匹配 handler |
| HTTP | 实际 `GET/POST/...` 调用、可追溯 group path、唯一 handler/wrapper 解包结果、route function 接入项目 web 启动注册链 |
| Dubbo | `ServiceConfig.Interface`、明确 `Methods[].Name`、`SetProviderService` 的 concrete provider、`MethodMapper` 或唯一 Go method 映射、export function 接入 provider 启动链 |
| Job | 真实注册 map 的静态 task name、唯一 handler function/method、任务集合接入 `jobx` 启动注册链 |

路径由配置键或拼接决定时，保留 `registration` 证据和 `identityResolution: "symbolic"`，但不把猜测出的最终 URL 写成正式 identity。静态字面量和可解常量标为 `static`。

## 5. 分析模型

保持现有 facts-first 结构：各协议保留自己的原子 fact，由影响层建立统一 terminal projection。不要在 `facts.Store` 中再复制一份通用 `ExternalContractFact`，否则协议 fact 与通用 fact 会形成双写和漂移。

```text
project source
  -> project / AST / symbols / references
  -> grpc, route, dubbo, job extractors
  -> GrpcProviderFact / RouteRegistrationFact / DubboProviderFact / JobRegistrationFact
  -> serviceimpact terminal projection
  -> reverse graph query
  -> grpc-impact JSON
```

统一 projection 至少包含：

```text
kind, identity, identityResolution,
entrySymbol, registration, terminalRelation,
protocol metadata, confidence
```

已有 `GrpcProviderFact` 和 route registration facts 直接接入 projection。新增 `DubboProviderFact`、`JobRegistrationFact` 只记录协议原子证据；图传播与 JSON 投影继续由独立模块完成。

调用图方向为 `caller -> callee`。从变更符号开始，反向可达的已注册 handler 是影响结论。这与当前 gRPC Provider impact 的传播方向一致：`external entry -> ... -> changed symbol`。

terminal relation 保持协议语义，不修改现有 gRPC 值：gRPC 使用 `exposed_grpc_operation`，HTTP 使用 `exposed_http_endpoint`，Dubbo 使用 `exposed_dubbo_method`，Job 使用 `registered_job`。它们都表示静态 `may reach changed`，不承诺运行时每次调用都会执行变更点。

不以名称、目录或注释推测关系；无法证明的链路宁可不输出。仅存在于未接入启动链的 route/export/task 注册事实不进入正式 summary。

### 协议抽取

**HTTP**：复用当前独立的 route facts 与 linker，不复制 BFF endpoint annotation 语义。支持 group、HTTP verb、已知 middleware wrapper 与 function value handler。动态 group prefix 记录来源表达式和配置键。静态 path 的 identity 为 `METHOD resolvedPath`；动态 prefix 的稳定 ID 使用注册位置，输出 `localPath` 与 `pathExpression`，不伪造完整 URL。

**Dubbo**：从启动链上的 `ExportProviders` 进入 `*ApiExport`，关联 `Provider.Services[key] = ServiceConfig`、`SetProviderService(instance)`、`MethodMapper` 和具体 method。identity 为 `interface@version/method`；version 动态时保留 symbolic 版本而不伪造值。

**Job**：识别参数或返回值为 `map[string]jobx.TaskFunc`、`map[string]JobListener` 的注册函数及其静态 map 赋值。identity 为 job name，handler 为可调用符号。动态 task name 或无法唯一绑定的 handler 不进入正式结果。

三类新增入口都需要 registration liveness：注册函数必须能通过项目引用、容器绑定或明确 framework adapter 连接到启动根。无法证明启动连接时仅保留诊断事实。这一规则不要求执行环境条件为真，只证明代码确实接入服务启动结构。

## 6. 输出契约

`grpc-impact` 只输出一份按协议分组的 JSON。协议分组是唯一的正式聚合方式，不保留 gRPC 专属镜像字段，避免同一结论在两个数组中重复出现。

```json
{
  "summary": {
    "grpc": [
      {
        "kind": "grpc_operation",
        "identity": "/shopline.trade.api.TradeService/EstimateCost",
        "identityResolution": "static"
      }
    ],
    "dubbo": [],
    "http": [
      {
        "kind": "http_endpoint",
        "method": "POST",
        "path": "/bff/api/channel/email/register",
        "identityResolution": "static"
      }
    ],
    "job": []
  },
  "fileSources": [],
  "entrySourcesSummary": {
    "grpc": [],
    "dubbo": [],
    "http": [],
    "job": []
  }
}
```

每个 `fileSources[]` 保留原始 diff、现有 symbol impact tree 和同形的 `impacts: {grpc,dubbo,http,job}`。`entrySourcesSummary` 也以同一分组提供 `entry -> file/module source` 的反向汇总。四个 key 恒定存在，空结果为 `[]`。每个契约携带 `registration`，使 Nexus 或回归系统无需重新理解 AST 即可审计结论。

## 7. 实施阶段与复杂度

| 阶段 | 内容 | 状态 |
| --- | --- | --- |
| A | 通用 terminal projection、JSON 兼容投影、schema/golden | 已完成 |
| B | HTTP route 与 Job 注册 | 已完成 |
| C | Dubbo provider | 已完成 |
| D | 统一 contract projection、真实项目回归 | 已完成 |

实现继续复用反向图，协议差异收敛在 extractor 与 terminal projection。Dubbo method 配置和 service 级配置使用不同 span/change kind：前者只影响单 method，后者影响该 interface 的全部 method。

真实项目已分别构造 HTTP、Dubbo、XXL-Job 三文件 diff。`sc1-server` 精确得到 `POST /mc/sendMessage`、`ChatBoxAdviceApi/getAdvice`、`comment_match`；`sc2-server` 精确得到 symbolic `GET /count/:type`、`StoreChannelConfigApi@1.0.0/queryLoginConfig`、`UnReadCountScanJob`。同 interface 的 sibling Dubbo method 未被带出，验证后两个项目工作区已恢复。

## 8. 验收标准

1. 每个正式契约都能定位到注册位置和完整 symbol 链。
2. 直接改动 HTTP/Dubbo/Job handler 时不会报出同 service 的 sibling method。
3. 共享业务 helper 的改动可同时得到多个协议的独立结论，并按 source 去重。
4. 动态 path 不伪造最终值；JSON 明确显示 symbolic 状态和原始表达式。
5. `go test ./...`、schema/golden、命令级 fixture 均通过。
6. 在 `sc1-server`、`sc2-server` 分别覆盖 HTTP、Dubbo、Job 的真实范式；无对应范式的协议不得虚构“已验证”。
7. 旧 gRPC JSON 字段、排序、terminal relation 和 schema 继续通过兼容测试。

## 9. 后续待办

- Pulsar/IM producer 和 consumer，以及 topic 到下游消费者的跨仓编排。
- 跨仓把 gRPC Provider 结果喂给 BFF `impact --grpc`，再接 ts-analyzer。
- 出站 HTTP、出站 gRPC、Dubbo consumer 的依赖资产清单。
- Proto/OpenAPI/Dubbo IDL 的兼容性分类和入出参、错误码资产。
