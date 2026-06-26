# go-analyzer Handoff

这份文档给下一个接手 `go-analyzer` 的 agent 使用。目标是让接手者不用翻完整聊天记录，也能知道这个项目要做什么、已经做了什么、如何运行、当前架构是什么，以及后续应该优先做什么。

## 1. 项目目标

`go-analyzer` 是一个面向 Go BFF 项目的静态影响范围分析工具。

它的核心目标是把一次 Go MR 的 diff 转换成受影响的 HTTP 接口列表，帮助开发、测试和自动化流程判断本次后端改动需要重点回归哪些接口。

MVP 的主问题是：

```text
这次 Go BFF diff 影响了哪些 HTTP 接口？
```

当前策略是 annotation-first：

- controller 注释中的 `@Get` / `@Post` 等 annotation 是最终 HTTP endpoint 真值。
- route AST 负责证明 handler 被注册，并提供 route group、middleware、wrapper 等传播证据。
- MVP 暂不判断 annotation 和 route 是否一致，也不判断注释是否过期。
- 先保证“代码里有哪些事实”被准确、稳定、可追溯地抽出来。

## 2. 当前状态

项目已经具备独立 MVP 闭环，不再依赖 `visanal` / `nexus` 作为日常开发参考。

已经完成的能力：

- 加载 Go module，解析 Go AST，保留注释。
- 建立 package / file / symbol index。
- 提取 controller endpoint annotation。
- 提取 route registration、route group、middleware binding、handler wrapper。
- 提取 symbol reference 和 route-handler / handler-annotation link。
- 解析 unified diff，把变更映射到 symbol / route / annotation / middleware / file。
- 解析 `go.mod` dependency change，并映射本地 module usage。
- 从 change facts 传播到 impacted HTTP endpoints。
- 输出 impact evidence chain。
- 支持 JSON 配置扩展项目规则。
- CLI 支持 `facts`、`impact`、`schema`、`help`。
- 输出契约已通过 JSON Schema 暴露。
- 真实项目 smoke 可跑通。

最近的提交序列：

```text
c7f21f2 feat: publish analyzer output contracts
b46ae51 feat: configure analyzer extraction rules
5fc26ec feat: wire cli impact analysis
99dccd6 test: harden analyzer with diagnostics and smoke validation
06e2e1c feat: propagate impacts to annotated endpoints
c3c9ad4 feat: map diffs and go module changes
ef4a570 feat: build go bff fact extraction pipeline
```

当前真实项目 smoke 基线：

```text
sc1-bff-service: symbols=781 annotations=32 routes=32 diagnostics=0
sc1-admin-bff: symbols=5120 annotations=463 routes=490 diagnostics=0
```

## 3. 外部项目关系

这个集合仓里曾经有几个参考项目：

- `visanal`: 已有 Go 依赖分析项目，早期主要参考图结构、依赖传播和分层思路。
- `nexus`: 主要参考 `nexus/internal/transform` 中的 BFF annotation、route、handler 抽取思路。
- `sc1-admin-bff`: 真实 Go BFF 验收样本。
- `sc1-bff-service`: 真实 Go BFF 验收样本。

现在 `go-analyzer` 已经可以作为独立项目推进。

后续建议：

- 不再日常依赖 `visanal` / `nexus`。
- `sc1-admin-bff` 和 `sc1-bff-service` 只作为外部 smoke/demo 项目。
- 新增场景优先沉淀到 `testdata/fixtures`，不要把规则写死到某个外部项目。

## 4. 架构分层

核心目录：

```text
cmd/go-analyzer        CLI 入口
internal/app           pipeline 编排
internal/project       Go module 加载、文件扫描
internal/astindex      AST symbol index
internal/config        默认规则和 JSON 配置加载
internal/facts         facts 数据模型
internal/extract       annotation / route / reference / gomod 提取
internal/link          route-handler、handler-annotation 等事实关联
internal/diff          unified diff 和 go.mod change 映射
internal/graph         reverse reference graph、route graph、evidence graph
internal/impact        impact propagation
internal/output        JSON 输出和 schema contract
internal/diagnostics   非致命诊断
testdata/fixtures      单测 fixture
testdata/golden        golden output
docs                   架构、契约、验证和历史计划
scripts                smoke 脚本
```

当前主流程：

```text
CLI
  -> app pipeline
  -> load project
  -> build AST index
  -> extract facts
  -> link facts
  -> parse diff
  -> map changes
  -> impact propagation
  -> JSON output
```

Facts 输出主流程：

```text
go-analyzer facts
  -> project.Load
  -> astindex.Build
  -> annotation.ExtractWithConfig
  -> route.ExtractWithConfig
  -> link.Run
  -> reference.Extract
  -> output.RenderJSON
```

Impact 输出主流程：

```text
go-analyzer impact
  -> build fact store
  -> diff.ParseUnified
  -> diff.MapChanges
  -> impact.Analyze
  -> output.RenderImpactJSON
```

## 5. CLI 使用

CLI 边界要求路径使用绝对路径。

Facts：

```bash
go run ./cmd/go-analyzer facts --project /absolute/path/to/project --format json
```

Impact：

```bash
go run ./cmd/go-analyzer impact --project /absolute/path/to/project --diff /absolute/path/to/change.diff --format json
```

输出 schema：

```bash
go run ./cmd/go-analyzer schema --type facts
go run ./cmd/go-analyzer schema --type impact
```

帮助：

```bash
go run ./cmd/go-analyzer help
go run ./cmd/go-analyzer help facts
go run ./cmd/go-analyzer help impact
go run ./cmd/go-analyzer help schema
```

配置：

```bash
go run ./cmd/go-analyzer facts \
  --project /absolute/path/to/project \
  --config /absolute/path/to/go-analyzer.json \
  --format json
```

示例配置见：

```text
docs/examples/go-analyzer.config.json
```

## 6. 验证命令

常规验证：

```bash
go test ./...
```

检查 diff whitespace：

```bash
git diff --check
```

真实项目 smoke：

```bash
bash scripts/smoke-real-projects.sh
```

smoke 脚本假设 `sc1-bff-service` 和 `sc1-admin-bff` 是 `go-analyzer` 的 sibling directories。脚本内部会把 sibling project 解析成绝对路径再传给 CLI。

输出会写到：

```text
.analyzer-smoke/
```

该目录已在 `.gitignore` 中忽略。

Schema 和示例配置校验：

```bash
python3 -m json.tool docs/examples/go-analyzer.config.json > /dev/null
go run ./cmd/go-analyzer schema --type facts > /tmp/go-analyzer-facts.schema.json
go run ./cmd/go-analyzer schema --type impact > /tmp/go-analyzer-impact.schema.json
python3 -m json.tool /tmp/go-analyzer-facts.schema.json > /dev/null
python3 -m json.tool /tmp/go-analyzer-impact.schema.json > /dev/null
```

## 7. 重要设计原则

### 7.1 Annotation-first

MVP 以 controller annotation 作为 endpoint 真值。

原因：

- BFF route path 可能来自常量、helper、wrapper、legacy path、generated route。
- 第一版强行拼接所有 route path 容易制造“看似精确但实际错误”的输出。
- annotation 通常更接近外部 API contract。

route facts 的作用：

- 证明 handler 被注册。
- 传播 route group、middleware、wrapper 的影响。
- 作为 evidence chain 的一部分。

### 7.2 事实提取优先

当前阶段先提取准确的代码事实，不做业务正确性判断。

暂不做：

- annotation 与 route 是否一致的判断。
- annotation 是否过期的判断。
- 运行时 route table 还原。
- 动态分发、反射、复杂 DI 的完全精确分析。

### 7.3 配置扩展而不是项目硬编码

默认配置覆盖 `sc1-admin-bff` 和 `sc1-bff-service` 的主要模式。

特殊项目通过 JSON config 扩展：

- `project.skipDirs`
- `route.httpMethods`
- `route.handlerWrappers`
- `route.routeGroupWrappers`
- `route.generatedRouteCalls`
- `annotation.methods`

不要把某个业务仓的路径、package 名、controller 名写死在 extractor 里。

### 7.4 Diagnostics 不要静默丢失

遇到暂不支持但可识别的模式，优先输出 diagnostics。

已有诊断示例：

- `route_dynamic_path`
- `route_unresolved_handler`
- `route_wrapper_unsupported`
- `annotation_missing_for_handler`
- `module_usage_file_fallback`
- `module_unreferenced`

## 8. 关键文档

正式架构方案：

```text
docs/design/go-analyzer-mvp-architecture.md
```

输出契约：

```text
docs/contracts/output-contract.md
```

真实项目验证：

```text
docs/validation/real-project-validation.md
```

历史模块计划：

```text
docs/superpowers/plans/
```

示例配置：

```text
docs/examples/go-analyzer.config.json
```

## 9. 已知边界

当前 MVP 仍有这些边界：

- route path 拼接只作为辅助事实，不作为 endpoint 真值。
- dynamic route path 只保留 raw expression，并输出 diagnostic。
- indirect handler expression 如 map/slice lookup 会降级为 diagnostic。
- go.mod change 的 module usage 有 precise 和 file fallback 两种精度。
- impact evidence chain 已可用，但还没有针对真实 MR 的大规模人工验收集。
- `generatedRouteCalls` 已进入配置模型，但 generated route 专项规则仍可继续深化。

## 10. 建议下一步

优先级从高到低：

1. 真实 diff 场景验收

   为 `sc1-admin-bff` 和 `sc1-bff-service` 准备几类真实或模拟 diff：

   - service 方法变更。
   - controller 方法变更。
   - route registration 变更。
   - route group prefix 变更。
   - middleware binding 变更。
   - go.mod dependency upgrade / replace 变更。

   用 `go-analyzer impact` 跑输出，人工确认 impacted endpoints 是否符合预期。

2. 沉淀 fixture

   每发现一个真实项目模式，就抽成最小 fixture 放到 `testdata/fixtures`。

3. 提升 route wrapper / generated route 支持

   尤其关注 generated route、guard wrapper、跨函数 route helper 的 summary。

4. 增强 diagnostics 文档

   为每个 diagnostic code 写原因、影响和建议处理方式。

5. 与前端 analyzer 对接

   当前 impact output 已有 endpoint list，后续可以作为前端 analyzer 的 API 输入。

6. CI 化

   把 `go test ./...`、schema 校验和必要 smoke 纳入 CI。

## 11. 接手注意事项

- 修改代码前先跑相关测试，尽量保持 TDD。
- 不要把外部项目源码复制进 `go-analyzer`。
- 不要让文档出现本机绝对路径。
- CLI 输入路径保持绝对路径约束。
- 真实项目 smoke 输出目录 `.analyzer-smoke/` 不提交。
- 如果引入新规则，优先补 config、fixture、diagnostic，再考虑 extractor 逻辑。
- 如果新增输出字段，更新 `internal/output/contract.go` 和 `docs/contracts/output-contract.md`。
- 如果变更 facts JSON，检查 golden test 是否需要更新。
