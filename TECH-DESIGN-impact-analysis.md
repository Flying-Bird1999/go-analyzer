# Go 服务影响范围分析能力 · 技术方案

## 1. 背景与要解决的问题

前端 `React + TypeScript` 项目已经验证过一套影响范围分析模型：

```text
代码 diff → 找出变更的语义节点 → 沿依赖往上传播 → 落到业务入口
```

它回答的是"这次改动会影响哪些入口、要回归哪些范围"。现在要把同一套方法用到 **Go 服务**上。

**现在的痛点**：改完一段 Go 代码，没人说得准它挂在哪几个对外接口下面。于是要么凭经验猜（容易漏测），要么保守地全量回归（成本高）。我们要做的是一个**只根据代码里能证明的事实**来回答问题、不猜运行时行为的分析器。

它面向**单个 Go 服务项目**，处理两类项目、回答两个不同的问题：

| 项目类型                                  | 要回答的问题                                                                                            | 分析结果                      |
| ----------------------------------------- | ------------------------------------------------------------------------------------------------------- | ----------------------------- |
| **BFF 项目**                        | 这次 diff（或某个上游 gRPC 接口）会影响哪些**对外 HTTP 接口**和**主动推给前端的 IM 消息**？ | HTTP 接口 / IM event          |
| **后端服务项目**（sc1-server 这类） | 这次 diff 会影响哪些**对外暴露的入站接口**？                                                      | gRPC / HTTP / Dubbo / XXL-Job |

这两类项目共用一整套公共底座（读代码、建索引、引用分析、反向引用图、路由抽取等），只有"认哪种入口、怎么落到结论"这部分按项目类型分开。文档 §2 讲公共底座，§3 讲 BFF 怎么走，§4 讲后端服务怎么走。

### 目标项目

- BFF：`sl-sc1-admin-bff`、`sl-sc1-bff-service`、`sl-sc2-admin-bff`
- 后端服务：`sc1-server`、`sc2-server` 这类 gRPC / Dubbo 服务端

它们大致都是 `router → controller → service → remote` 的分层，用 `lego.RouterGroup`（类似 Gin）注册路由，但前缀写法、wrapper、中间件各仓不一样。所以分析器不能为某一个仓写死规则，要能识别这一类项目的通用写法。

### 设计原则

1. **只报能证明的关系**。反射、运行时注入、外部 SDK 内部调用、动态路由这些静态看不透的，一律不猜；看不透的地方降级成诊断或"未解析"标记，不混进结论。
2. **事实优先（facts-first）**。抽取层只负责从代码里提取事实，图和查询层只读事实，输出层只做稳定的格式投影。三层之间只通过一个共享的事实仓库 `facts.Store` 传数据。
3. **输出稳定可回归**。对外 JSON 有 schema 约束，结论和诊断信息分开放。
4. **业务方零配置**。路由 / 注解 / 中间件的写法由分析器内置识别，不需要业务方写语法配置。
5. **宁缺毋滥**。少报一个不确定的关系，也不要报一个"看着对其实错"的结论。

---

## 2. 公共底座（两类项目共用）

### 2.1 整体怎么跑

一句话：**先把源码变成"可查询的事实"，再按调用方选的命令分成两条独立的分析链路各自出结果**。分三段：

- **第一段 · 读代码**：加载项目源码，建立符号索引，同时解析 diff 并校验它确实已经打到当前源码上。
- **第二段 · 提事实**：把符号、引用关系、被 diff 改动的节点等，统一抽成事实存进 `facts.Store`。这一段和链路无关。
- **第三段 · 分链路出结果**：从事实仓库开始，按调用方选择的命令（`bff-impact` 走 BFF 链路、`grpc-impact` 走后端服务链路）走各自的传播 / 投影链路，产出稳定 JSON（这一段才是两条链路真正分开的地方，前两段是共用的）。

> **分叉依据是命令，不是项目类型的自动探测。** 分析器不会去"嗅探"这个仓是 BFF 还是后端服务——是调用方通过运行哪个命令来选定链路。两类项目恰好各自对应一个命令，但决定权在命令，不在探测。

```mermaid
flowchart TB
    SRC["输入：变更后的源码 + 已应用的 diff"]

    subgraph S1["第一段 · 读代码（与链路无关）"]
        direction TB
        LOAD["加载项目源码<br/>（包 / 文件模型、按编译条件过滤文件）"]
        IDX["建符号索引<br/>（函数/类型/常量/变量的稳定 ID + 类型推断）"]
        DIFFV["解析 diff + 校验已应用<br/>（删除块留待 impact 层恢复被删声明）"]
    end

    subgraph S2["第二段 · 提事实（与链路无关）"]
        direction TB
        SYM["抽公共事实：符号、引用关系、变更根、go.mod 变更"]
        STORE[("facts.Store<br/>事实仓库，唯一的数据总线")]
        SYM --> STORE
    end

    SRC --> LOAD --> IDX --> SYM
    SRC --> DIFFV --> STORE

    STORE --> FORK{"运行哪个命令？"}
    FORK -->|bff-impact| BFF["BFF 分析链路（§3）"]
    FORK -->|grpc-impact| SVC["后端服务分析链路（§4）"]

    BFF --> OUT1["JSON：受影响的 HTTP 接口 / IM event"]
    SVC --> OUT2["JSON：受影响的 gRPC / HTTP / Dubbo / XXL-Job 入站接口"]
```

**两条链路最大化复用公共底座**——源码加载、符号索引、diff、引用抽取、`route`/`link`、反向引用图、输出外壳都是共享的。**分叉只发生在两处**：

- **领域抽取器**：BFF 用 `annotation` / `im` / `grpc`(client)，后端服务用 `grpc`(server) / `dubbo` / `job`——各认各的入口类型。
- **传播 / 投影模块**：BFF 走 `impact`，后端服务走 `serviceimpact`——各自决定"怎么落到结论、输出什么结构"。

也就是说，底层设施尽量共用，只有"认哪种入口、怎么把改动落成对外契约"这层按项目类型分开。这也是为什么文档把两条链路分开讲（§3、§4），但它们并不是两套独立实现。

### 2.2 依赖分析：怎么把一处改动追到对外接口

这是底座里最关键的一个设计点，两条链路都靠它。核心就是做**依赖分析**——建一张"谁用了谁"的引用关系图，从改动的地方顺着这张图往上找，直到找到对外接口。

为什么不能只看"函数调用"？Go 服务里，controller 常常**不是被调用的，而是被当成一个值传进注册函数**。比如：

```go
broadcastGroup.GET(
    "/record",
    sa2.ControllerWithReqResp(broadcast.BroadcastAdminApi.QueryBroadcastRecord),
)
```

这里 `QueryBroadcastRecord` 没有被"调用"，它是作为**函数值**传给了 `GET`。如果只记"函数调用"关系，就追不到这种"被当值传参"的注册关系。

所以我们记录的引用关系覆盖**三种**，比只看调用更全：

| 引用关系 | 代码长什么样 | 例子 | 为什么要记 |
| --- | --- | --- | --- |
| `call`（调用） | 被调用 | `QueryBroadcastRecord()` | 常规调用依赖 |
| `value`（取值） | 被当值 / 函数值引用（含传参、赋值） | `GET("/x", ...QueryBroadcastRecord)` | controller 被注册函数当值传走，靠它才追得到路由 |
| `type`（类型） | 被当类型用（参数、返回值、字段、组合字面量、泛型参数） | `func(...) *OrderResp` 引用了 `OrderResp` | **改了 struct/类型会真实影响接口的出入参**，必须追 |

第三种（类型）值得单独强调：如果改了一个 struct 或类型——比如给 `OrderResp` 加个字段、改个 tag——它本身不是函数，但它被某个 controller 当**返回值/请求体**用了，这次改动就实打实影响了那个接口的响应结构。所以类型改动也要沿"谁把它当类型用了"这条关系往上追，一直追到把它当出入参的 controller，落到接口。

一句话：一个 service 方法改了、或一个 struct 改了，都能顺着"谁用了它"一路往上——追到 controller，再追到路由注册那一行，最后落到对外接口。

两条链路都从**变更根**（diff 改动映射出的起点）出发，沿这张图遍历，区别在**终点不同**——也在**遍历方向不同**（见 §3.2 的两个方向）。

### 2.3 diff 必须已经应用到当前源码（不是改动前的旧代码）

两条链路的 `--diff` 都要求：**`--project` 指向的源码，必须是这份 diff 应用之后的版本**（也就是"改动后"的代码，不是改动前）。因为分析要靠 diff 里的行号去源码里定位改了哪个函数/类型，如果源码还是旧的、或根本没打这个 diff，行号就对不上，会定位到错误的位置、算出一个"看着有效其实是错的"结论。所以底座会先校验：diff 过期、为空、路径越界、或改动的文件有语法错误，一律直接报错退出，不带病往下算。

**被删除声明的恢复（重点）。** 有一类改动很特殊：**删掉一个接口**。删除后，源码里已经没有这个函数了——它不在当前代码里，正常遍历根本碰不到它，这次删除就会被漏掉。为此底座会从 diff 的**删除块**（`-` 开头的行）里，把被删掉的那段声明**重建出来**（单行、多行都支持），当成一个"曾经存在、现在被删"的节点放回分析。这样"这个 controller/路由被删了 → 对应的 HTTP 接口下线了"才能作为一条影响被传播出来，而不是无声消失。输出里这类会用带 `deleted_` 前缀的关系标出来，让调用方知道是删除影响。

### 2.4 整体架构分层（模块怎么串起来）

这一节就是整个项目的**架构分层**：数据自上而下流经四层——**读代码 → 提事实 → 建图/关联 → 分链路传播 → 输出**，`facts.Store` 是中间的总线，之后按命令分到两个传播模块。

```mermaid
flowchart TB
    subgraph READ["① 读代码"]
        PROJ["project<br/>加载源码"]
        AST["astindex<br/>符号索引"]
        DIFF["diff<br/>解析+校验已应用"]
        PROJ --> AST
    end

    subgraph EXTRACT["② 提事实（抽取器）"]
        direction LR
        REF["reference 引用关系（共用）"]
        ROUTELINK["route + link 路由抽取/关联（共用）"]
        GOMOD["gomod 模块变更+使用点（共用）"]
        BFFX["annotation / im / grpc(client)<br/>（BFF 专用）"]
        SVCX["grpc(server) / dubbo / job<br/>（后端服务专用）"]
    end

    STORE[("facts.Store")]
    GRAPH["③ graph 只读查询视图<br/>ReverseGraph / RouteGraph / CallGraph / IMGraph（共用）"]

    AST --> REF
    AST --> ROUTELINK
    AST --> BFFX
    AST --> SVCX
    DIFF -->|go.mod 的 diff：哪些模块变了| GOMOD
    AST -->|把模块变更映射到本仓使用点| GOMOD
    DIFF -->|改动映射成变更根| STORE
    REF --> STORE
    ROUTELINK --> STORE
    GOMOD --> STORE
    BFFX --> STORE
    SVCX --> STORE

    STORE --> GRAPH
    GRAPH --> IMPACT["④ impact<br/>BFF 传播"]
    GRAPH --> SVCIMPACT["④ serviceimpact<br/>后端服务传播"]
    IMPACT --> OUTPUT["⑤ output<br/>稳定 JSON + schema"]
    SVCIMPACT --> OUTPUT

    APP["app：命令编排——只加载一次事实，按命令决定走 impact 还是 serviceimpact"]
```

> **关于 gomod 的两个输入**（容易混淆）：模块**"哪些变了"**来自 **go.mod 的 diff**（新增/删除的 require/replace 行），不是从符号索引里推的；`gomod` 再用项目源码 + 符号索引把这些模块变更**映射到本仓的使用点**（哪些文件 import 了它），才能继续往接口传播。所以它同时吃"diff"（变更）和"项目索引"（使用点）两个输入。

各模块职责与产出：

| 模块                  | 做什么                                                                                          | 产出什么                       |
| --------------------- | ----------------------------------------------------------------------------------------------- | ------------------------------ |
| `project`           | 加载项目，建立包 / 文件模型，按编译条件（GOOS/tags 等）过滤掉不参与编译的文件                   | 可遍历的项目源码模型           |
| `astindex`          | 给每个声明（函数/方法/类型/常量/变量）建稳定 ID，做类型和值类型推断                             | 符号索引，供引用解析用         |
| `diff`              | 解析 unified diff，校验它确实已应用                                                             | 变更块、删除块（`-` 行原文）   |
| `facts`             | 定义所有事实类型、事实仓库`facts.Store`                                                       | 事实的存取接口（数据总线）     |
| `extract/reference` | 扫描项目内的调用 / 类型引用 / 取值引用                                                          | "谁引用了谁"的引用事实         |
| `extract/route` + `link` | 识别路由注册语法，并把路由 / 注解 / handler 关联起来（**两条链路共用**：BFF 用它出 HTTP 接口，后端服务也用它出 HTTP 入站接口） | 路由事实 + "路由↔handler↔注解"关联 |
| `extract/gomod`     | 解析 go.mod 的 require/replace 变更，定位到本仓的使用点                                         | 模块变更 → 本仓使用点的关联   |
| `graph`             | 用事实建多个只读查询视图（ReverseGraph 反向引用 / RouteGraph 路由域 / CallGraph 调用 / IMGraph），并保证遍历顺序稳定（结果可回归）；只读 facts，不改 facts | 传播图（两条链路都从它遍历）   |
| `dependency`        | 建在 `graph` 之上的 **endpoint ↔ gRPC 双向查询层**（不是抽取器，也不物理绑定 BFF）；`bff-impact --grpc` 与 `endpoint-assets` 都走它 | endpoint↔gRPC 查询结果 |
| `impact`（含删除恢复） | 从 `ChangeFact` 构造传播树；并在此层从 diff 删除块**恢复被删声明**（重解析 `-` 行文本、回填符号索引） | BFF 传播树 |
| `diagnostics`       | 记录抽取失败或降级（比如某个非变更文件解析不了）                                                | 诊断项（不进结论，走调试通道） |
| `config`            | 解析可选的模块变更过滤配置，字段严格校验                                                        | 过滤规则                       |
| `app`               | 命令编排：只加载一次事实，按命令决定走哪条链路                                                  | 对外命令的执行入口             |
| `output`            | 把链路结果投影成稳定 JSON，并能导出 JSON Schema                                                 | 对外 JSON + schema             |
| （BFF 专用抽取器）    | `annotation` / `im` / `grpc`(client)；传播走 `impact`，依赖查询走 `dependency`      | 见 §3                         |
| （后端服务专用抽取器）| `grpc`(server) / `dubbo` / `job`；传播走 `serviceimpact`                             | 见 §4                         |

**一条硬约束**：任何模块都不能直接去读另一个**抽取器**的内部 AST 状态。跨模块交换数据只走读代码层的两条共享总线——`facts.Store`（事实总线）和 `astindex.Index`（符号索引总线）。`reference` / `link` / `gomod` / `impact` 会直接读 `astindex.Index`（它是共享索引，不是某个抽取器的私有状态），其余一律通过 `facts.Store`。这样加新能力时不会牵一发动全身。

### 2.5 事实模型

事实仓库里的主要事实（公共部分）：`SymbolFact`（声明身份）、`ReferenceFact`（引用关系）、`ChangeFact`（diff 映射出的变更根）。BFF 和后端服务各自还有专属事实，分别在 §3、§4 讲。

结论只表达**能静态证明的关系**：一条传播关系要么能证明、进结论，要么证明不了、降级成诊断或"未解析"标记（见设计原则 1、5），不用一个"打折的把握度"去表达"可能有关系"。这样调用方拿到的每一条影响都是可信的，而不需要再去猜哪几条要打问号。

### 2.6 命令怎么把这些模块串起来（编排真相）

上面讲的是"有哪些模块"，这一节讲"一次运行里它们按什么顺序被谁调用"——这是 `app` 层的编排职责，也是理解"为什么同一套底座能出两种结果"的关键。

**哪个命令跑哪些抽取器、gRPC 抽取用什么模式：**

| 命令 | 领域抽取器 | gRPC client 抽取模式 | 服务入口抽取（job/dubbo/gRPC-server） | 传播/投影 |
| --- | --- | --- | --- | --- |
| `bff-impact`（仅 `--diff`） | annotation / route+link / reference / im | **off**（不加载 gRPC 依赖，保 diff 性能与语义） | 不跑 | `impact` |
| `bff-impact`（带 `--grpc`） | 同上 | **strict**（失败即 typed error，不出半份 JSON） | 不跑 | `impact` + `dependency` 反查 |
| `endpoint-assets` | annotation / route+link / reference | **strict** | 不跑 | `dependency` 正查 |
| `grpc-impact` | route+link / reference | 按需 | **跑**（gRPC-server / Dubbo / Job） | `serviceimpact` |
| `facts`（排障） | 全部 BFF 域 | **diagnostic**（失败记诊断、保留其余事实） | **跑**（诊断模式，容错） | 无（只投影事实快照） |

> 要点：gRPC client 依赖发现要 `go list` 拉依赖，成本高，所以只在真正需要时（`--grpc` / `endpoint-assets` / `facts`）才加载；纯 diff 的 `bff-impact` 完全不碰它。`facts` 是唯一同时抽 BFF 域和服务入口事实的命令，因为它是"排障入口"，要能同时照见两类项目。

**一次运行的时序（以 `bff-impact --diff` 为例）：**

```mermaid
sequenceDiagram
    autonumber
    participant CLI as cmd
    participant APP as app（编排）
    participant DIFF as diff
    participant BASE as project+astindex
    participant EX as 领域抽取器
    participant STORE as facts.Store
    participant IMP as impact
    participant OUT as output

    CLI->>APP: bff-impact(project, diff)
    APP->>DIFF: 读取 + 解析 + 校验已应用
    DIFF-->>APP: FileChanges（未应用/过期即报错退出）
    APP->>BASE: 加载源码 + 建符号索引
    BASE-->>STORE: 写入 symbol / module 事实
    APP->>EX: annotation / route+link / reference / im
    EX-->>STORE: 写入各域事实
    APP->>STORE: diff 映射成变更根 + 恢复被删声明 + go.mod 使用点
    APP->>IMP: AnalyzeTrees(store)
    IMP->>STORE: 只读查询（经 graph 视图）
    IMP-->>APP: 传播树
    APP->>OUT: 投影为稳定 JSON
    OUT-->>CLI: stdout
```

> `grpc-impact` 的时序同构，差别只在"领域抽取器"换成服务入口抽取、末端换成 `serviceimpact`。这也再次说明：前两段共用，分叉只在抽取器与传播层。

---

## 3. BFF 分析链路

### 3.1 这条链路回答什么

算出：**受影响的对外 HTTP 接口** + **受影响的出站 IM event**。它接受两种输入，可单独给、也可一起给：

- **一份 BFF 的 diff**：分析这次代码改动影响了哪些接口 / IM event。
- **一个上游 gRPC 接口**（canonical 完整方法名）：反查当前 BFF 里哪些接口用到了它——这是一等输入，专门支持"上游 gRPC 改了，下游 BFF 哪些接口受影响"的场景。

具体参数：

- 输入：`--project`（绝对路径）+ 至少一个 `--diff`（已应用）或 `--grpc`（完整方法名），两者可以一起给。
- 输出：一份 JSON。顶层固定有 `summary`（汇总受影响的接口和 IM event）、`fileSources[]`（每个变更文件的原始 diff + 从它出发的完整传播树）、`grpcSources[]` 与 `endpointSourcesSummary`（用 `--grpc` 反查时承载 gRPC → endpoint 的结果，没用到时为空）；如果改了 go.mod，还有 `moduleSources[]`。

### 3.2 流程：两个传播方向

变更根不是只有一种，"改了什么"决定往哪个方向传播。具体分派不靠"变更类型"这个标签，而是把变更根的目标 ID 依次在各域索引里解析（路由 > group > 中间件 > 注解 > job > 符号 > 文件兜底），命中哪个域就走哪个方向。落下来就是**两个方向**（对应上游 gRPC 反查是第三个入口）：

- **方向 A —— 改了符号**（service / controller / 类型 / 常量 / 变量）：从这个符号出发，沿反向引用图往"**谁引用了我**"的方向走，一路追到注册它的路由，落到接口。
- **方向 B —— 改了路由域**（group 前缀 / 中间件 / 路由本身）：从这个 group / 中间件 / 路由出发，往"**我管辖了哪些 controller**"的方向展开（同 group 下的路由、被这个中间件作用到的路由），对每个受影响 controller 落到接口。

> **中间件本质上就是个普通函数/方法，那怎么确定它是"中间件"、要走方向 B？** 分析器不靠命名去猜。在 route 抽取阶段，`group.Use(Auth)` 这种挂载会被记成一条**中间件绑定**，link 阶段再把 `Auth` 解析成它对应的具体符号。所以判定依据是：**这个符号是不是某条 `.Use()` 绑定的目标**。当改动的符号命中某条中间件绑定时，就触发方向 B 的扩散（扩到该 group 下受它作用的 controller）；同时它也还是个符号，方向 A 的常规引用传播照常进行——两者叠加，不冲突。

```mermaid
flowchart TB
    ROOT{"变更根：改了什么？"}

    subgraph DIRA["方向 A · 改了符号（往引用者走）"]
        direction TB
        SYM["变更的符号<br/>service / 类型 / 常量 …"]
        UP["沿反向引用图找引用者<br/>（谁 call / value / type 引用了它）"]
        CTRL["controller 方法"]
        SYM --> UP --> CTRL
    end

    subgraph DIRB["方向 B · 改了路由域（往被管辖的 controller 走）"]
        direction TB
        GRP["变更的 group 前缀 / 中间件 / 路由"]
        DOWN["展开它管辖的路由<br/>（同 group 下 / 被中间件作用到）"]
        HDL["受影响的各个 controller"]
        GRP --> DOWN --> HDL
    end

    ROOT -->|符号变更| SYM
    ROOT -->|路由/group/中间件变更| GRP

    CTRL --> ROUTE["路由注册点（group 前缀 / wrapper）"]
    HDL --> ROUTE
    ROUTE --> EP["确定 HTTP 接口（见 §3.3）"]

    CTRL -. "命中发送点" .-> SND["发送点 / payload / event 常量"]
    ROOT -.->|IM 相关符号变更| SND
    SND --> EV["确定 IM event（见 §3.4）"]

    EP --> DOC["稳定 JSON"]
    EV --> DOC
```

> IM 传播本质上属于方向 A 的一个分支：payload / event 常量 / 发送控制逻辑是符号，改了之后同样沿反向引用图往上走，只是终点是 IM event 而不是 HTTP 接口（见 §3.4）。

### 3.3 传播的终点（一）：HTTP 接口的路径以谁为准

这是本链路最需要说清楚的地方。一个 controller 身上可能有**两处**路径信息：

1. **controller 注解**：开发者在注释里显式写的，比如
   ```go
   // @Get /admin/api/bff-web/mc/broadcast/record
   func (api *adminBroadcastApi) QueryBroadcastRecord(...) {}
   ```
2. **路由注册**：`g.GET("/record", controller)` 这行，加上它所在 group 的前缀拼出来的路径。

**策略是"注解优先（annotation-first）"，判定顺序如下：**

| 情况                                    | 接口路径以谁为准                                                                                                                        |
| --------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| controller 有注解                          | **以注解的 method + path 为准**（正式结论）。路由解析出的路径作为辅助证据放进同级 `routes[]` 一起输出，但**不覆盖**注解。 |
| controller 没有注解                        | 用路由解析出的 method + path 兜底。                                                                                                     |
| 同一 controller 注册在多个 URL（别名路由） | 见下方"别名"。                                                                                                                          |

**为什么以注解为准？** BFF 的路由前缀可能来自常量、helper 函数参数、wrapper、跨函数传递的 group——纯靠 AST 把这些拼起来，很容易拼出一个"看着精确其实错"的路径。注解是开发者显式声明的，更可信。但注解也可能和实际注册**漂移**（对不上），所以路由解析出的路径也一并放进 `routes[]`，让这种漂移对调用方可见，而不是藏起来。

```json
{
  "method": "POST",
  "path": "/admin/api/bff-web/orders",   // 正式结论，来自注解
  "routes": [{ "method": "POST", "path": "/api/bff-web/orders" }]  // 辅助证据，来自路由
}
```

**别名路由（重点，会输出两条）**：同一个 controller 有时注册在多个不同 URL（新老路径并存），其中只有一个对得上注解。**这时两条路径都会作为独立接口输出**，不会因为注解只写了一个就把另一个吞掉。

判定规则：如果某条路由自己的 method+path 不匹配任何注解，**且**这个 controller 的注解已经被它的其它路由全部认领了，就判定这条是"别名"，用它**自己的 method/path** 单独作为一个接口输出。这样挂在别名路径上的中间件 / group 改动才不会被漏报。判定必须严格要求"注解全被别的路由认领"——否则会误伤"单路由 + 注解漂移"的正常情况（那种情况应保留注解身份）。

举个具体例子。controller `GetCustomer` 注解写的是新路径，同时注册了新旧两条：

```go
// @Get /admin/api/bff-web/mc/customer/:customerId   ← 注解只写了新路径
func (api *customerApi) GetCustomer(...) {}

adminGroup.GET("/admin/api/bff-web/mc/customer/:customerId", ...GetCustomer)  // 路由1：对得上注解
ucGroup.GET("/uc/customers/:customerId",                     ...GetCustomer)  // 路由2：旧别名，对不上注解
```

改动命中 `GetCustomer` 后，输出**两个**受影响接口：

```json
[
  {
    "method": "GET",
    "path": "/admin/api/bff-web/mc/customer/:customerId",   // 路由1 → 注解身份（annotation-first）
    "relation": "annotation_endpoint"
  },
  {
    "method": "GET",
    "path": "/uc/customers/:customerId",                    // 路由2 → 别名，用自己的路径
    "relation": "route_endpoint"
  }
]
```

**两个方向的典型传播路径（对应 §3.2 的方向 A / B）：**

```text
方向 A（改符号）：
  service 方法改了      → 引用它的 controller → 注册它的路由 → 该 controller 的注解 → HTTP 接口
  controller 改了       → 注册它的路由 → 注解 → HTTP 接口

方向 B（改路由域）：
  group 前缀改了        → 这个 group 下管辖的所有 controller → 各自的注解 → 各自的 HTTP 接口
  group.Use(中间件) 改了 → 该 group 后续注册的 controller → 各自的注解
  中间件函数体改了       → 它被挂载的位置 → 受该中间件作用的 controller → 各自的注解
```

### 3.4 传播的终点（二）：IM event 怎么确定

BFF 会主动往前端推 IM 消息，这也是要回归的对象。识别分两步：**先判断"这里确实是一次对外广播发送"，再确定 event 名**。

**第一步：怎么判断是一次广播发送。** 有两种发送写法，对应两种识别方式：

**方式一 · 协议型（业务自己按协议拼 HTTP 发广播）**——识别依据是代码里**同时**出现两个字面量锚点：scheme `broadcast://` 和 endpoint `/broadcast/send`。两个都在才算数（只出现一个不算），这样能避免把普通 HTTP 请求误判成广播。

```go
// 业务自己拼一个广播请求：两个锚点都在 → 判定为一次广播发送
url := "broadcast://im" + "/broadcast/send"
httpClient.Post(url, buildPayload("POST", "LOCK_INVENTORY_UPDATE", body))
```

**方式二 · SDK 型（调用公共 IM SDK）**——识别依据是**精确的 import path + 精确的函数名**：import 必须正好是 `gopkg.inshopline.com/sc1/commons/utils/bus/notify/im`，函数名在内置清单里（`SendIm` / `SendImAsync` / `SendImToUid` / `SendImToUidAsync`）。命中后，按该函数固定的参数布局取值——这几个函数都是 **event 取第 4 个参数、payload 取第 5 个参数**（`call.Args` 的 0-based 下标 3 与 4）。

```go
import im "gopkg.inshopline.com/sc1/commons/utils/bus/notify/im"

// 精确 import + 函数名命中 → event 取下标3、payload 取下标4
im.SendIm(ctx, appId, channel, eventName, payload)
//                             └ event     └ payload
```

> 如果 SDK 函数被调用但实参数量对不上（比如少传了，凑不到下标 4）——不静默放过，而是记一条诊断（code=`im_sdk_argument_mismatch`），说明这次发送没被分析，避免"以为分析到了其实没有"。

**第二步：确定 event 名。** event 名由 channel + event 常量静态拼出来，比如 channel=`POST`、常量=`LOCK_INVENTORY_UPDATE`，拼成 `POST/LOCK_INVENTORY_UPDATE`。当发送分支来自 if/else 等条件时，会沿条件把可能的取值都传播出来，并正确处理分隔符（channel 为空时不加分隔符）。

**传播**：payload 类型 / event 常量 / 发送控制逻辑改了 → 沿本仓调用链往上传播（用不动点迭代，直到不再有新变化）→ 落到具体的 event 字符串。

**拼不出确定字符串的**（动态拼接的 event）→ 标成 `im_event_unresolved` 保留在传播树里，但**不计入** IM 数量统计，避免误报。输出只保留能静态确定的 event 字符串，不含 appId / mode / payload。

### 3.5 附带能力：BFF ↔ gRPC 依赖查询

这是独立于 diff 传播的查询能力。

**关键问题：一个调用 `x.GetOrder(...)` 长得和普通方法调用一模一样，怎么认出它是一次 gRPC 调用？** 靠的不是名字猜测，而是"生成代码建表 + 类型比对"两步：

**第一步 · 建表（扫描 protobuf 生成的 client 代码）**。只认带 `Code generated ... DO NOT EDIT` 标记的生成文件。protobuf 生成的 gRPC client，每个方法体里都有一句规范的传输调用，`ctx` 之后的第一个 string 参数就是完整方法名字符串：

```go
// 依赖里 protobuf 生成的 client（不是业务手写的）
func (c *orderServiceClient) GetOrder(ctx, in, ...) (*Order, error) {
    out := new(Order)
    err := c.cc.Invoke(ctx, "/order.OrderService/GetOrder", in, out, ...)  // ← 规范传输调用，藏着完整方法名
    ...
}
```

分析器扫遍这些生成代码，得到一张**内部映射表**（就是一个 map，只在分析期用、不落盘），key 是"哪个包的哪个 client 类型的哪个方法"，value 是它对应的完整 gRPC 方法名。大致长这样：

```text
key（包 + client 接口类型 + Go 方法）                          →  value（canonical 完整方法名）
────────────────────────────────────────────────────────────    ──────────────────────────────────
(order/pb, OrderServiceClient, GetOrder)                       →  /order.OrderService/GetOrder
(order/pb, OrderServiceClient, ListOrders)                     →  /order.OrderService/ListOrders
(user/pb,  UserServiceClient,  GetUser)                        →  /user.UserService/GetUser
```

> key 里的 client 类型是**导出的接口类型**（如 `OrderServiceClient`），不是未导出的具体实现（`orderServiceClient`）——因为第二步比对的是 BFF 里变量的静态类型，而生成代码里变量通常声明成接口。

补两个实现细节：
- **流式 RPC 一样识别**：除 `Invoke` 外，`NewStream` 形式的流式方法也建表（方法名取对应参数），并从 `ServiceDesc` 的 `Streams` 推导出 streaming 模式。
- **完整方法名可以来自常量**：`Invoke` 的方法名参数既可以是字面量，也可以是引用一个包级 string 常量（新版生成 SDK 常用 `..._FullMethodName` 常量），两种都能解析。

如果某个生成方法用了 `Invoke`/`NewStream` 但没有暴露可解析的规范 protobuf 方法名，就不入表——不满足契约就不认。反过来，如果同一个 key 解析出两个不同的完整方法名（或 streaming 模式冲突），这是**硬错误、直接中断建表**，不静默跳过——避免用一张自相矛盾的表往下算。

**第二步 · 比对（在 BFF 代码里查这张表）**。BFF 里一个调用 `x.GetOrder(...)`，只有当 `x` 的**静态类型**正好是表里那个生成 client 类型、方法名也在表里，才判定为 gRPC 调用，并绑定到对应的 `/order.OrderService/GetOrder`。普通函数调用的 receiver 类型查不到这张表 → 自动被排除。

所以一条 BFF → gRPC 关系必须**同时**满足三点才输出：生成代码证明了这个 operation（第一步入表）、调用的 receiver 类型能静态确定且命中表（第二步）、endpoint 到调用点之间项目内存在可达的调用链。gRPC 接口的身份**只认**完整方法名 `/package.Service/Method`，不靠 Go 方法名、变量名、目录名去猜；不穿透外部 SDK 内部，不跨 BFF 仓聚合。

> **每条 distinct 调用链都单独保留为一条证据。** 当一个 endpoint 经由两条不同路径（比如两个不同 helper）都到达同一个发起 gRPC 的公共函数时，两条路径各自作为一条独立的 call-site chain 输出，而不是只留最先碰到的那条——这是回归排查时定位"从哪条路径调到的"所必需的。

两个方向：

- `bff-impact --grpc /pkg.Svc/Method`：给一个上游 gRPC 接口，反查当前 BFF 里哪些 endpoint 用到了它（可和 `--diff` 合并成一份 JSON）。反查出的 consumer 关系固定标 `may_call`：静态调用链可达，但不承诺每次 HTTP 请求都会真的发这次 RPC。
- `endpoint-assets --endpoint "GET /orders/:id"`：给一个精确 endpoint，查它下游依赖哪些 gRPC 接口。

> **两个方向互为逆运算，满足双向不变量**：在同一份源码快照与同一 build context 下，`endpoint-assets(A)` 的结果里含 gRPC `B`，当且仅当 `bff-impact --grpc B` 的结果里含 endpoint `A`。这个不变量是两条查询共享同一套 `dependency` 图查询的直接结果，也是回归对齐的基线。

### 3.6 配置与输出

- **配置**（可选）：`--impact-config`，不给时会自动读项目里的 `.analyzer/go-impact.config.json`。目前只开放**模块版本变更的过滤**（比如忽略某些 proto 模块的版本号变动），不开放路由/注解/中间件的语法配置。配置字段严格校验，写错或用旧格式直接报错。
  ```json
  { "analyzeModuleChanges": true, "ignoredModuleChanges": ["gopkg.inshopline.com/sc1/app/modules/*/proto"] }
  ```
- **输出**：稳定 JSON，有 schema 约束。**结论里不含诊断信息**（保证接入方拿到的结构稳定），诊断走单独的调试命令看。

### 3.7 这条链路能识别的范围

diff 语义化（函数/方法/变量/常量/类型/struct 字段与 tag 的改动）、这些声明的反向引用传播、controller 注解识别、路由 / group / 中间件 / wrapper 的传播、codegen 模板生成的路由、被删路由的恢复、go.mod 变更到本仓使用点再到接口的传播、出站 IM event 传播、BFF ↔ gRPC 依赖双向查询。

---

## 4. 后端服务分析链路

### 4.1 这条链路回答什么

给一份后端服务的 diff，算出**受影响的对外入站接口**，按 `grpc` / `dubbo` / `http` / `job` 四类分组。

- 输入：`--project`（绝对路径）+ `--diff`（已应用）+ 可选 `--impact-config`。
- 输出：一份 JSON，结构和 BFF **同构**——`summary`（全局去重后的入站契约总览，同样**按 `grpc` / `dubbo` / `http` / `job` 四类分组**）+ `fileSources[]`（每个变更文件的完整传播树）+ `entrySourcesSummary`（受影响入站接口 → 变更来源的反查，也按四类分组）；改了 go.mod 时还会有 `moduleSources[]`（和 BFF 一样，模块变更也会传播到入站契约）。这条链路**只分析当前这一个服务仓**，不查 BFF；跨仓串联是外部编排层的事。

### 4.2 流程

```mermaid
flowchart TB
    CR["变更根<br/>（diff 改动的节点 / go.mod 模块变更使用点）"]
    RG["沿反向引用图往上走"]
    HDL["provider handler / 具体实现"]
    CR --> RG --> HDL

    subgraph IN["落到四类入站接口（识别规则见 §4.3）"]
        direction TB
        G1["gRPC 接口"]
        G2["Dubbo 方法"]
        G3["HTTP 接口"]
        G4["XXL-Job"]
    end

    HDL --> LIVE{"注册点是'活的'吗？<br/>（被引用 / 符合 main·Register*·Initialize* 约定）"}
    LIVE -->|否| DROP["丢弃：孤立注册不计入"]
    LIVE -->|是| G1
    LIVE -->|是| G2
    LIVE -->|是| G3
    LIVE -->|是| G4
    G1 --> OUT["稳定 JSON（四类分组）"]
    G2 --> OUT
    G3 --> OUT
    G4 --> OUT
```

### 4.3 什么算一个"入站接口"——四类注册的识别规则

这是本链路的核心。分析器要能从代码里**认出**每一类注册，才能把改动落到具体接口上。规则如下：

**① gRPC 接口**

名字规则只用来**找到**注册入口，接口身份是**解析描述符**得到的，不是从名字猜的。分两步：

- **靠命名规则找到注册函数**：protobuf 生成的服务端注册函数形如 `RegisterOrderServiceServer`（`Register` 开头、`Server` 结尾、无 receiver）。这一步只是定位，不产生结论。
- **解析 `Xxx_ServiceDesc` 描述符拿真实身份**：找到注册函数后，去读它对应的 `OrderService_ServiceDesc` 变量——里面写着真实的 protobuf service 名和方法列表，据此得到每个方法的完整名 `/order.OrderService/GetOrder`。**身份来自描述符内容，不是把 Go 函数名当身份。**

```go
// 生成代码：描述符里才是真实的 service / method 名
var OrderService_ServiceDesc = grpc.ServiceDesc{
    ServiceName: "order.OrderService",              // ← 真实 service 名
    Methods:     []grpc.MethodDesc{{MethodName: "GetOrder"}, ...},  // ← 真实方法名
}
func RegisterOrderServiceServer(s grpc.ServiceRegistrar, srv OrderServiceServer) { ... }
//                                                        └ 注册时传入的具体实现 = handler
```

- 还要求确定这个 server 接口的具体实现（就是注册时传进去的那个 `srv`），分两种情况：
  - **能证明出多个候选实现**：这是真歧义，**直接报错中断**分析（不猜、不误报）。
  - **一个候选都证明不出来**：记为待确认（一条 warning 诊断），但该注册**仍会作为一个入站契约输出**——用注册函数本身作为绑定点，只是缺具体 handler 符号。这样"这个 gRPC 方法确实注册了"的事实不会因为找不到实现而丢失。
- 接口身份：`/package.Service/Method`（来自描述符）。
- 生成代码从哪读：`Xxx_ServiceDesc` 和 `RegisterXxxServer` **优先从仓内源码**读取（并按最近的 `go.mod` 恢复嵌套 module 的 import path）；仓内没有时，只按需只读加载**实际被 `RegisterXxxServer` 用到的那个依赖包**，不扫描无关依赖图。

**② Dubbo 方法**

- 依据：同一个函数里（不要求导出），**同时**出现 `ServiceConfig` 字面量和 `.SetProviderService(具体实现)` 调用。
- 配对：一个函数里可能有多组，按**源码顺序**把第 i 个 config 和它之后第一个还没被占用的 `SetProviderService` 配起来（这样分组写法 `config;config;call;call` 和交错写法 `config;call;config;call` 都不会配错、不重复）。
- 方法范围：`ServiceConfig` 没写 `Methods` 的（service 级导出）→ 展开该实现类型的**全部公开方法**；写了 `Methods` 的 → 只取列出的方法。
- 接口身份：Dubbo interface + method。

**③ HTTP 接口**

- 依据：服务端自己用路由语法（`g.GET / g.POST ...`）注册的入站接口，路径解析方式和 BFF 的路由一样（本地路径 + group 前缀拼接）。
- 接口身份：拼接后的 method + path。

**④ XXL-Job**

- 依据：在一个"注册函数"里往 map 塞 handler——通过函数的参数/返回值类型能证明这个 map 的 value 是 `jobx` / `xxljob` 包里的 `JobListener` / `TaskFunc` 类型。
- 取值：map 的 key（字符串字面量，或能静态求值的本地/导入包 string 常量）= job 名，value = handler。
- 接口身份：job 名。

**四类共同的前提**：注册点必须是"活的"。判定规则是：注册函数**确实在项目里被接线调用到**（有入向引用）；**或者**它符合启动约定——函数名是 `main`、或以 `Register` / `Initialize` 开头（这类是框架/启动期约定入口，即使静态查不到显式引用也按"活的"算，避免把只由启动框架反射拉起的注册漏掉）。除此之外的孤立注册不计入。

**动态值的表达**：入站契约里如果某段身份是动态拼出来的（比如 HTTP path 用了运行时变量、Dubbo version 来自表达式），不伪造一个运行时值，而是保留原始表达式并标记为 `symbolic`（对应输出里的 `identityResolution` 与 `pathExpression` / `dubboVersionExpression` 字段），让调用方知道这一段是符号级、不是字面确定值。

### 4.4 传播

从变更根沿反向引用图往上走，走到 provider handler / 具体实现，就命中它对应的入站接口。同一个接口被多条路径命中时只出一条去重后的契约。终点只落在有真实注册证据、且注册点判定为"活的"（见 §4.3 共同前提）的入站契约上。

### 4.5 输出

稳定 JSON，有 schema 约束（`schema --type grpc-impact`）。必填三段 `summary` / `fileSources[]` / `entrySourcesSummary`（改了 go.mod 时追加可选的 `moduleSources[]`）；其中 `summary` 和 `entrySourcesSummary` 都固定按 `grpc` / `dubbo` / `http` / `job` 四类分组、组内顺序稳定，方便和回归基线对齐。它和 BFF 输出**形状同构、字段定义各自独立**（两者用不同的 schema，投影逻辑不共用）。

### 4.6 这条链路能识别的范围

diff 语义化 + 反向引用传播（复用底座）、gRPC 服务端接口、Dubbo provider（含源码顺序配对）、XXL-Job、服务端 HTTP 入站接口、go.mod 模块变更到入站契约的传播（同 BFF），四类分组稳定 JSON。（Pulsar / IM 入站暂不覆盖，见 §6。）

---

## 5. 对外命令面（并入 Nexus）

这套能力将作为一项**独立能力并入 Nexus**（面向 coding agent 的 Go 工具链 CLI，`gopkg.inshopline.com/bff/nexus/v2`，`go install ...@next`），对外统一提供。

> **集成方式（明确约束）**：作为**自包含的独立能力**并入，**不复用 Nexus 已有的任何能力**（不共享 `bff` / `doc` 的解析器、模型或数据层）。做法是在 Nexus 下**新增一个 `go-analyzer` 命令族**，把本能力整包引入，命令层只做参数转发。将来若 `ts-analyzer` 也并入，就是平级再加一个 `ts-analyzer` 族，两者互不影响。

命令面：

```text
# BFF
nexus go-analyzer bff-impact       --project <绝对路径> [--diff <绝对路径>] [--grpc </pkg.Svc/Method>]... [--impact-config <绝对路径>]
nexus go-analyzer endpoint-assets  --project <绝对路径>  --endpoint "METHOD /path"...
# 后端服务
nexus go-analyzer grpc-impact      --project <绝对路径>  --diff <绝对路径>  [--impact-config <绝对路径>]
# 调试
nexus go-analyzer facts            --project <绝对路径>
nexus go-analyzer schema           --type facts|impact|grpc-impact   # impact 对应 bff-impact 命令的输出契约

# 将来的 ts-analyzer（示意，不在本方案范围）
# nexus ts-analyzer <...>
```

约定：

- **命令名说明**：本文档统一用并入 Nexus 后的对外命名 `bff-impact`。当前独立二进制里这个命令实际叫 `impact`（`schema --type` 也用 `impact` 指代它的输出契约）；并入 Nexus 时由命令层做转发/改名为 `bff-impact`，行为与契约不变。
- 路径参数都要求**绝对路径**；`bff-impact` 至少给一个 `--diff` 或 `--grpc`；`--diff` 必须已应用到源码。
- 结论 JSON 走 stdout（agent 可直接 pipe），可选 `--out-dir` 按 `~/.local/nexus-ai/go-analyzer/<子命令>/<id>/` 落盘（结论和诊断分开放），对齐 Nexus 的产物布局。
- 复用 Nexus 的外壳约定（`-h` 探索、版本对齐脚本、issue 上报）——这些是 CLI 通用外壳，不算"复用已有能力"。
- 只依赖 Go 标准库，Go 1.24+。

**与 Nexus 的接触面只有两处**：命令注册点、CLI 外壳约定。除此之外和 `bff` / `doc` 零耦合，各自独立演进。

---

## 6. 后续可以继续支持的方向

以下能力现在不在覆盖范围内，属于代码里静态证明不了、或需要跨仓/额外输入的场景。等有需要时再按同一套事实模型往上加，不需要推翻现有结构：

- **前端页面影响**：把本工具输出的 HTTP 接口喂给 `ts-analyzer`，串起"后端改动 → 前端页面"。
- **跨仓传播**：上游服务 → BFF → 前端的自动串联（现在每个仓单独跑，跨仓交给外部编排）。
- **后端服务的 Pulsar / IM 入站**。
- **多实现接口分发、复杂反射 / DI 的精确分析**。
- **面向 QA 的自然语言回归报告**。

---

## 7. 风险与权衡

| 风险                            | 说明                                | 怎么应对                                                         |
| ------------------------------- | ----------------------------------- | ---------------------------------------------------------------- |
| 静态分析看不透动态路由 / 反射   | Go 服务有 wrapper / DI / 反射       | 看不透的降级成诊断或"未解析"，不进结论                           |
| 注解和实际路由对不上（BFF）     | 注解漂移                            | 注解优先出结论，同时把路由路径放进`routes[]`，漂移对调用方可见 |
| 别名路由漏报中间件影响（BFF）   | 同 controller 多 URL，只有一个匹配注解 | 严格判定别名后单独输出，不漏挂在别名上的中间件改动               |
| Dubbo provider 配错（后端服务） | 一个函数里多组 config / 实现        | 按源码顺序配对、标记已占用，防止抢占和重复                       |
| gRPC server 多实现歧义（后端服务） | 一个 server 接口能证明出多个实现     | 直接报错中断，不猜一个；零实现则记待确认但仍保留注册契约         |
| 跨仓需求                        | 上游 → BFF → 前端                 | 单仓分析 + 外部编排，预留 HTTP / gRPC 桥接输出                   |

---

## 8. 评审重点

1. **架构分叉**（共用公共底座，只在领域抽取器 + 传播/投影层分开）是否清晰、复用边界是否合理。
2. **BFF 的接口路径策略**（注解优先 + 路由作辅助证据 + 别名判定）是否覆盖真实注册写法。
3. **后端服务四类注册的识别规则**（§4.3）是否准确、有没有漏掉的注册形态。
4. **IM 识别锚点**（协议型双锚点 / SDK 型签名）是否够用。
5. **"只报能证明的关系"原则**（证明不了就降级为诊断/未解析，不用置信度打折）是否满足"结论百分百可信"的诉求。
6. **并入 Nexus 的形态**（独立 `go-analyzer` 族、不复用已有能力、为 `ts-analyzer` 预留平级空间）是否符合平台方向。
