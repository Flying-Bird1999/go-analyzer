# go-analyzer 架构与代码审核（2026-07-07）

> 本文件是一次架构师级深度审核的产出，作为**当前优化 backlog**。
> 审核范围：`internal/` 与 `cmd/go-analyzer`。状态：**源码无阻塞性 bug，以下均为优化项。2026-07-08 已修复一批（见 §0.1），其余按需 triage。**
> 已解决的条目已在条目前标注 `✅` 并保留，便于追溯。

## 0. 产出方式与可信度

- 方法：多 agent workflow（10 个 finder 并行）—— 4 个模块 holistic 深读（`extract/im`、`extract/route`、`extract/reference`、`impact`）+ 6 个跨模块维度（boundary / coupling / duplication / error-handling / concurrency / complexity）。
- 验证：每条发现由独立的对抗式 skeptic 默认「反驳」，**必须读真实代码取证**才能放行；refuted 的不进入本清单。
- 规模：37 条原始发现 → **33 条通过核实** → 去重后 33 条（high 4 / medium 14 / low 15）。48 agent，~158 万 token。
- 局限：性能类条目基于代码结构推断，**未经 profiling**；动手前建议先复现核实（尤其标注「可能真实 bug」的项）。

## 0.1 修复进展（2026-07-08）

本轮按 TDD（bug 先写失败测试坐实）+ 行为保持（重构靠现有 8.5k 行测试 + 新增 facts 契约护栏兜底）推进，全程 `go build/vet/gofmt/test ./...` 全绿。

**✅ 已完成（8 项）：**

- ✅ T1#1 **path 拼接漂移 bug**（真实 bug）：`impact/deleted_route.go` 的 `joinDeletedRoutePath` 补最终 `TrimRight`，与 `route.joinPath` 对齐；新增 `internal/impact/join_test.go` 坐实 root/trailing-slash 漂移。
- ✅ T1#2 **Event/Body 匹配过松 bug**（真实 bug）：`im/summary.go` `directProtocolSummary` 改为结构化绑定（Event 接收者必须是持有 Body 字段的同一变量）；新增 `TestExtractDirectProtocolIgnoresUnrelatedEventAndBody` 坐实干扰项错配，并补 `TestExtractDirectProtocolResolvesSplitBodyAssignment` 支持 `data.Body = msg` 分步赋值。
- ✅ T1#3 **facts 契约对齐护栏**：新增 `internal/output/contract_alignment_test.go`，反射断言 Document↔schema required↔schema properties↔RenderJSON 四处同步 + 17 个 fact 结构体↔schema $def 对齐 + required/omitempty 语义对齐 + transient 不泄漏。
- ✅ T2/B2 **astindex 原语去重（ARCH 18）**：导出 `astindex.SelectorParts`（删 route/reference/link 3 份副本）、`astindex.ValueTypeFromTypeExpr`（reference/im 改委托，消 3 份平行 type-expr walker）。
- ✅ `expandSymbol` O(L²)→就地回溯（`impact/tree_builder.go`），删 `copySymbolPath`。
- ✅ `package_load_failed` 诊断码进 `diagnostics/codes.go`（`CodePackageLoadFailed`），`project/loader.go` 接入。
- ✅ Store 瞬态事实 `Changes/ModuleChanges/ModuleUsages` 改 `json:"-"`（与 `RouteGroupFlows` 对齐，从结构上排除泄漏）。
- ✅ `im/summary.go` `addSummary` 删冗余逐次插入排序（最终顺序由 extract 第三步按 IMEventFact ID 排序保证）。

**⏸️ 暂缓（4 项，附理由）：**

- ⏸️ B2 RenderExpr 三变体统一：route/im 用 `token.NewFileSet()`、reference 用 `file.FileSet`，统一会改 raw text 输出、动 golden，属有输出漂移风险的中低价值项，留待定向评估。
- ⏸️ T2 拆 `im/summary.go` 模板子系统为 `template.go`（high×M）：模板代码与 `summaryEngine` 深度耦合（依赖 `symbolDependencies`/`fieldTypeIDs`/`resolveLocalCall`/`eval.eventValue`），完整接口抽取是 ~180 行精密手术，对 IM 传播最复杂路径有引入隐蔽回归风险，建议作为独立专注 session 做。
- ⏸️ T2 `reference/extractor.go` 5→2 遍历融合（medium×M）：reference 为第二复杂模块，融合需精细保证节点分类不变，建议独立 session。
- ⏸️ B5 `project/loader.go` 并行化：**先 profile** 证明 `project_load` 占大头再动，否则过早优化（全仓刻意保持无并发原语）。

**未列入本轮的 Low 项**（`middlewareBindingsForSymbol` 索引、`isProjectPackage` 下沉、`diagnostics.AddFact` 增量、output `sortByID` 泛型等）为低价值微优化或边际收益，可机会性批量清理。


## 1. 架构师优先级分层（执行建议，非照单全收）

### T1 — 先做（低风险、ROI 最高，含 2 个可能的真实 bug）

| # | 条目 | 锚点 | 评级 | 备注 |
|---|---|---|---|---|
| ✅1 | **path 拼接漂移**：deleted route 与 live route 端点串可能不一致 | `internal/extract/route/astutil.go:63-85` + `internal/impact/deleted_route.go:493-524` | 可能真实 bug→已修 | `joinDeletedRoutePath` 补最终 TrimRight 对齐 `joinPath`；新增 `join_test.go` 坐实 |
| ✅2 | **directProtocolSummary Event/Body 匹配过松** | `internal/extract/im/summary.go:448-483` | 可能真实 bug→已修 | 改结构化绑定（Event 接收者须为持有 Body 的同一变量）；新增干扰项测试坐实 |
| ✅3 | facts 公开契约对齐测试（纯加测试） | `internal/facts/store.go:53-121`、`internal/output/json.go`、`contract_test.go` | high×M | 新增 `contract_alignment_test.go`：Document↔schema↔RenderJSON + 17 fact↔$def 反射断言 + transient 不泄漏 |
| ✅4 | 收敛 astindex 共享原语（机械去重） | `internal/astindex/index.go` | high×S / medium×S | 导出 `ValueTypeFromTypeExpr`、`SelectorParts`，删 im/reference/route/link 平行副本（ARCH 18） |
| ✅5 | expandSymbol O(L²)→O(1) 就地回溯 | `internal/impact/tree_builder.go:217-225` | medium×S | 就地回溯 + 删 `copySymbolPath`，零行为变化 |

### T2 — 值得做（有测试网兜底，工作量更大）

- 拆 `internal/extract/im/summary.go`（1119 行 god file）的值模板子系统为独立 `template.go`（high×M）。
- `internal/extract/reference/extractor.go:41-92` 函数体被遍历 5 次 → 融合为 1-2 次（medium×M）。

### T3 — 暂缓 / 需证据

- ⚠️ **并行化 `project/loader.go:152-189` 的 loadFile**：报告评 high，但「CPU 热点」**无 profiling 证据**，且会破坏全仓「无并发原语」的简洁性。**先 profile 证明 `project_load` 占大头再动，否则过早优化。**
- `internal/impact/deleted_route.go` 在 impact 包执行 extractor 职责（boundary，medium×M）：ARCH 5.14 有意把 RecoverDeletedRoutes 列为 impact 入口，属文档内部矛盾。
- `internal/astindex/index.go:32-36` Symbols 是导出可变 map，建议非导出 + 只读访问器 + `AddSymbol`（coupling，medium×M）。
- 15 条 Low：机会性批量清理。

---

## 2. 完整发现清单（workflow 产出，每条含证据 + 建议）

### High（4）

**`[complexity]` summary.go 是 1119 行 god file，值模板子系统应拆出独立文件** — `internal/extract/im/summary.go:25-53,516-749,957-1030` — high×M
6 类职责（引擎编排/函数索引/可达性/直接摘要/控制依赖/事实投影）混于一文件；值模板子系统（`templateFromExpr`/`substitute` ~340 行）内聚度高、仅依赖 `eval/index/resolveLocalCall/symbolDependencies/fieldTypeIDs` 几个窄能力，却作为 `*summaryEngine` 方法无法脱离引擎单测。建议抽出 `template.go`，导出为接受窄接口的独立类型；engine.go 只留编排/索引/可达性/投影。

**`[coupling]` 公开事实面在 Store/Document/schema/RenderJSON 四处手工同步，缺对齐测试** — `internal/facts/store.go:53-121` — high×M
新增一个公开 fact 需同步 6+ 处（Store 字段、NewStore、Document 字段、schema required+properties、RenderJSON 拷贝+sort、ensureNonNilSlices），而 `contract_test.go` 只有“某属性存在/退役定义已清理”的点状断言，无集合相等断言、无 schema 校验器；漏改 Document 时 golden 仍逐字节相等、测试全绿。建议：(1) 对 RenderJSON 产物用 `schemas/facts` 做 JSON-Schema 校验（性价比最高）；(2) 补“Document 数组字段 == schema required == schema properties 顶层键 == RenderJSON 拷贝目标”的反射断言（注意 Store 上 Changes/ModuleChanges/ModuleUsages 是按设计不进 facts JSON 的 transient，需白名单）。

**`[duplication]` 类型表达式→ValueType 解析存在三份平行实现，违反 ARCH 18** — `internal/extract/im/expr.go:488-517` — high×S
`im/expr.go:488 typeExprValueType`、`reference/scoped_types.go:168 scopedTypesFromTypeExpr`、`astindex/index.go:506 valueTypeFromTypeExpr`（未导出）三份处理同一组 Ident/Selector/Star/Paren/Index/IndexList 分支、同样 `file.Imports[pkg.Name]` 解析，唯一差别是单值 vs 切片。而 ARCHITECTURE 18 点名禁止的正是 value type resolver 平行实现。修复极廉：导出 `astindex.TypeExprValueType`，reference 包一层返回 `[]ValueType`，im 直接替换。单点扩展类型形态。

**`[concurrency]` loadFile 解析是 CPU 热点却串行，且共享 map/slice 写无同步** — `internal/project/loader.go:152-189` — high×M *(架构师注：先 profile，见 T3)*
每文件独立 `token.NewFileSet()`+`parser.ParseFile`（天然可并行），却紧接着直接写 `p.Packages[pkgPath]`（map）与 `pkg.Files = append`（slice）；全仓 internal 无 sync/goroutine/errgroup。astindex 因跨趟共享写必须串行，故 project_load 是唯一现实并行化收益点。建议 worker 池并发解析 + 单 goroutine 按序 reduce（或 mutex 保护两处写），保留确定性输出与 extractor 串行假设。

### Medium（14，已合并明显重叠项）

**`[correctness]` directProtocolSummary 的 Event/Body 匹配过松，可能错配** — `internal/extract/im/summary.go:448-483` — medium×S *(架构师注：上调为优先核实)*
eventExpr 取任意 `*.Event(...)`、payloadExpr 取任意 `Body` 字段，两者无结构绑定；Body 是 Go 极常见字段名，可达 `/broadcast/send` 的函数同时含无关 `Event()` 与无关 `Body` 时会产生虚假 IMEventFact 并沿链上传播。建议像 `broadcastParamsCall` 那样要求 Event 接收者与含 Body 的字面量同变量/同类型。

**`[duplication]` 路径拼接三套实现，deleted route 与 live route 端点串可能不一致** — `internal/extract/route/astutil.go:63-85` + `internal/impact/deleted_route.go:493-524` — medium×S *(架构师注：上调为优先核实)*
`joinPath`/`joinContextPrefix`（=joinPath 委托）有末尾 TrimRight，`joinDeletedRoutePath` 缺失；`prefix="/api"+path="/"` 时前者得 `/api`、后者得 `/api/`。deleted route 不再经 `applyRouteCallPrefixes` 二次归一，漂移被保留，同一逻辑端点在删除前后串不同。建议导出 `joinPath` 让两边委托，补一条 `LocalPath="/"` 回归用例。

**`[performance]` expandSymbol 每条反向引用边复制整张 path map，链深 L 即 O(L²) 拷贝** — `internal/impact/tree_builder.go:217-225,597-604` — medium×S
DFS 顺序执行，复制纯防御性冗余。建议就地回溯：进入 child 前 `path[ref.FromSymbol]=true`，返回后 `delete`。每条边零分配，环路判定与 EventsForPath 行为不变。

**`[duplication]` selectorParts 在 4 个包逐字节相同（astindex 已有未导出副本）** — `internal/extract/route/astutil.go:38-47` — medium×S
route/reference/link/astindex 四份完全相同，三调用方都已 import astindex。导出 `astindex.SelectorParts` 并删三份副本即可，纯机械替换，消除三个漂移源。

**`[duplication]` AST 表达式→源码文本渲染有 3 个变体，route/im 两份逐字节相同** — `internal/extract/route/astutil.go:15-22` — medium×S
route 的 `exprString` 与 im 的 `renderExpr` 用 `token.NewFileSet()`（丢位置信息），reference 的 `typeExprString` 用 `file.FileSet`（更正确）。建议在共享包提供 `RenderExpr(fset, expr)`，统一传 `file.FileSet`。

**`[concurrency]` 稳定 ID 内嵌 append 位置计数器（len(store.*)），是确定性/并行化地雷** — `internal/extract/route/extractor.go:349` — medium×S
`middlewareID(...)+":"+strconv.Itoa(len(store.Middleware))` 把切片长度写进对外稳定 ID；deleted_route 与 pipeline moduleUsage 也有同型。另一中间件路径 `middlewareCall` 仅用 `routeFunc:groupVar:statementIndex` 已证唯一，故 len() 后缀冗余。建议改 file:line:symbol 或稳定 hash 派生，为后续按包并行扫清隐性顺序依赖。

**`[performance]` controlExpressions 对同一调用点反复全量遍历函数体** — `internal/extract/im/summary.go:146-172,842-891` — medium×M
不动点最内层每个 calleeSummary 对同一 (info,call) 全量 `ast.Inspect` 两遍，而控制上下文是静态的。建议在 `indexBody` 一次性预算 `call.Pos()→[]ast.Expr` 缓存入 functionInfo，两处改查表。

**`[performance]` 每个函数体被重复遍历 5 次，可融合为 1-2 次** — `internal/extract/reference/extractor.go:41-92` — medium×M（校正自 high）
collectScopedValueTypes/ignoredValuePositions/callFunPositions/extractValueReferences/extractFuncReferences 自身五次 `ast.Inspect(fn.Body)`。三次收集遍历无依赖可合并、两次解析遍历处理不相交节点类型，可干净 5→2。1127 行 extractor_test 提供安全网。

**`[duplication]` 表达式分发逻辑三处重复，分类规则需同步改三遍** — `internal/extract/reference/extractor.go:71-162` — medium×M
CallExpr/CompositeLit 分支在函数体与初始化式逐字重复，SelectorExpr/Ident 抽取在 values.go 与 extractor.go 重复。建议抽两个聚焦小 helper（`handleCallExpr`/`handleValueSelector`）而非 boolean 参数化（后者会与 skipLocals 语义冲突）。

**`[boundary]` deleted_route.go 放在 impact 包却执行 extractor 职责，反向依赖 extract/link/astindex** — `internal/impact/deleted_route.go:32-258` — medium×M（校正自 high）
用 go/parser 解析删除块、产出 Symbol/Annotation/RouteRegistration/Change fact，且 line 223 是 astindex.Build 之外唯一处 `idx.Symbols[id]=` 写——绕过 facts.Store 改私有索引。注：ARCHITECTURE 5.14 有意把 RecoverDeletedRoutes 列为 impact 入口，故属“文档内部矛盾”而非意外泄漏。建议迁到独立 recover 层，impact 退回纯传播；同时配合下条给 astindex 加访问器收口 post-Build 写。

**`[coupling]` astindex.Index.Symbols 是导出可变 map，缺访问器封装** — `internal/astindex/index.go:32-36` — medium×M
15+ 处直接 `idx.Symbols[id]` 读写，无 Has/Symbol 访问器；deleted_route 的越界写正是结构性根因。建议 Symbols 改非导出 + 只读访问器 + 显式 `AddSymbol` 收口 deleted route 恢复路径。

**`[performance]` 不动点传播每轮对所有 callee 摘要重跑 substitute（含 go/printer）** — `internal/extract/im/summary.go:137-173` — medium×M（校正自 high）
全函数×全调用×callee 全部 summaries 无差别 substitute，去重发生在事后 addSummary。建议 dirty 集合：每轮只处理“上一轮新增摘要的 callee”的 caller。健康仓改善为常数因子（传播深度 D≈2-5），不是量级收益，但仍消除 renderExpr 的 FileSet 重复分配。

**`[coupling]` route 常量路径解析是 im evaluator 的弱化子集，无法解析跨包 const 路径** — `internal/extract/route/extractor.go:50-102` — medium×L
route 只收本包常量、递归无 SelectorExpr 分支；`g.GET(Const+"/x",h)`（含本包 const）也会被判动态、发 CodeRouteDynamicPath 告警。缺口实际在两处：`routeStringArg`（组前缀，缺 SelectorExpr）与 `call.go:37 stringLiteral`（路由路径，需 const 求值而非仅字面量）。建议把静态字符串求值下沉到 astindex，两个调用点都接入。

**`[correctness]` ParseRouteCall 接收者解析缺 returnsGroup 守卫，deleted 比 live 更宽松** — `internal/extract/route/call.go:57-77` — low×M（核实存疑，已降级）
deleted 路径只用 `parseRouteGroupExpr`（仅查 isRouteGroupWrapper），live 路径还过 `groupForExpr` 的 returnsGroup 校验。但 live 路径不消费 `parsed.GroupRaw`，故非“两路径结论分歧”，而是 deleted best-effort 恢复上的精度不对称；建议的等价校验需跨层管道改造，性价比低。短期至少补注释固化边界。

### Low（15，合并简述，可批量）

均为真实但影响有限的清理项：

- **im/summary.go `addSummary` 每次插入重排** (`:488-509`) — 去重走 summaryKeys、最终顺序由 extract 第三步保证，内层 sort 对正确性零贡献，删 502-507 即可，O(K²logK)→O(KlogK)。
- **reference go/printer 对同一表达式重复调用** (`types.go:119-152` / `values.go:166-190`) — `raw` 提到循环/分支前，零语义变化；但 targets 长度通常 1，属常量倍率微优化。
- **scoped types walker 与 astindex 重复** (`reference/scoped_types.go:167-202`) — 与 high 条 #3 同源，导出 `astindex.ValueTypeFromTypeExpr` 一并解决。
- **middlewareBindingsForSymbol 直接扫 store.Middleware** (`tree_builder.go:279-299`) — graph 已为 ref/route/dep 建索引独缺此；NewRouteGraph 第 109 行已在遍历该切片，加一张 `BindingsByMiddlewareSymbol` map 几乎零成本。注：impact 读 store 在架构上合规，属内部自洽性而非跨模块耦合。
- **瞬态事实 Changes/ModuleChanges/ModuleUsages 误带真实 json tag** (`facts/store.go:63-77`) — 注释写“不输出”却带 tag，仅靠 Document 过滤；改 `json:"-"` 与 RouteGroupFlows 对齐。当前无实际泄漏。
- **isProjectPackage 既抽函数又内联** (`reference/resolver.go:115-117` / `types.go:173`) — 同包内重复 + 全仓 4 处行内；下沉 `idx.IsProjectPackage(pkgPath)`。
- **package_load_failed 绕过 codes.go 注册表** (`project/loader.go:152-189`) — 唯一裸字符串诊断码、severity 硬编码；补 `CodePackageLoadFailed` 常量（project import diagnostics 无循环）。
- **matchesBuildContext 静默吞 MatchFile 错误** (`project/loader.go:137-143`) — build-constraint 降级与其他降级不一致，畸形 tag 时文件被保守保留可能让 route/IM 静默泄漏；补 `build_context_match_failed` info 诊断。
- **im indexBody AssignStmt/DeclStmt 多名字下标逻辑重复** (`summary.go:245-275`) — 抽本地闭包 `recordAssignment`，约 5 行重复（非声称的 12 行）。
- **im ident/selector→ValueSymbolID 解析 4 处重复** (`summary.go:329-356,929-953` + `expr.go`) — 抽 `resolveSelectorImport` 小 helper；但 eventValueSeen/enumConst 要 constDecl+递归，不契合统一 API，仅 2 站点干净适用。
- **im.resolveLocalCall vs reference.ResolveCall 两套实现** (`summary.go:899-925`) — im 仅 Ident/pkg.Func，不覆盖 var 函数值/方法分发；边界已在 899-901 注释为有意，复用需移植整套 scopedTypes（非“轻量”），短期补测试固化边界。
- **output RenderJSON 11 路（实为 10 路）拷贝/排序/归一样板** (`output/json.go:17-103`) — 用泛型 `sortByID[T]` 收敛排序段（零类型安全损失），拷贝/归一两段因绑定具名字段不宜一刀切；漂移后果已被 golden 兜底。
- **diagnostics.AddFact 每次重建 Collector，O(M²)** (`diagnostics/facts.go:25-46`) — Store 持长生命周期 Collector，AddFact 改 O(1) 增量，排序推迟到收尾（RenderJSON 本就重排 Diagnostics，每次排序冗余）。M 由问题构造数封顶（通常几十），非关键路径。
- **selectorParts route vs reference 重复** — 已并入 medium 的 4 份合并项。
- **joinDeletedRoutePath vs joinPath** — 已并入 medium 的路径拼接合并项。

---

## 3. 复现 / 再审核说明

若需要刷新本 backlog（例如大重构后），可用相同的 multi-agent workflow 重新跑一遍：10 finder（4 模块 holistic + 6 维度）→ 每发现对抗式 verify → 去重排序 → synthesis。脚本与本次 run 存于会话 workflow 目录；也可参考 `ARCHITECTURE.md` §16（当前能力边界）与 §17（扩展原则）作为审核基准。

本文件**不作为当前实现状态真值**——实现状态以 `ARCHITECTURE.md` 为准；本文件是面向未来的优化 backlog 与审核记录。
