# 下一个 Agent 交接提示词

## 可直接复制的提示词

```text
请接手 /Users/bird/Desktop/go-analyzer 项目。

这是一个全新的 Go BFF 影响范围分析工具项目，目标是分析前端团队维护的 Go BFF 项目 diff，最终输出“受影响的 HTTP 接口列表”。当前项目还没有进入编码阶段，已有 README 和技术方案文档，需要你先完整阅读：

1. /Users/bird/Desktop/go-analyzer/README.md
2. /Users/bird/Desktop/go-analyzer/docs/design/go-bff-impact-analysis-design.md

关键背景：
- 现有前端 analyzer 已经能基于 API 输入分析前端页面影响范围，但 go-analyzer MVP 不需要打通前端，只需要独立输出受影响 HTTP 接口。
- 第一批真实 BFF 项目是：
  - /Users/bird/Desktop/agent-factory/projects/sc1-admin-bff
  - /Users/bird/Desktop/agent-factory/projects/sc1-bff-service
- 两个项目大致遵循 router -> controller -> service -> remote。
- MVP 优先使用 controller 函数注释作为 HTTP 接口出口，例如 // @Get /admin/api/bff-web/...
- route AST 不强行完整拼接复杂 path，主要用于证明 handler 注册关系，以及传播 route group、中间件、guard/wrapper 变更造成的影响。
- go.mod 依赖变更也要作为影响源，先从 changed module 映射到本地 import usage，再传播到 HTTP endpoint。

请作为 Go 架构师继续推进。优先不要直接写分析器代码，先做以下工作：
1. 深入阅读两个真实 BFF 项目的 router/controller/service/middleware 结构。
2. 检查当前技术方案是否遗漏重要场景，尤其是 route wrapper、middleware object method、controller 注释缺失、go.mod 依赖变更。
3. 如果方案需要修正，先更新 docs/design/go-bff-impact-analysis-design.md。
4. 如果方案已经足够稳定，再输出 MVP 实现计划，按模块拆分 internal/diff、internal/goindex、internal/change、internal/refgraph、internal/route、internal/endpoint、internal/modimpact、internal/impact、internal/report。

注意：
- 不要把最终协议设计作为第一优先级，当前重点是依赖分析架构和真实 BFF 场景覆盖。
- 不要在 MVP 中分析底层 gRPC 跨仓影响。
- 不要把 route AST path 拼接当作唯一 HTTP 出口。
- 不要静默忽略无法解析的场景，应该进入 diagnostics。
```

## 当前项目状态

- GitHub 仓库：`https://github.com/Flying-Bird1999/go-analyzer.git`
- 本地路径：`/Users/bird/Desktop/go-analyzer`
- 当前阶段：方案设计阶段，尚未开始编码。
- 已提交内容：
  - `README.md`
  - `docs/design/go-bff-impact-analysis-design.md`

## 设计共识

核心目标：

```text
Go BFF diff -> 受影响 HTTP 接口
```

暂不做：

- 前端页面影响范围。
- 底层 gRPC 跨仓传播。
- 运行时 route table 抽取。
- AI 报告生成。

核心分析管线：

```text
diff
  -> 变更节点识别
  -> Go 语义索引
  -> 反向引用图
  -> route 领域图
  -> 影响传播
  -> 受影响 HTTP 接口
```

关键判断：

- Go BFF 不能只做 call graph，因为 route 注册里 controller 常作为函数值传入，而不是被直接调用。
- 应建模为 reverse reference graph：`被引用节点 -> 引用它的节点或代码位置`。
- controller method 不是终点，它还会被 route registration site 引用。
- route group prefix 变更、中间件挂载变更、中间件函数内部变更、route wrapper 变更都要能传播到受影响 handler。
- HTTP endpoint MVP 优先来自 controller 注释，route AST 只做辅助证据和影响传播。

## 真实项目重点观察位

`sc1-admin-bff`：

- `router/router.go`
- `router/mc/broadcast.go`
- `router/live/*`
- `router/mc/*`
- `controller/mc/broadcast/broadcast.go`
- `middleware/*`
- `util/guard/*`
- `nexus/codegen/apis.RegisterRouters(g)`

`sc1-bff-service`：

- `router/router.go`
- `router/mc/app_proxy/app_proxy.go`
- `router/common/common.go`
- `router/live/view.go`
- `middleware/app_proxy_auth/*`
- `middleware/load_app_proxy.go`
- `nexus/codegen/apis.RegisterRouters(g)`

## 建议下一步

建议下一个 agent 优先完成：

1. 继续补充真实项目画像，盘点所有 route wrapper 和 middleware wrapper。
2. 判断 controller 注释覆盖率是否足够支撑 MVP。
3. 设计第一批 fixture case。
4. 输出 MVP 实现计划。
5. 用户确认后再开始编码。
