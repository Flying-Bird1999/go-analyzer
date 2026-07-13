# BFF gRPC Dependency Assets Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为单个 Go BFF 项目提供精确的 `endpoint-assets` 与 `grpc-consumers` 双向查询，且只输出可由 generated gRPC client、静态 receiver 类型和项目内可执行调用链共同证明的正式关系。

**Architecture:** 先从当前 module 的只读依赖图构建 generated gRPC catalog，再复用统一的 AST receiver 类型解析器提取项目内 `GrpcCallFact`。查询层基于仅包含 `call` reference 的可执行图做确定性 BFS，并将 route/annotation handler 作为 endpoint 边界；`facts` 以 diagnostic 模式提取，两个新命令以 strict 模式执行，`impact` 完全关闭该能力。

**Tech Stack:** Go 1.24、标准库 `go/ast` / `go/parser` / `go/token` / `os/exec`、现有 `project` / `astindex` / `facts` / `graph` / `output` pipeline、JSON golden tests、真实 BFF smoke tests

---

## 1. 实施约束

- 设计真值：`docs/design/bff-grpc-dependency-assets.md`。
- 每次命令只分析一个 `--project`；不得引入多仓聚合或项目注册表。
- gRPC 主键只能是 `/<protobuf-package>.<service>/<rpc-method>`，不能由 Go method 名反推。
- endpoint 输入只能是 `METHOD /exact/template/path`，method 大写归一化，path 精确匹配。
- 只接受 generated transport、client interface/constructor、receiver 静态类型三类证据同时成立的调用。
- 禁止根据变量名、getter 名、field 名、目录名或 selector method 名猜测。
- 禁止递归进入外部 SDK 寻找其隐藏的 gRPC 调用。
- BFF 业务源码直接调用 `Invoke` / `NewStream` 不进入第一版结果。
- 查询输出 all-or-nothing；任何严格分析错误都不得在 stdout 留下部分 JSON。
- 新命令不增加 `schema --type`；但现有 `facts` schema 必须同步新增公开 facts。
- 每个任务完成后运行指定测试并提交；不得把多个失败点堆到最后一起修复。

## 2. 文件职责映射

### 新增文件

- `internal/project/dependencies.go`：只读执行 `go list -deps -json ./...`，解析当前 build context 选中的依赖包。
- `internal/project/dependencies_test.go`：覆盖 readonly/vendor/local replace/workspace/GOFLAGS/文件零修改。
- `internal/astindex/scoped_values.go`：共享的函数作用域 value type 收集与 receiver 表达式解析。
- `internal/astindex/scoped_values_test.go`：覆盖参数、field、局部变量、constructor/getter、遮蔽和多候选。
- `internal/facts/grpc.go`：`GrpcOperationFact`、`GrpcClientBinding`、`GrpcCallFact`、streaming mode。
- `internal/extract/grpc/catalog.go`：generated source catalog 构建、binding 冲突检测。
- `internal/extract/grpc/catalog_test.go`：现代/旧版 unary、四种 streaming、decoy、冲突。
- `internal/extract/grpc/extractor.go`：项目源码 gRPC selector call 抽取。
- `internal/extract/grpc/extractor_test.go`：真实 BFF receiver 范式与严格拒绝场景。
- `internal/graph/call.go`：只含 `ReferenceKindCall` 与 gRPC terminal 的可执行正反向图。
- `internal/graph/call_test.go`：过滤 type/value edge、排序、环与多 call-site。
- `internal/dependency/query.go`：endpoint/grpc 输入解析、双向 BFS、聚合与不变量。
- `internal/dependency/query_test.go`：正反向查询、最短链、批量失败、双向一致性。
- `internal/output/dependency.go`：两个命令共享的 endpoint、handler、client、chain contract。
- `internal/output/endpoint_assets.go`：正向 document 构建与稳定 JSON 渲染。
- `internal/output/grpc_consumers.go`：反向 document 构建与稳定 JSON 渲染。
- `internal/output/dependency_test.go`：contract 字段、排序、路径清理与 golden。
- `internal/app/errors.go`：稳定 typed error code 及 CLI 可识别的错误接口。
- `internal/app/dependency.go`：`RunEndpointAssets*`、`RunGrpcConsumers*` 严格 pipeline。
- `internal/app/dependency_test.go`：mode、错误传播、stdout 前完整校验。
- `testdata/fixtures/grpc-proto/go.mod`：独立 generated proto fixture module。
- `testdata/fixtures/grpc-proto/order/order_grpc.pb.go`：现代 unary/streaming generated fixture。
- `testdata/fixtures/grpc-proto/legacy/legacy.pb.go`：旧版 literal `Invoke` fixture。
- `testdata/fixtures/grpc-dependencies/go.mod`：通过相对 `replace` 引用 proto fixture 的 BFF module。
- `testdata/fixtures/grpc-dependencies/controller/order.go`：annotation handler 与直接调用。
- `testdata/fixtures/grpc-dependencies/service/order.go`：多层 service forwarding。
- `testdata/fixtures/grpc-dependencies/remote/order.go`：wrapper、package var、struct/local/getter 调用。
- `testdata/fixtures/grpc-dependencies/router/router.go`：route 到 handler link。
- `testdata/golden/grpc-dependencies.facts.json`：新增 gRPC facts 的稳定输出。
- `testdata/golden/endpoint-assets.json`：正向查询稳定输出。
- `testdata/golden/grpc-consumers.json`：反向查询稳定输出。

### 修改文件

- `internal/extract/reference/scoped_types.go`：删除私有类型推断实现，改为调用共享 `astindex.FunctionScope`。
- `internal/extract/reference/extractor.go`：使用共享 scope API，保持现有 reference 行为。
- `internal/facts/store.go`：加入非 nil 的 `GrpcOperations`、`GrpcCalls`。
- `internal/diagnostics/codes.go`：加入 gRPC diagnostic mode 使用的稳定诊断码。
- `internal/output/schema.go`：现有 facts `Document` 增加 gRPC facts。
- `internal/output/json.go`：复制、排序并输出 gRPC facts。
- `internal/output/contract.go`：只扩展现有 facts schema，不增加新 schema 类型。
- `internal/output/contract_alignment_test.go`：把 gRPC facts 纳入顶层和字段对齐护栏。
- `internal/output/golden_test.go`：增加三个新 golden case，更新 mini BFF 空数组。
- `internal/app/options.go`：增加两个查询 options 和内部 `grpcMode`。
- `internal/app/pipeline.go`：按 `off|diagnostic|strict` 控制 dependency/catalog/call extraction。
- `cmd/go-analyzer/main.go`：注册两个子命令、重复 flag、help、稳定错误渲染。
- `cmd/go-analyzer/main_test.go`：参数、help、错误前缀、stdout/stderr 与 timings 隔离。
- `README.md`：补充两个正式命令和精度边界。
- `ARCHITECTURE.md`：补充 catalog、gRPC facts、可执行图与 pipeline mode。
- `docs/contracts/output-contract.md`：记录两个新 JSON contract 和 facts 扩展。
- `docs/validation/real-project-validation.md`：记录三个真实 BFF 的固定验收关系。
- `scripts/smoke-real-projects.sh`：验证 operation/call/relation 数量和双向固定链。
- `testdata/baselines/real-project-facts.json`：增加 gRPC facts/relation baseline 字段。

## 3. 任务清单

### Task 1: 定义 gRPC 原子事实与 facts 输出契约

**Files:**
- Create: `internal/facts/grpc.go`
- Modify: `internal/facts/store.go:51-121`
- Modify: `internal/output/schema.go:7-32`
- Modify: `internal/output/json.go:17-102`
- Modify: `internal/output/contract.go:10-34` 及 `factsDefinitions`
- Modify: `internal/output/contract_alignment_test.go:17-159`
- Modify: `internal/output/golden_test.go:24-72`
- Modify: `testdata/golden/mini-bff.facts.json`

- [ ] **Step 1: 先写 Store 与 contract 对齐失败测试**

在 `internal/output/contract_alignment_test.go` 的公开顶层键中加入：

```go
"grpc_operations", "grpc_calls",
```

并在 fact/schema 对齐表中加入 `GrpcOperationFact`、`GrpcClientBinding`、`GrpcCallFact`。新增 Store 测试断言零值 Store 序列化为 `[]`，不能是 `null`。

- [ ] **Step 2: 运行测试确认按预期失败**

Run: `go test ./internal/facts ./internal/output -run 'TestFacts|TestRenderJSON|TestMiniBFFGolden'`

Expected: FAIL，提示缺少 gRPC fact 类型或 facts 顶层字段不一致。

- [ ] **Step 3: 实现最小 fact 类型**

`internal/facts/grpc.go` 至少定义：

```go
type GrpcStreamingMode string

const (
    GrpcStreamingUnary         GrpcStreamingMode = "unary"
    GrpcStreamingClient        GrpcStreamingMode = "client_streaming"
    GrpcStreamingServer        GrpcStreamingMode = "server_streaming"
    GrpcStreamingBidirectional GrpcStreamingMode = "bidirectional_streaming"
)

type GrpcClientBinding struct {
    GoPackage string `json:"go_package"`
    ClientType string `json:"client_type"`
    GoMethod string `json:"go_method"`
}

type GrpcOperationFact struct {
    ID string `json:"id"`
    FullMethod string `json:"full_method"`
    ProtoPackage string `json:"proto_package"`
    Service string `json:"service"`
    Method string `json:"method"`
    StreamingMode GrpcStreamingMode `json:"streaming_mode"`
    ClientBindings []GrpcClientBinding `json:"client_bindings"`
    Evidence []EvidenceFact `json:"evidence"`
}

type GrpcCallFact struct {
    ID string `json:"id"`
    CallerSymbol SymbolID `json:"caller_symbol"`
    OperationID string `json:"operation_id"`
    ClientBinding GrpcClientBinding `json:"client_binding"`
    Span SourceSpan `json:"span"`
    Evidence []EvidenceFact `json:"evidence"`
}
```

generated evidence 的 `Span.File` 必须使用稳定逻辑路径 `dependency/<go-import-path>/<basename>`，不能暴露 module cache、workspace 或 local replace 的绝对路径；BFF call evidence 继续使用项目相对路径。

- [ ] **Step 4: 接入 Store、RenderJSON 与现有 facts schema**

将两个数组加入 `facts.Store`、`facts.NewStore`、`output.Document`、`RenderJSON`、`ensureNonNilSlices` 和 `factsDefinitions()`。排序规则为 operation `ID`、call `ID`；operation 内 bindings 由 extractor 提前排序。

不得给 `SchemaJSON` 增加 `endpoint-assets` 或 `grpc-consumers` 类型。

- [ ] **Step 5: 更新 mini-bff golden 并验证字节稳定**

Run: `UPDATE_GOLDEN=1 go test ./internal/output -run TestMiniBFFGolden`

Run: `go test ./internal/facts ./internal/output`

Expected: PASS；mini BFF facts 新增 `"grpc_operations": []`、`"grpc_calls": []`。

- [ ] **Step 6: 提交**

```bash
git add internal/facts/grpc.go internal/facts/store.go internal/output/schema.go internal/output/json.go internal/output/contract.go internal/output/contract_alignment_test.go internal/output/golden_test.go testdata/golden/mini-bff.facts.json
git commit -m "feat: add gRPC analysis facts"
```

### Task 2: 实现只读依赖包发现

**Files:**
- Create: `internal/project/dependencies.go`
- Create: `internal/project/dependencies_test.go`
- Create: `testdata/fixtures/grpc-proto/go.mod`
- Create: `testdata/fixtures/grpc-proto/order/order_grpc.pb.go`
- Create: `testdata/fixtures/grpc-proto/legacy/legacy.pb.go`
- Create: `testdata/fixtures/grpc-dependencies/go.mod`

- [ ] **Step 1: 写 dependency discovery 失败测试**

测试通过临时 module 或 checked-in sibling fixtures 断言：

```go
pkgs, err := DiscoverDependencies(ctx, bffRoot, BuildContextOptions{
    GOOS: "linux", GOARCH: "amd64", Tags: []string{"grpcfixture"}, CgoEnabled: boolPtr(false),
})
```

必须覆盖：

- 当前 module 的 local `replace ../grpc-proto` 被选中。
- `_test.go` 不出现在 `GoFiles`。
- `GOWORK=off` 隔离 ambient workspace。
- inherited `GOFLAGS=-mod=mod -tags=wrong` 不污染结果。
- `go env -w GOFLAGS=...` 写入隔离 `GOENV` 文件的持久配置不污染结果。
- 非 vendor 模式显式 `-mod=readonly`。
- active vendor 模式显式 `-mod=vendor`。
- 执行前后的 `go.mod`、存在时的 `go.sum` SHA-256 完全一致。
- `go list` 非零退出返回可分类错误，不扫描其他 module cache 版本。

- [ ] **Step 2: 运行测试确认 API 尚不存在**

Run: `go test ./internal/project -run TestDiscoverDependencies -count=1`

Expected: FAIL，`DiscoverDependencies` 未定义。

- [ ] **Step 3: 实现 discovery 数据结构与命令执行器**

核心 API：

```go
type DependencyPackage struct {
    ImportPath string
    Dir string
    GoFiles []string
    ModulePath string
    ModuleVersion string
    ModuleDir string
    Replace *DependencyModule
}

type DependencyDiscoveryError struct { Err error }

func DiscoverDependencies(ctx context.Context, root string, build BuildContextOptions) ([]DependencyPackage, error)
```

实现要求：

- `exec.CommandContext(ctx, "go", args...)`，`Dir=root`。
- args 使用 `go list -deps -json -mod=<vendor|readonly> [-tags=...] ./...`。
- 环境从 `os.Environ()` 复制后删除已有 `GOFLAGS`、`GOWORK`、`GOOS`、`GOARCH`、`CGO_ENABLED`，再显式写入 `GOFLAGS=`、`GOWORK=off` 和 analyzer 的 build context。必须设置空 `GOFLAGS` 环境变量，不能只删除它；否则 `go env -w GOFLAGS=...` 的持久配置仍会生效。
- 用 `json.Decoder` 连续解码多个 package object，不按行切 JSON。
- 过滤 `Standard=true` 的标准库包，并按 import path 排除当前 module 自身 package；不得用
  `DepOnly` 排除依赖，因为 `go list -deps` 的目标依赖通常正是 `DepOnly=true`。
- 对 `GoFiles`、最终 package 数组稳定排序并去重。
- vendor 判定遵循 Go active vendor 条件；判定逻辑独立为可单测函数。
- 调用前后读取 `go.mod`、可选 `go.sum`，发现内容变化即返回错误。

- [ ] **Step 4: 验证 readonly、workspace 和 build context 测试**

Run: `go test ./internal/project -run 'TestDiscoverDependencies|TestDependencyModuleMode' -count=1`

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/project/dependencies.go internal/project/dependencies_test.go testdata/fixtures/grpc-proto testdata/fixtures/grpc-dependencies/go.mod
git commit -m "feat: discover selected Go dependency packages"
```

### Task 3: 抽取共享函数作用域类型解析器

**Files:**
- Create: `internal/astindex/scoped_values.go`
- Create: `internal/astindex/scoped_values_test.go`
- Modify: `internal/extract/reference/scoped_types.go:14-244`
- Modify: `internal/extract/reference/extractor.go` 中 `functionBodyContext` 的创建和 receiver 解析调用
- Test: `internal/extract/reference/extractor_test.go`

- [ ] **Step 1: 为共享 scope API 写失败测试**

API 目标：

```go
scope := astindex.NewFunctionScope(file, idx, fn)
types := scope.ResolveValueTypes(receiverExpr, receiverExpr.Pos())
```

测试必须覆盖：显式参数类型、receiver、struct field、`:=`、`var`、`new(T)`、项目 constructor/getter 返回类型、import alias、map/interface 唯一绑定、同名局部变量遮蔽和多候选不收敛。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/astindex -run TestFunctionScope -count=1`

Expected: FAIL，`NewFunctionScope` 未定义。

- [ ] **Step 3: 移动而不是复制现有推断逻辑**

将 `internal/extract/reference/scoped_types.go` 中的 `scopedValueTypes`、参数/局部声明收集、`scopedTypesFromTypeExpr`、`scopedTypesFromValueExpr`、`scopedCallableID` 移入 `internal/astindex/scoped_values.go`。

公开最小 API：

```go
type FunctionScope struct { /* private indexes */ }
func NewFunctionScope(file *project.File, idx *Index, fn *ast.FuncDecl) *FunctionScope
func (s *FunctionScope) ResolveValueTypes(expr ast.Expr, pos token.Pos) []ValueType
```

返回值必须去重、稳定排序；多个候选必须全部返回，不能隐式选第一个。不要在共享层引入 gRPC 名称或 catalog 概念。

- [ ] **Step 4: reference extractor 改用共享 API**

保留 reference extractor 自己的 `ignored`、`callFuns` 语法位置集合；仅类型解析迁移到 `astindex.FunctionScope`。删除重复私有实现，必要时把 `scoped_types.go` 缩减为 reference 专用的语法位置 context。

- [ ] **Step 5: 验证 reference 行为零回归**

Run: `go test ./internal/astindex ./internal/extract/reference -count=1`

Run: `go test ./internal/graph ./internal/impact -count=1`

Expected: PASS，现有 reference 数量和歧义诊断不变化。

- [ ] **Step 6: 提交**

```bash
git add internal/astindex/scoped_values.go internal/astindex/scoped_values_test.go internal/extract/reference/scoped_types.go internal/extract/reference/extractor.go internal/extract/reference/extractor_test.go
git commit -m "refactor: share scoped receiver type resolution"
```

### Task 4: 构建 generated gRPC catalog

**Files:**
- Create: `internal/extract/grpc/catalog.go`
- Create: `internal/extract/grpc/catalog_test.go`
- Modify: `testdata/fixtures/grpc-proto/order/order_grpc.pb.go`
- Modify: `testdata/fixtures/grpc-proto/legacy/legacy.pb.go`

- [ ] **Step 1: 写现代、旧版和 streaming 的 table tests**

每个 case 输入 `[]project.DependencyPackage`，断言 catalog key：

```go
type BindingKey struct {
    GoPackage string
    ClientType string
    GoMethod string
}
```

精确映射到 generated transport 中的 full method。测试包括：

- `*_FullMethodName` 常量 + `Invoke`。
- literal full method + `Invoke`。
- `NewStream` 对应 client/server/bidi streaming。
- Go method 大小写与 canonical method 不同。
- 两个 service 同名 method。
- 一个 operation 的多个 binding。
- 一个 binding key 对应两个 operation 时失败。
- 名为 `ServiceClient` 但没有 generated marker/constructor/transport 的 decoy 不进入 catalog。
- dynamic full method 和无法解析 source 返回错误，不能部分成功。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/extract/grpc -run TestBuildCatalog -count=1`

Expected: FAIL，package/API 不存在。

- [ ] **Step 3: 实现 parser 与 catalog 索引**

核心 API：

```go
type Catalog struct {
    Operations []facts.GrpcOperationFact
    ByBinding map[BindingKey]CatalogEntry
}

func BuildCatalog(pkgs []project.DependencyPackage) (*Catalog, error)
func (c *Catalog) Lookup(key BindingKey) (CatalogEntry, bool)
```

单个 generated package 的处理顺序：

1. 只解析 `GoFiles` 指定的活动源码。
2. 检查 generated file marker。
3. 收集 string const。
4. 收集 exported client interface method signatures。
5. 从 constructor 返回 concrete client 表达式建立 concrete receiver -> exported interface。
6. 从 concrete receiver method 的 `Invoke` / `NewStream` 提取 full method。
7. 根据 interface method 的 stream 参数/返回类型和 `NewStream` 形态确定 streaming mode。
8. 写入 binding key，检测冲突。

operation 的 proto package/service/method 必须从 full method 拆分。generated evidence 使用逻辑路径，不得把 `DependencyPackage.Dir` 写进 facts。

- [ ] **Step 4: 验证 catalog 精度和稳定排序**

Run: `go test ./internal/extract/grpc -run TestBuildCatalog -count=1`

Expected: PASS；对 package 输入乱序运行两次，序列化后的 operation/binding 顺序完全相同。

- [ ] **Step 5: 提交**

```bash
git add internal/extract/grpc/catalog.go internal/extract/grpc/catalog_test.go testdata/fixtures/grpc-proto
git commit -m "feat: build generated gRPC client catalog"
```

### Task 5: 抽取项目内 gRPC 调用事实

**Files:**
- Create: `internal/extract/grpc/extractor.go`
- Create: `internal/extract/grpc/extractor_test.go`
- Modify: `internal/diagnostics/codes.go:7-40`
- Create: `testdata/fixtures/grpc-dependencies/controller/order.go`
- Create: `testdata/fixtures/grpc-dependencies/service/order.go`
- Create: `testdata/fixtures/grpc-dependencies/remote/order.go`
- Create: `testdata/fixtures/grpc-dependencies/router/router.go`

- [ ] **Step 1: 写真实 receiver 范式失败测试**

加载 `grpc-dependencies`，构建 index/catalog，调用：

```go
result, err := grpc.Extract(project, index, catalog)
```

断言识别 package-level client、struct field、scoped local、项目 constructor/getter、import alias、唯一 interface binding、controller direct call、remote wrapper、getter chain。

每个正式 `GrpcCallFact.Evidence` 必须同时断言存在两类证据：项目内 BFF selector call expression，以及命中的 generated catalog transport entry；任一证据缺失都不得生成正式 fact。

反例必须断言不产生 fact：名称相似但无类型证据、protobuf message-only、HTTP/Redis 同名 method、业务源码 direct `Invoke/NewStream`、`_test.go` mock、外部 SDK hidden call。

- [ ] **Step 2: 写歧义和 catalog 漂移失败测试**

- receiver 候选命中两个不同 generated binding/operation：返回 `grpc_call_ambiguous`。
- receiver 候选命中同一 operation 的不同 generated binding：无法收敛到唯一 binding，不生成正式 fact。
- receiver 候选同时包含一个 generated binding 和一个未知候选：无法证明 receiver，不生成正式 fact。
- receiver 已精确确定为 generated client，但 method 不存在于 catalog：返回 catalog incomplete error。
- 普通非 generated receiver 的未知 method：忽略，不报错。

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/extract/grpc -run 'TestExtract|TestAmbiguous' -count=1`

Expected: FAIL，`Extract` 未定义。

- [ ] **Step 4: 实现 selector call 抽取**

核心返回类型：

```go
type ExtractResult struct {
    Calls []facts.GrpcCallFact
    Diagnostics []Diagnostic
}
```

对每个项目 `FuncDecl`：

1. 建立 `astindex.FunctionScope`。
2. 遍历 `CallExpr`，只处理 selector callee。
3. 对 selector receiver 调用 `ResolveValueTypes`。
4. 将每个候选转换为 `BindingKey{PackagePath, TypeName, Sel.Name}`。
5. 只有全部 receiver 候选都能映射、且收敛到同一个 generated `BindingKey` 时才生成 call fact；不同 binding 即使指向同一 operation 也不能任选其一，混有未知候选同样不得生成。
6. 多个可证明 generated 候选映射到不同 operation 时返回 `grpc_call_ambiguous` typed extraction error；不能收敛但不构成 operation 冲突时只保留 diagnostic，不进入正式 facts。
7. caller 使用 enclosing function/method 的稳定 `SymbolID`。
8. call ID 包含 caller、operation 和 call-site span，确保同 operation 多 call-site 不合并。
9. `GrpcCallFact.Evidence` 同时复制项目内 selector call evidence 和 catalog entry 的 generated transport evidence；不得仅记录其中一侧。

所有 BFF span 使用 `project.Root` 相对路径并 `filepath.ToSlash`。call facts 按 caller、file、line、column、operation 排序。

- [ ] **Step 5: 验证 extractor**

Run: `go test ./internal/astindex ./internal/extract/reference ./internal/extract/grpc -count=1`

Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/extract/grpc internal/diagnostics/codes.go testdata/fixtures/grpc-dependencies
git commit -m "feat: extract proven BFF gRPC calls"
```

### Task 6: 为共享 pipeline 加入 off/diagnostic/strict 模式

**Files:**
- Create: `internal/app/errors.go`
- Modify: `internal/app/options.go:6-28`
- Modify: `internal/app/pipeline.go:36-344`
- Modify: `internal/app/pipeline_test.go`
- Modify: `internal/diagnostics/codes.go`

- [ ] **Step 1: 写三种 mode 的失败测试**

构造 dependency discovery 失败或 catalog 冲突 fixture，断言：

- `RunImpactWithMetrics` 不出现 `dependency_list` / `grpc_extract` stage，且不受 gRPC 故障影响。
- `RunFactsWithMetrics` 成功返回其他 facts，并写入稳定 gRPC diagnostic。
- strict 内部构建返回带 code 的 error，且不进入 query/render。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/app -run 'Test.*GrpcMode' -count=1`

Expected: FAIL，pipeline 尚无 mode。

- [ ] **Step 3: 定义 typed error 与内部 mode**

`internal/app/errors.go`：

```go
type ErrorCode string

const (
    ErrorInvalidEndpoint ErrorCode = "invalid_endpoint"
    ErrorEndpointNotFound ErrorCode = "endpoint_not_found"
    ErrorInvalidGrpcMethod ErrorCode = "invalid_grpc_method"
    ErrorProjectLoadFailed ErrorCode = "project_load_failed"
    ErrorDependencyLoadFailed ErrorCode = "dependency_load_failed"
    ErrorGrpcCatalogFailed ErrorCode = "grpc_catalog_failed"
    ErrorGrpcCallAmbiguous ErrorCode = "grpc_call_ambiguous"
)

type AnalysisError struct {
    Code ErrorCode
    Message string
    Err error
}

func (e *AnalysisError) Error() string
func (e *AnalysisError) Unwrap() error
```

内部选项：

```go
type grpcMode uint8
const (
    grpcModeOff grpcMode = iota
    grpcModeDiagnostic
    grpcModeStrict
)

type buildFactsOptions struct { grpcMode grpcMode }
```

- [ ] **Step 4: 接入 buildFacts 阶段**

- `impact` 调 `buildFacts(..., buildFactsOptions{grpcMode: grpcModeOff})`。
- `facts` 使用 `grpcModeDiagnostic`。
- mode 非 off 才记录 `dependency_list`、`grpc_extract`。
- diagnostic 模式将 dependency/catalog/extractor 错误转换成 `DiagnosticFact`，不产生部分 gRPC facts。
- strict 模式将错误映射成 `AnalysisError` 并立即返回。
- project loader 的基础失败映射为 `project_load_failed`；非查询命令现有 message 行为保持兼容。

- [ ] **Step 5: 验证 mode 与现有 pipeline**

Run: `go test ./internal/app -count=1`

Run: `go test ./internal/output -run TestMiniBFFGolden -count=1`

Expected: PASS；impact metrics 不含新增阶段。

- [ ] **Step 6: 提交**

```bash
git add internal/app/errors.go internal/app/options.go internal/app/pipeline.go internal/app/pipeline_test.go internal/diagnostics/codes.go
git commit -m "feat: add gRPC extraction pipeline modes"
```

### Task 7: 构建可执行调用图

**Files:**
- Create: `internal/graph/call.go`
- Create: `internal/graph/call_test.go`
- Read/Reuse: `internal/facts/reference.go`

- [ ] **Step 1: 写 call-only 图失败测试**

构造包含 `call`、`type`、`value` reference 和多个 `GrpcCallFact` 的 Store，断言：

```go
g := graph.NewCallGraph(store)
g.Callees(caller)
g.Callers(callee)
g.GrpcCalls(caller)
```

只返回 executable call edge；type/value reference 必须完全不可达。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/graph -run TestCallGraph -count=1`

Expected: FAIL，`NewCallGraph` 未定义。

- [ ] **Step 3: 实现双向索引**

```go
type CallGraph struct {
    Forward map[facts.SymbolID][]facts.ReferenceFact
    Reverse map[facts.SymbolID][]facts.ReferenceFact
    GrpcByCaller map[facts.SymbolID][]facts.GrpcCallFact
}
```

构造时仅接收 `ref.Kind == facts.ReferenceKindCall` 且 from/to 非空的边。所有数组按 target/source、span、ID 稳定排序并去重；不修改现有 `ReverseGraph`，避免改变 impact 语义。

- [ ] **Step 4: 验证环和多 call-site**

Run: `go test ./internal/graph -run TestCallGraph -count=1`

Expected: PASS；A->B->A 不造成构造或查询异常，同一 operation 的两个 call site 都保留。

- [ ] **Step 5: 提交**

```bash
git add internal/graph/call.go internal/graph/call_test.go
git commit -m "feat: add executable call graph"
```

### Task 8: 实现 endpoint 与 gRPC 双向查询

**Files:**
- Create: `internal/dependency/query.go`
- Create: `internal/dependency/query_test.go`
- Reuse: `internal/graph/call.go`
- Reuse: `internal/graph/route.go`

- [ ] **Step 1: 写输入解析和 endpoint identity 失败测试**

```go
endpoint, err := dependency.ParseEndpoint("get /orders/:id")
grpcMethod, err := dependency.ParseGrpcMethod("/package.OrderService/GetOrder")
```

断言 method 归一化；空 method/path、多余 token、非 `/` path、grpc 缺 service/method、多余 slash 均失败。重复输入去重并排序。

- [ ] **Step 2: 写双向关系 fixture 测试**

用内存 Store 构建：一个 endpoint 多 operation、一个 operation 多 endpoint、一个 endpoint 多 handler、同 operation 多 call-site、递归环和两条等长路径。

必须断言：

```go
forward := dependency.FindEndpointAssets(store, endpoints)
reverse := dependency.FindGrpcConsumers(store, grpcMethods)
assertForwardReverseInvariant(t, forward, reverse)
```

同时断言不存在 endpoint 是 `endpoint_not_found`，存在但无 gRPC 返回空数组，合法但未消费的 gRPC 返回空 consumers，批量输入任一 endpoint 不存在则整批失败。

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/dependency -count=1`

Expected: FAIL，package/API 不存在。

- [ ] **Step 4: 实现查询领域模型**

查询层返回不带 JSON tag 的内部结构：

```go
type Endpoint struct { Method, Path string }
type SymbolChain struct {
    Handler facts.SymbolID
    Symbols []facts.SymbolID
    Call facts.GrpcCallFact
}
type EndpointAsset struct { Endpoint Endpoint; Handlers []facts.SymbolID; Grpc []GrpcDependency }
type GrpcMethodIdentity struct { FullMethod, ProtoPackage, Service, Method string }
type GrpcConsumerResult struct { Grpc GrpcMethodIdentity; Consumers []EndpointConsumer }
```

endpoint identity 解析规则：优先使用 `RouteGraph.AnnotationsForHandler` 的 method/path；没有 annotation 时使用已解析 route method/path。只有被 route 注册证明的 handler 才能成为 endpoint。

- [ ] **Step 5: 实现确定性 BFS**

- Forward：handler -> project callees -> `GrpcCallFact` terminal。
- Reverse：gRPC caller -> project callers -> endpoint handlers。
- 不设置 `maxDepth`，visited key 至少包含当前 symbol；路径重建保存 predecessor。
- 最短路径长度优先；等长路径按完整 symbol ID 序列字典序选择。
- 每个 `(handler, grpc call-site)` 保留一条路径；不同 call-site 不合并。
- 输出链统一为 endpoint -> gRPC。
- `grpc-consumers` 的结果 identity 直接从合法 canonical 输入拆分，因此 catalog 中不存在、当前 BFF 未消费的 gRPC 仍能返回空 consumers。
- 只验证实际 `GrpcCallFact.OperationID` 必须能在 `GrpcOperations` 中找到；事实库出现悬空 call fact 才属于分析错误，不能要求每个查询输入预先存在于 catalog。

- [ ] **Step 6: 验证双向不变量与排序**

Run: `go test ./internal/dependency -count=1`

Expected: PASS；测试显式遍历所有 forward pair，并在 reverse 中找到相同 endpoint/grpc pair，反向亦然。

- [ ] **Step 7: 提交**

```bash
git add internal/dependency/query.go internal/dependency/query_test.go
git commit -m "feat: query endpoint and gRPC dependency relations"
```

### Task 9: 实现两个正式 JSON 输出 contract

**Files:**
- Create: `internal/output/dependency.go`
- Create: `internal/output/endpoint_assets.go`
- Create: `internal/output/grpc_consumers.go`
- Create: `internal/output/dependency_test.go`
- Create: `testdata/golden/endpoint-assets.json`
- Create: `testdata/golden/grpc-consumers.json`

- [ ] **Step 1: 写 contract 与 golden 失败测试**

测试从 Task 8 的查询结果构建两个 document，并断言顶层键严格为：

```text
endpoint-assets: project, endpointAssets
grpc-consumers: project, grpcConsumers
```

字段命名严格对齐设计：`buildContext`、`endpointAssets`、`fullMethod`、`protoPackage`、`callSite` 等 camelCase。第一版不得输出 `contract`、`http`、`events` 或 diagnostics 占位。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/output -run 'TestEndpointAssets|TestGrpcConsumers' -count=1`

Expected: FAIL，document/render API 不存在。

- [ ] **Step 3: 实现共享 contract**

`internal/output/dependency.go` 定义共享结构：

```go
type DependencyProject struct { Module string; BuildContext DependencyBuildContext }
type DependencyEndpoint struct { Method, Path string }
type DependencyHandler struct { ID, Kind, Name, File string }
type DependencyClient struct { GoPackage, ClientType, GoMethod string }
type DependencyChain struct { Symbols []DependencyHandler; CallSite DependencyCallSite }
type DependencyGrpc struct { FullMethod, ProtoPackage, Service, Method string; Clients []DependencyClient; Chains []DependencyChain }
```

projection 通过 Store symbol 表补充 handler/symbol 的 kind/name/file。所有文件路径必须验证为相对路径，不得含 `..` 或绝对前缀。

- [ ] **Step 4: 实现稳定渲染**

- 所有 nil slice 转为空数组。
- endpoint 按 method/path；gRPC 按 full method；handler 按 ID；client 按 package/type/method；chain 按 call-site 和 symbol 序列排序。
- `json.MarshalIndent` 后追加换行。
- 对输入查询结果随机乱序多次渲染，字节必须完全一致。

- [ ] **Step 5: 生成并锁定 golden**

Run: `UPDATE_GOLDEN=1 go test ./internal/output -run 'TestEndpointAssetsGolden|TestGrpcConsumersGolden'`

Run: `go test ./internal/output -count=1`

Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/output/dependency.go internal/output/endpoint_assets.go internal/output/grpc_consumers.go internal/output/dependency_test.go testdata/golden/endpoint-assets.json testdata/golden/grpc-consumers.json
git commit -m "feat: render dependency asset reports"
```

### Task 10: 增加 app entry 与 CLI 子命令

**Files:**
- Create: `internal/app/dependency.go`
- Create: `internal/app/dependency_test.go`
- Modify: `internal/app/options.go`
- Modify: `cmd/go-analyzer/main.go:22-240`
- Modify: `cmd/go-analyzer/main_test.go`

- [ ] **Step 1: 写 app API 失败测试**

新增 options：

```go
type EndpointAssetsOptions struct {
    ProjectPath string
    Endpoints []string
    Format string
    BuildContext project.BuildContextOptions
}

type GrpcConsumersOptions struct {
    ProjectPath string
    GrpcMethods []string
    Format string
    BuildContext project.BuildContextOptions
}
```

测试 `RunEndpointAssetsWithMetrics` / `RunGrpcConsumersWithMetrics` 使用 strict mode，先完成所有输入与分析校验，再调用 render；任何错误时 `RunResult.Output` 必须为空。

- [ ] **Step 2: 写 CLI 失败测试**

覆盖：

- 重复 `--endpoint` / `--grpc`。
- 缺少输入 flag。
- project 必须为绝对路径。
- build context 与 `--timings` 透传。
- `--format` 仅支持 json。
- 成功 stdout 是单个完整 JSON，timings 只在 stderr。
- typed error stderr：`error_code=<code> message=<message>`。
- `help endpoint-assets` / `help grpc-consumers`。
- `schema --type endpoint-assets` 仍然失败。

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/app ./cmd/go-analyzer -run 'TestRunEndpoint|TestRunGrpc|TestUsage' -count=1`

Expected: FAIL，新入口与子命令不存在。

- [ ] **Step 4: 实现 app pipeline**

每个 entry 的阶段：

```text
validate options
buildFacts(grpcModeStrict)
dependency_query
dependency_render
```

所有 endpoint 先统一 parse；任一不存在时在 render 前失败。`grpc-consumers` 对合法但未被消费的 full method 正常返回空 consumers。metrics 顺序固定。

- [ ] **Step 5: 实现 repeatable flag 与错误渲染**

在 CLI 定义 `stringListFlag` 实现 `flag.Value`，不要用逗号拆分 canonical value。`main()` 的错误输出改为：typed `AnalysisError` 使用稳定前缀，其他历史错误保持原 message，避免破坏 facts/impact CLI 测试。

- [ ] **Step 6: 运行 app/CLI 测试**

Run: `go test ./internal/app ./cmd/go-analyzer -count=1`

Expected: PASS。

- [ ] **Step 7: 提交**

```bash
git add internal/app/dependency.go internal/app/dependency_test.go internal/app/options.go cmd/go-analyzer/main.go cmd/go-analyzer/main_test.go
git commit -m "feat: add endpoint and gRPC dependency commands"
```

### Task 11: 增加 fixture 端到端 facts 与双向 golden

**Files:**
- Modify: `internal/output/golden_test.go`
- Create: `testdata/golden/grpc-dependencies.facts.json`
- Verify: `testdata/fixtures/grpc-dependencies/**`
- Verify: `testdata/fixtures/grpc-proto/**`

- [ ] **Step 1: 写完整 fixture pipeline golden test**

`TestGrpcDependenciesFactsGolden` 必须通过 `app.RunFacts`，而不是手工拼 Store；`TestEndpointAssetsGolden` 和 `TestGrpcConsumersGolden` 必须通过 app entry。归一化仅允许 project root/build context，不得删除 call-site、handler、client 或 chain 字段。

- [ ] **Step 2: 运行测试确认 golden 缺失或不匹配**

Run: `go test ./internal/output -run 'TestGrpcDependencies|TestEndpointAssetsGolden|TestGrpcConsumersGolden' -count=1`

Expected: FAIL，提示 golden 缺失或 mismatch。

- [ ] **Step 3: 生成 golden 并人工检查关键关系**

Run: `UPDATE_GOLDEN=1 go test ./internal/output -run 'TestGrpcDependencies|TestEndpointAssetsGolden|TestGrpcConsumersGolden' -count=1`

人工检查：

- canonical method 来自 generated transport literal/const。
- 每个 gRPC call fact 的 evidence 同时包含 BFF call expression 和 generated catalog transport entry。
- chain 从 endpoint handler 开始，以项目内 gRPC call-site caller 结束。
- proto fixture 的绝对目录没有进入 JSON。
- direct/wrapper/getter 场景都存在。
- decoy/message-only/direct Invoke 不存在。

- [ ] **Step 4: 重跑确保 golden 字节稳定**

Run: `go test ./internal/output -run 'TestGrpcDependencies|TestEndpointAssetsGolden|TestGrpcConsumersGolden' -count=2`

Expected: PASS 两次。

- [ ] **Step 5: 提交**

```bash
git add internal/output/golden_test.go testdata/golden/grpc-dependencies.facts.json testdata/golden/endpoint-assets.json testdata/golden/grpc-consumers.json testdata/fixtures/grpc-dependencies testdata/fixtures/grpc-proto
git commit -m "test: cover gRPC dependency queries end to end"
```

### Task 12: 更新正式文档与三个真实 BFF smoke baseline

**Files:**
- Modify: `README.md:89-163`
- Modify: `ARCHITECTURE.md`
- Modify: `docs/contracts/output-contract.md`
- Modify: `docs/validation/real-project-validation.md`
- Modify: `scripts/smoke-real-projects.sh:101-205` 及项目执行区
- Modify: `testdata/baselines/real-project-facts.json`

- [ ] **Step 1: 扩展 smoke baseline 结构**

每个真实项目增加：

```json
{
  "grpc_operations": 0,
  "grpc_calls": 0,
  "endpoint_grpc_relations": 0,
  "cases": [
    {
      "endpoint": "GET /exact/path",
      "grpc": "/package.Service/Method"
    }
  ]
}
```

具体 case 必须从各仓当前代码中选择稳定、可人工核对的真实调用，不得使用模糊搜索结果。

- [ ] **Step 2: 扩展 smoke 脚本硬不变量**

对三个项目运行 facts，并针对 baseline `cases` 分别执行 `endpoint-assets` 和 `grpc-consumers`。脚本必须检查：

- facts operation/call 数量。
- 每个固定 endpoint 的正向结果包含指定 full method。
- 每个 full method 的反向结果包含相同 endpoint。
- 两边 chain 至少一条且方向一致。
- stdout JSON 可解析，stderr timings 不混入文件。
- 没有新增 gRPC ambiguity/catalog diagnostic。

数量可以沿用现有 tolerance 策略；固定关系和双向不变量必须是零容忍硬门禁。

- [ ] **Step 3: 运行真实项目 smoke 并记录 baseline**

Run: `SMOKE_STRICT=1 ./scripts/smoke-real-projects.sh`

Expected: 第一次因 baseline 尚未更新而 FAIL，并打印三个项目实际 operation/call/relation 数量。

根据正式输出更新 `testdata/baselines/real-project-facts.json`，再运行同一命令。

Expected: PASS，覆盖：

- `sl-sc1-admin-bff`：package-level client、wrapper、direct call、generated getter chain。
- `sl-sc2-admin-bff`：旧版 generated transport、Go/canonical method 大小写差异。
- `sl-sc1-bff-service`：controller direct call、project-local wrapper。

- [ ] **Step 4: 更新使用与架构文档**

README 增加两个命令示例和输入格式；ARCHITECTURE 记录 dependency discovery/catalog/call graph/mode；output contract 记录两个 JSON 文档；validation 文档记录固定 case、命令和结果。

文档必须明确：单项目、多个 gRPC 输入、precision-first、外部 SDK 不穿透、direct transport 不支持、新查询无 schema type。

- [ ] **Step 5: 提交**

```bash
git add README.md ARCHITECTURE.md docs/contracts/output-contract.md docs/validation/real-project-validation.md scripts/smoke-real-projects.sh testdata/baselines/real-project-facts.json
git commit -m "docs: validate BFF gRPC dependency analysis"
```

### Task 13: 全量验证与最终代码审查

**Files:**
- Verify all changed files

- [ ] **Step 1: 格式化和静态检查**

Run: `gofmt -w cmd/go-analyzer internal/app internal/astindex internal/dependency internal/diagnostics internal/extract/grpc internal/extract/reference internal/facts internal/graph internal/output internal/project testdata/fixtures/grpc-dependencies testdata/fixtures/grpc-proto`

Run: `go vet ./...`

Expected: PASS，无输出。

- [ ] **Step 2: 运行全量单元和 golden 测试**

Run: `go test ./... -count=1`

Expected: PASS。

- [ ] **Step 3: 验证 race-sensitive 共享状态**

Run: `go test -race ./internal/project ./internal/extract/grpc ./internal/dependency ./internal/app ./cmd/go-analyzer`

Expected: PASS；catalog/scope/query 不使用可变全局状态。

- [ ] **Step 4: 验证 CLI 构建和帮助**

Run: `go build -o /tmp/go-analyzer ./cmd/go-analyzer`

Run: `/tmp/go-analyzer help endpoint-assets`

Run: `/tmp/go-analyzer help grpc-consumers`

Expected: 两个 help 均列出 repeatable 输入和公共 build context 参数。

- [ ] **Step 5: 运行真实项目验收**

Run: `SMOKE_STRICT=1 ./scripts/smoke-real-projects.sh`

Expected: PASS；脚本退出前恢复其临时应用的所有 diff。

- [ ] **Step 6: 检查目标仓零修改和输出无绝对依赖路径**

Run: `git status --short`

Run: `rg -n '/Users/|/go/pkg/mod|\.codex/' .analyzer-smoke testdata/golden -g '*.json'`

Expected: 除计划内文件无意外修改；路径扫描无命中。

- [ ] **Step 7: 使用 @superpowers:requesting-code-review 做最终审查**

审查重点：

- 是否存在任何名称猜测或低置信度补全。
- generated catalog 是否可能选错 module 版本。
- type/value reference 是否误入可执行图。
- strict mode 是否可能输出 partial JSON。
- 所有 forward/reverse relation 是否满足不变量。
- impact 是否保持 grpc mode off。

- [ ] **Step 8: 修复审查问题后重复对应验证**

Critical/Important 问题必须修复并重跑受影响 package、`go test ./...` 和真实 smoke。Minor 问题若明确延期，必须记录到 review 结论，不能静默忽略。

- [ ] **Step 9: 提交最终验证修订**

```bash
git add -A
git commit -m "test: harden gRPC dependency analysis"
```

如果审查后没有文件变化，则不创建空 commit。

## 4. 完成定义

以下条件必须同时满足：

- `endpoint-assets` 支持多个 endpoint，`grpc-consumers` 支持多个 canonical gRPC method。
- 两个方向对同一项目快照/build context 的正式关系完全一致。
- generated 新旧 unary/streaming 代码均由 transport 证据识别，不依赖 Go method 推断 canonical method。
- 真实 BFF direct、wrapper、getter chain 均有固定验收 case。
- 不存在 endpoint 与不存在 gRPC dependency 能被稳定区分。
- strict 分析失败没有 stdout partial JSON，stderr 带稳定 error code。
- `facts` diagnostic、query strict、`impact` off 三种模式行为经过测试。
- facts schema 已同步；新命令没有新增 schema type。
- `go test ./...`、`go vet ./...`、关键包 race test、三个真实 BFF smoke 全部通过。
- 目标 BFF 的 `go.mod`、`go.sum` 和源码未被 analyzer 修改。
