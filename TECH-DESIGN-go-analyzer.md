# Go 服务影响范围分析

> go-analyzer 回答一个问题：**改了这些代码，会影响哪些对外入口？**
>
> 两条链路，各自独立、互不依赖：
>
> | 链路     | 适用项目     | 回答的问题                                                    | 命令                            |
> | -------- | ------------ | ------------------------------------------------------------- | ------------------------------- |
> | BFF      | BFF 服务     | 改动影响哪些 HTTP 接口、出站 IM 事件、上游 gRPC 依赖？        | `impact`、`endpoint-assets` |
> | 后端服务 | 后端业务服务 | 改动影响哪些注册出去的服务契约（gRPC / HTTP / Dubbo / Job）？ | `grpc-impact`                 |
>
> 两条链路的终点类型、身份规则、输出契约都不同。它们共用底座（项目加载、声明索引、Diff 映射、影响传播），但那是内部实现，不影响使用方式。

---

## 1. 在 Nexus 里落地

go-analyzer 作为顶层命令组接入 Nexus，与 `nexus bff`、`nexus grpc`、`nexus doc` 并列：

```text
nexus go-analyzer <subcommand> [flags]
```

go-analyzer 原本是独立 Go module，核心代码全在 `internal/` 下，Go 的可见性规则决定 Nexus 无法直接 import，所以把代码搬进 Nexus 的 `internal/goanalyzer/`——自包含，不依赖也不被 `internal/bff`、`internal/transform` 等已有包调用，因为解决的是完全不同的问题（源码 + Diff → JSON 结论，而非 schema → 代码生成）。

三个子命令，覆盖两条链路的四种用法：

| 命令                   | 链路     | 回答                                                              | 输入             |
| ---------------------- | -------- | ----------------------------------------------------------------- | ---------------- |
| `impact --diff`      | BFF      | 这次代码改动影响哪些 HTTP 接口和 IM 事件？                        | Diff             |
| `impact --grpc`      | BFF      | 上游某个 gRPC 方法变了，本 BFF 哪些接口受影响？                   | gRPC 方法身份    |
| `endpoint-assets`    | BFF      | 某个 HTTP 接口依赖哪些上游 gRPC 接口？（`--endpoint` 可重复传） | `METHOD /path` |
| `grpc-impact --diff` | 后端服务 | 这次代码改动影响哪些注册的 gRPC / HTTP / Dubbo / Job 契约？       | Diff             |

`impact --diff` 与 `impact --grpc` 可同时传，合并成一份输出。`impact --grpc` 与 `endpoint-assets` 查的是同一份依赖关系，只是方向相反，构成**双向不变量**——`endpoint-assets(接口 A)` 包含 gRPC B，必然等价于 `impact --grpc B` 的结果包含接口 A，不会因查询方向不同而给出两套矛盾的结论。四个命令的真实调用与完整输出见第 2 节场景。

---

## 2. 单服务场景

> 每个场景用真实项目、真实改动走完全程，展示链路怎么从一行代码推到一个对外入口——一次分析器调用、一个项目。跨服务场景见第 3 节。BFF 和后端服务是两条独立链路，各自的场景分开放。

### BFF 场景

#### 2.1 BFF 改了一个共享 model → 一次改动扇出到 3 个接口

**项目**：`sl-sc1-admin-bff`

**触发**：`model.ProductSet`（组合商品）是个被大量复用的公共类型，开发给它加一个营销标签字段。这个类型本身不属于任何一个 controller，改动前很难一眼看出牵动了多少接口。

```diff
--- a/model/product.go
+++ b/model/product.go
@@ -11,6 +11,7 @@ type ProductSet struct {
 	AvailableStartTime *time.Time                        `json:"availableStartTime"` // 组合商品售卖开始时间
 	AvailableEndTime   *time.Time                        `json:"availableEndTime"`   // 组合商品售卖结束时间
 	Status             string                            `json:"status"`             // 商品状态
+	PromotionTag       string                            `json:"promotionTag"`       // 营销标签
 	ChildrenInfo       map[string]ProductSetChildrenInfo `json:"childrenInfo"`       // 子商品数据映射
 }
```

**命令**：

```bash
nexus go-analyzer impact \
  --project /path/to/sl-sc1-admin-bff \
  --diff /path/to/change.diff \
  --format json --timings
```

**结论**：3 个 HTTP 接口，分别属于交易、直播、关键词三个完全不同的业务模块。

```jsonc
{
  "summary": {
    "impactedEndpointCount": 3,
    "impactedEndpoints": [
      {
        "method": "GET",
        "path": "/admin/api/bff-web/keyword/product/product_set/list",
        "routes": [{"method": "GET", "path": "/admin/api/bff-web/keyword/product/product_set/list"}]
      },
      {
        "method": "GET",
        "path": "/admin/api/bff-web/live/sale/:salesId/product/product_set/list",
        "routes": [{"method": "GET", "path": "/admin/api/bff-web/live/sale/:salesId/product/product_set/list"}]
      },
      {
        "method": "GET",
        "path": "/admin/api/bff-web/trade/product/product_set/list",
        "routes": [{"method": "GET", "path": "/admin/api/bff-web/trade/product/product_set/list"}]
      }
    ],
    "impactedIMCount": 0,
    "impactedIMEvents": []
  }
}
```

**链路**：一个起点，三条独立的传播路径，各自经过不同的中间函数：

```text
type ProductSet (model/product.go)
  ├─ func GetProductSetList (service/product/product.go)
  │   ├─ 直接被 controller/trade/product 用作返回值
  │   │   └─ annotation GET /admin/api/bff-web/trade/product/product_set/list
  │   │
  │   ├─ func GetProductSetSelectableList (service/keyword/keyword.go)
  │   │   └─ func GetProductSetSelectableList (controller/keyword/keyword.go)
  │   │       └─ annotation GET /admin/api/bff-web/keyword/product/product_set/list
  │   │
  │   └─ func GetLiveSaleProductSetList (service/live/sale/sale.go)
  │       └─ func GetLiveProductSetList (controller/live/sale/sale.go)
  │           └─ annotation GET /admin/api/bff-web/live/sale/:salesId/product/product_set/list
```

**值得注意的**：三条链路长度不一样——交易模块直接用了 model，2 跳就到接口；关键词和直播模块各多包了一层 service 转换，4 跳才到接口。分析器不关心链路长短，只要反向引用链存在就会继续往上找，所以起点相同、深度不同的三条链路都被完整还原，不会因为中间多包了一层就漏报。

---

#### 2.2 一次查多个接口的上游依赖，其中一个接口依赖 3 个 gRPC 方法

**项目**：`sl-sc1-admin-bff`

**触发**：接手或 review 一批接口，想批量看它们各自依赖了哪些上游 gRPC——`--endpoint` 可重复传，一次调用拿到多份依赖清单。其中 `ExportCommentReportV2` 这个接口内部按 `ReportType` 分支调用了三个不同的导出方法，是个典型的"一个接口、多个条件依赖"的案例。

```go
// controller/live/statistics/statistics.go
// @Get /admin/api/bff-web/live/sale/:salesId/comments/report/export
func ExportCommentReportV2(ctx context.Context, legoCtx *lego.RequestContext, req ExportReportReqV2) (bool, error) {
	if req.ReportType == "COMMENT" {
		return live.SalesReportServiceClientExportCommentReport(ctx, &exportReq)
	}
	if req.ReportType == "CART" {
		return live.SalesReportServiceClientExportCartReport(ctx, &exportReq)
	}
	return live.SalesReportServiceClientExportSalesReport(ctx, &exportReq)
}
```

**命令**：

```bash
nexus go-analyzer endpoint-assets \
  --project /path/to/sl-sc1-admin-bff \
  --endpoint "GET /admin/api/bff-web/live/sale/:salesId/comments/report/export" \
  --endpoint "GET /admin/api/bff-web/post/sale/:salesId/statistics"
```

**结论**：第一个接口命中 3 个 gRPC 依赖，第二个命中 1 个。

```text
GET /admin/api/bff-web/live/sale/:salesId/comments/report/export
  ├─ SalesReportService/ExportCommentReport
  ├─ SalesReportService/ExportCartReport
  └─ SalesReportService/ExportSalesReport

GET /admin/api/bff-web/post/sale/:salesId/statistics
  └─ SalesStatisticsService/GetStatistics
```

两个接口的完整输出（真实结果，`endpointAssets` 数组按传入顺序各占一项）：

```jsonc
{
  "project": {"module": "sc1-admin-bff"},
  "endpointAssets": [
    {
      "endpoint": {"method": "GET", "path": "/admin/api/bff-web/live/sale/:salesId/comments/report/export"},
      "routes": [{"method": "GET", "path": "/admin/api/bff-web/live/sale/:salesId/comments/report/export"}],
      "controller": [{
        "id": "func:sc1-admin-bff/controller/live/statistics::ExportCommentReportV2",
        "kind": "func", "name": "ExportCommentReportV2",
        "file": "controller/live/statistics/statistics.go"
      }],
      "dependencies": {
        "grpc": [
          {
            "identity": "gopkg.inshopline.com/sc1/app/modules/medium/proto/gen/sales_report.SalesReportService/ExportCartReport",
            "goMethod": "ExportCartReport",
            "chains": [{
              "symbols": [
                {"name": "ExportCommentReportV2"},
                {"name": "SalesReportServiceClientExportCartReport"}
              ],
              "callSite": {"file": "remote/grpc/live/salesReport.go", "line": 29, "column": 12}
            }]
          },
          {
            "identity": "gopkg.inshopline.com/sc1/app/modules/medium/proto/gen/sales_report.SalesReportService/ExportCommentReport",
            "goMethod": "ExportCommentReport",
            "chains": [{
              "symbols": [
                {"name": "ExportCommentReportV2"},
                {"name": "SalesReportServiceClientExportCommentReport"}
              ],
              "callSite": {"file": "remote/grpc/live/salesReport.go", "line": 20, "column": 12}
            }]
          },
          {
            "identity": "gopkg.inshopline.com/sc1/app/modules/medium/proto/gen/sales_report.SalesReportService/ExportSalesReport",
            "goMethod": "ExportSalesReport",
            "chains": [{
              "symbols": [
                {"name": "ExportCommentReportV2"},
                {"name": "SalesReportServiceClientExportSalesReport"}
              ],
              "callSite": {"file": "remote/grpc/live/salesReport.go", "line": 38, "column": 12}
            }]
          }
        ]
      }
    },
    {
      "endpoint": {"method": "GET", "path": "/admin/api/bff-web/post/sale/:salesId/statistics"},
      "routes": [{"method": "GET", "path": "/admin/api/bff-web/post/sale/:salesId/statistics"}],
      "controller": [{
        "id": "func:sc1-admin-bff/controller/post/statistics::GetSalesStatistics",
        "kind": "func", "name": "GetSalesStatistics",
        "file": "controller/post/statistics/sale.go"
      }],
      "dependencies": {
        "grpc": [{
          "identity": "gopkg.inshopline.com/sc1/app/modules/medium/proto/gen/sales_statistics.SalesStatisticsService/GetStatistics",
          "goMethod": "GetStatistics",
          "chains": [{
            "symbols": [
              {"name": "GetSalesStatistics"},
              {"name": "SalesStatisticsServiceClientGetStatistics"}
            ],
            "callSite": {"file": "remote/grpc/live/salesStatistics.go", "line": 48, "column": 19}
          }]
        }]
      }
    }
  ]
}
```

**这批结果能回答两个实际问题：**

- 上游任意一个 `SalesReportService` 方法改了返回结构，`ExportCommentReportV2` 都要回归
- 拿其中任意一条 gRPC 身份反查（见 2.3），能找到还有哪些其他接口也调了它

---

#### 2.3 上游 gRPC 方法变了 → 反查本 BFF 哪些接口受影响

**项目**：`sl-sc1-admin-bff`

**触发**：这是 2.2 的反方向查询——手上只有一个 gRPC 方法身份（比如后端团队通知"这个方法要改返回结构了"），需要反查本 BFF 有哪些接口调了它，而不是从接口出发正向列依赖。拿 2.2 里查到的 `SalesReportService/ExportCommentReport` 反查：

**命令**：

```bash
nexus go-analyzer impact \
  --project /path/to/sl-sc1-admin-bff \
  --grpc "gopkg.inshopline.com/sc1/app/modules/medium/proto/gen/sales_report.SalesReportService/ExportCommentReport"
```

**结论**：2 个 HTTP 接口调了这个 gRPC 方法——一个是当前 Web 端接口，另一个是同样逻辑的旧版本接口，两者都在，一个都不漏。

真实完整输出：

```jsonc
{
  "summary": {
    "impactedEndpointCount": 2,
    "impactedEndpoints": [
      {
        "method": "GET",
        "path": "/admin/api/bff-web/live/sale/:salesId/comments/report/export",
        "routes": [{"method": "GET", "path": "/admin/api/bff-web/live/sale/:salesId/comments/report/export"}]
      },
      {
        "method": "GET",
        "path": "/api/posts/comments/report/export",
        "routes": [{"method": "GET", "path": "/api/posts/comments/report/export"}]
      }
    ],
    "impactedIMCount": 0,
    "impactedIMEvents": []
  },
  "fileSources": [],
  "grpcSources": [{
    "grpc": {
      "identity": "gopkg.inshopline.com/sc1/app/modules/medium/proto/gen/sales_report.SalesReportService/ExportCommentReport",
      "goMethod": "ExportCommentReport"
    },
    "consumers": [
      {
        "endpoint": {"method": "GET", "path": "/admin/api/bff-web/live/sale/:salesId/comments/report/export"},
        "routes": [{"method": "GET", "path": "/admin/api/bff-web/live/sale/:salesId/comments/report/export"}],
        "relation": "may_call",
        "controller": [{
          "id": "func:sc1-admin-bff/controller/live/statistics::ExportCommentReportV2",
          "kind": "func", "name": "ExportCommentReportV2",
          "file": "controller/live/statistics/statistics.go"
        }],
        "chains": [{
          "symbols": [
            {"id": "func:sc1-admin-bff/controller/live/statistics::ExportCommentReportV2", "kind": "func", "name": "ExportCommentReportV2", "file": "controller/live/statistics/statistics.go"},
            {"id": "func:sc1-admin-bff/remote/grpc/live::SalesReportServiceClientExportCommentReport", "kind": "func", "name": "SalesReportServiceClientExportCommentReport", "file": "remote/grpc/live/salesReport.go"}
          ],
          "callSite": {"file": "remote/grpc/live/salesReport.go", "line": 20, "column": 12}
        }]
      },
      {
        "endpoint": {"method": "GET", "path": "/api/posts/comments/report/export"},
        "routes": [{"method": "GET", "path": "/api/posts/comments/report/export"}],
        "relation": "may_call",
        "controller": [{
          "id": "func:sc1-admin-bff/controller/live/statistics::ExportCommentReport",
          "kind": "func", "name": "ExportCommentReport",
          "file": "controller/live/statistics/statistics.go"
        }],
        "chains": [{
          "symbols": [
            {"id": "func:sc1-admin-bff/controller/live/statistics::ExportCommentReport", "kind": "func", "name": "ExportCommentReport", "file": "controller/live/statistics/statistics.go"},
            {"id": "func:sc1-admin-bff/remote/grpc/live::SalesReportServiceClientExportCommentReport", "kind": "func", "name": "SalesReportServiceClientExportCommentReport", "file": "remote/grpc/live/salesReport.go"}
          ],
          "callSite": {"file": "remote/grpc/live/salesReport.go", "line": 20, "column": 12}
        }]
      }
    ],
    "impactedEndpoints": [
      {
        "method": "GET",
        "path": "/admin/api/bff-web/live/sale/:salesId/comments/report/export",
        "routes": [{"method": "GET", "path": "/admin/api/bff-web/live/sale/:salesId/comments/report/export"}]
      },
      {
        "method": "GET",
        "path": "/api/posts/comments/report/export",
        "routes": [{"method": "GET", "path": "/api/posts/comments/report/export"}]
      }
    ]
  }],
  "endpointSourcesSummary": [
    {
      "method": "GET",
      "path": "/admin/api/bff-web/live/sale/:salesId/comments/report/export",
      "sources": [{
        "sourceType": "grpc",
        "grpcIdentity": "gopkg.inshopline.com/sc1/app/modules/medium/proto/gen/sales_report.SalesReportService/ExportCommentReport",
        "rootSymbols": [],
        "chains": [[
          "grpc gopkg.inshopline.com/sc1/app/modules/medium/proto/gen/sales_report.SalesReportService/ExportCommentReport",
          "call_site remote/grpc/live/salesReport.go:20:12",
          "func SalesReportServiceClientExportCommentReport",
          "func ExportCommentReportV2",
          "GET /admin/api/bff-web/live/sale/:salesId/comments/report/export"
        ]]
      }]
    },
    {
      "method": "GET",
      "path": "/api/posts/comments/report/export",
      "sources": [{
        "sourceType": "grpc",
        "grpcIdentity": "gopkg.inshopline.com/sc1/app/modules/medium/proto/gen/sales_report.SalesReportService/ExportCommentReport",
        "rootSymbols": [],
        "chains": [[
          "grpc gopkg.inshopline.com/sc1/app/modules/medium/proto/gen/sales_report.SalesReportService/ExportCommentReport",
          "call_site remote/grpc/live/salesReport.go:20:12",
          "func SalesReportServiceClientExportCommentReport",
          "func ExportCommentReport",
          "GET /api/posts/comments/report/export"
        ]]
      }]
    }
  ]
}
```

**链路**：从 gRPC 身份反向展开，落到两个独立的 controller：

```text
gRPC SalesReportService/ExportCommentReport
  ├─ SalesReportServiceClientExportCommentReport (remote/grpc/live/salesReport.go:20)
  │   └─ controller ExportCommentReportV2 (controller/live/statistics/statistics.go)
  │       └─ 接口 GET /admin/api/bff-web/live/sale/:salesId/comments/report/export
  │
  └─ 同一个 client 方法，被另一个 controller 复用
      └─ controller ExportCommentReport (controller/live/statistics/statistics.go)
          └─ 接口 GET /api/posts/comments/report/export
```

**值得注意的**：这条查询验证了第 1 节说的双向不变量——2.2 正向查出 `ExportCommentReportV2` 依赖这个 gRPC 方法，这里反向查同一个 gRPC 方法，`ExportCommentReportV2` 对应的接口原样出现在结果里，且多了一个正向查询没扫到的旧接口（因为 2.2 只查了新接口）。两个方向查的是同一份 `chains` 关系，不会各自维护一套互相矛盾的数据。这也是后端发布通知链的关键一环：后端团队只需要给出变更的 gRPC 身份，就能拿到所有下游 BFF 接口，不需要知道 BFF 内部结构。

---

#### 2.4 同时改代码、同时传 gRPC 身份，两条触发源互不冲突

**项目**：`sl-sc1-admin-bff`

**触发**：`impact` 的 `--diff` 和 `--grpc` 是两个独立的触发源，可以同时给（1.2 节已经点过这一点）。这里用真实数据验证一次：同一次调用里，`--diff` 传 2.1 那次 `ProductSet` 改动（预期命中 3 个接口），`--grpc` 传 2.3 那条 `SalesReportService/ExportCommentReport`（预期命中 2 个接口）——两个触发源命中的是完全不相交的两组接口，验证不会互相干扰、也不会重复计数。

```bash
nexus go-analyzer impact \
  --project /path/to/sl-sc1-admin-bff \
  --diff /path/to/product-set-change.diff \
  --grpc "gopkg.inshopline.com/sc1/app/modules/medium/proto/gen/sales_report.SalesReportService/ExportCommentReport" \
  --format json --timings
```

**结论**：5 个接口，正好是两个触发源各自命中数的并集（3 + 2 = 5），没有多也没有少：

```jsonc
{
  "summary": {
    "impactedEndpointCount": 5,
    "impactedEndpoints": [
      {"method": "GET", "path": "/admin/api/bff-web/keyword/product/product_set/list", "routes": [{"method": "GET", "path": "/admin/api/bff-web/keyword/product/product_set/list"}]},
      {"method": "GET", "path": "/admin/api/bff-web/live/sale/:salesId/comments/report/export", "routes": [{"method": "GET", "path": "/admin/api/bff-web/live/sale/:salesId/comments/report/export"}]},
      {"method": "GET", "path": "/admin/api/bff-web/live/sale/:salesId/product/product_set/list", "routes": [{"method": "GET", "path": "/admin/api/bff-web/live/sale/:salesId/product/product_set/list"}]},
      {"method": "GET", "path": "/admin/api/bff-web/trade/product/product_set/list", "routes": [{"method": "GET", "path": "/admin/api/bff-web/trade/product/product_set/list"}]},
      {"method": "GET", "path": "/api/posts/comments/report/export", "routes": [{"method": "GET", "path": "/api/posts/comments/report/export"}]}
    ],
    "impactedIMCount": 0,
    "impactedIMEvents": []
  },
  "fileSources": [
    {"sourceFile": "model/product.go", "impactedEndpoints": [
      {"method": "GET", "path": "/admin/api/bff-web/keyword/product/product_set/list"},
      {"method": "GET", "path": "/admin/api/bff-web/live/sale/:salesId/product/product_set/list"},
      {"method": "GET", "path": "/admin/api/bff-web/trade/product/product_set/list"}
    ]}
  ],
  "grpcSources": [
    {
      "grpc": {"identity": "gopkg.inshopline.com/sc1/app/modules/medium/proto/gen/sales_report.SalesReportService/ExportCommentReport", "goMethod": "ExportCommentReport"},
      "consumers": [
        {"endpoint": {"method": "GET", "path": "/admin/api/bff-web/live/sale/:salesId/comments/report/export"}},
        {"endpoint": {"method": "GET", "path": "/api/posts/comments/report/export"}}
      ]
    }
  ],
  "endpointSourcesSummary": [
    {"method": "GET", "path": "/admin/api/bff-web/keyword/product/product_set/list", "sources": [{"sourceType": "file"}]},
    {"method": "GET", "path": "/admin/api/bff-web/live/sale/:salesId/comments/report/export", "sources": [{"sourceType": "grpc"}]},
    {"method": "GET", "path": "/admin/api/bff-web/live/sale/:salesId/product/product_set/list", "sources": [{"sourceType": "file"}]},
    {"method": "GET", "path": "/admin/api/bff-web/trade/product/product_set/list", "sources": [{"sourceType": "file"}]},
    {"method": "GET", "path": "/api/posts/comments/report/export", "sources": [{"sourceType": "grpc"}]}
  ]
}
```

**值得注意的**：

- **两个触发源各自的证据分别落在 `fileSources` 和 `grpcSources`，不会混在一起。** `fileSources` 只有 1 项，记录 `model/product.go` 这一个变更文件推出的 3 个接口；`grpcSources` 只有 1 项，记录这条 gRPC identity 反查出的 2 个接口。`endpointSourcesSummary` 里每个接口的 `sourceType` 精确标出它是从 `file` 还是从 `grpc` 来的，排障时一眼就能看出这次为什么被算进来。
- **没有重复计数，也没有相互抑制。** 这个例子里两个触发源命中的接口完全不相交，`summary.impactedEndpointCount` 正好是 3+2=5；如果两个触发源碰巧命中了同一个接口，`summary` 会全局去重只算一次，但 `endpointSourcesSummary` 里那个接口的 `sources` 数组会同时列出 `file` 和 `grpc` 两条证据——去重只发生在最终汇总层，不会丢证据。
- 这验证的正是日常场景：一次 MR 既改了业务代码又恰好赶上上游 gRPC 变更通知，两条回归依据不需要跑两次分析、也不用担心谁覆盖谁。

---

### 后端服务场景

#### 2.5 后端改了消息 DTO → 回归哪些入口契约

**项目**：`sc1-server`

**触发**：开发给消息列表的 DTO 加了一个附件名称字段，只碰了一个 struct。

```diff
--- a/modules/inbox/internal/model/inbox/ec_get_message_dto.go
+++ b/modules/inbox/internal/model/inbox/ec_get_message_dto.go
@@ -68,6 +68,7 @@ type GetMessageItem struct {
 	AttachmentUrl  string             `json:"attachment_url,omitempty"`
+	AttachmentName string             `json:"attachment_name,omitempty"`
 	ConversationId string             `json:"conversation_id"`
```

**命令**：

```bash
nexus go-analyzer grpc-impact \
  --project /path/to/sc1-server \
  --diff /path/to/change.diff \
  --format json --timings
```

**结论**：6 个 gRPC 入口 + 3 个 HTTP 入口，dubbo 和 job 未命中。真实完整 `summary`：

```jsonc
{
  "grpc": [
    {
      "id": "grpc:gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/DeleteMessage",
      "kind": "grpc_operation",
      "identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/DeleteMessage",
      "identityResolution": "static",
      "registration": {"file": "modules/inbox/internal/grpc/wire_set.go", "line": 35, "column": 2}
    },
    {
      "id": "grpc:gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/GetMessageList",
      "kind": "grpc_operation",
      "identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/GetMessageList",
      "identityResolution": "static",
      "registration": {"file": "modules/inbox/internal/grpc/wire_set.go", "line": 35, "column": 2}
    },
    {
      "id": "grpc:gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/PageSelectedMsg",
      "kind": "grpc_operation",
      "identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/PageSelectedMsg",
      "identityResolution": "static",
      "registration": {"file": "modules/inbox/internal/grpc/wire_set.go", "line": 35, "column": 2}
    },
    {
      "id": "grpc:gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/SendMessageByConversation",
      "kind": "grpc_operation",
      "identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/SendMessageByConversation",
      "identityResolution": "static",
      "registration": {"file": "modules/inbox/internal/grpc/wire_set.go", "line": 35, "column": 2}
    },
    {
      "id": "grpc:gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_client.MessageClientService/SendMessage",
      "kind": "grpc_operation",
      "identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_client.MessageClientService/SendMessage",
      "identityResolution": "static",
      "registration": {"file": "modules/inbox/internal/grpc/wire_set.go", "line": 34, "column": 2}
    },
    {
      "id": "grpc:gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_client.MessageClientService/TriggerSendWelcomeMessage",
      "kind": "grpc_operation",
      "identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_client.MessageClientService/TriggerSendWelcomeMessage",
      "identityResolution": "static",
      "registration": {"file": "modules/inbox/internal/grpc/wire_set.go", "line": 34, "column": 2}
    }
  ],
  "dubbo": [],
  "http": [
    {
      "id": "http:route:method:sc1-server/modules/inbox/internal/web:Router:InitInternal:GET:/customer/messages:4",
      "kind": "http_endpoint",
      "identity": "GET /sc1-internal/officialmsg/v1/customer/messages",
      "identityResolution": "static",
      "method": "GET", "path": "/sc1-internal/officialmsg/v1/customer/messages", "localPath": "/customer/messages",
      "registration": {"file": "modules/inbox/internal/web/router.go", "line": 29, "column": 2}
    },
    {
      "id": "http:route:method:sc1-server/modules/inbox/internal/web:Router:InitInternal:GET:/messages:5",
      "kind": "http_endpoint",
      "identity": "GET /sc1-internal/officialmsg/v1/messages",
      "identityResolution": "static",
      "method": "GET", "path": "/sc1-internal/officialmsg/v1/messages", "localPath": "/messages",
      "registration": {"file": "modules/inbox/internal/web/router.go", "line": 30, "column": 2}
    },
    {
      "id": "http:route:method:sc1-server/modules/inbox/internal/web:Router:InitInternal:POST:/customer/messages:6",
      "kind": "http_endpoint",
      "identity": "POST /sc1-internal/officialmsg/v1/customer/messages",
      "identityResolution": "static",
      "method": "POST", "path": "/sc1-internal/officialmsg/v1/customer/messages", "localPath": "/customer/messages",
      "registration": {"file": "modules/inbox/internal/web/router.go", "line": 33, "column": 2}
    }
  ],
  "job": []
}
```

`grpc[].identity` 的格式是 `<生成包 import 路径>.<Service>/<GoMethod>`，与 BFF 链路 `impact --grpc` 的**输入格式完全一致——不需要任何转换**，可以把这里任意一条 `identity` 原样传给下游 BFF 的 `impact --grpc`（2.4 已经验证过 `--diff` 和 `--grpc` 触发源互不冲突）。这 6 条 identity 具体流向了哪些 BFF、落到了哪些 HTTP 接口，第 3 节接着这批真实数据继续跑。

**链路**（以 `DeleteMessage` 为例）：

```text
type GetMessageItem (modules/inbox/internal/model/inbox/)
  └─ type InboxImEvent          ← DTO 被消息事件引用
      └─ method SendConversationImWithType
          └─ method DeleteMessage  (biz 层)
              └─ method DeleteMessage  (provider 层，实现 gRPC 接口)
                  └─ grpc_operation BizInboxService/DeleteMessage  ← 注册契约
```

链路终点是 `RegisterXxxServer` 注册出去的契约，不是 provider 方法本身——只有该实现确实被注册过，才会形成正式结论。同一个 DTO 同时被 gRPC provider 和内部 HTTP 路由用到，所以一次改动跨了两类入口。

---

## 3. 跨服务场景

> 单服务场景一次分析器调用只覆盖一个项目。真实的通知链往往要跨项目串联——一个后端改动，可能同时影响好几个下游 BFF。这类场景不是一次调用能解决的，而是把多次单服务调用的结果按稳定身份（gRPC identity、HTTP endpoint）串起来，串联逻辑在编排层，不在分析器内部。

### 3.1 同一次后端改动，继续追到两个下游 BFF 的 HTTP 接口

**项目**：`sc1-server`（后端） + `sl-sc1-admin-bff`、`sl-sc1-bff-service`（两个下游 BFF）

**背景**：2.5 那次 DTO 改动影响了 6 个 gRPC 入口——`BizInboxService` 下 4 个方法、`MessageClientService` 下 2 个方法。`sc1-server` 只知道自己注册了这些契约，不知道谁在调；实际调用方分散在两个不同的 BFF 项目里，而且事先并不知道每条 identity 具体被哪个 BFF 用了、用了几个。

**做法**：`--grpc` 是可重复 flag，2.5 那 6 条 identity **全部一次性**传给每个 BFF 各一次 `impact` 调用——不逐条试探、不预判哪条会命中，交给分析器在一次分析里把 6 条各自的命中结果都吐出来：

```bash
# 6 条 identity 全量传入，每个 BFF 各跑一次
nexus go-analyzer impact --project /path/to/sl-sc1-admin-bff \
  --grpc "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/DeleteMessage" \
  --grpc "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/GetMessageList" \
  --grpc "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/PageSelectedMsg" \
  --grpc "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/SendMessageByConversation" \
  --grpc "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_client.MessageClientService/SendMessage" \
  --grpc "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_client.MessageClientService/TriggerSendWelcomeMessage"

nexus go-analyzer impact --project /path/to/sl-sc1-bff-service \
  --grpc "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/DeleteMessage" \
  --grpc "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/GetMessageList" \
  --grpc "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/PageSelectedMsg" \
  --grpc "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/SendMessageByConversation" \
  --grpc "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_client.MessageClientService/SendMessage" \
  --grpc "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_client.MessageClientService/TriggerSendWelcomeMessage"
```

之所以能全量传入：`grpcSources` 是按 identity 分组的数组，没命中的 identity 照样在数组里占一条，只是 `consumers`/`impactedEndpoints` 是空数组——不会因为某条没命中就报错或漏掉，`summary.impactedEndpoints` 只汇总真正命中的那些。传 6 条还是传 1 条，命中的结果完全一样，不需要提前知道"这个 BFF 是否用了这条 gRPC"。

**结论**：命中关系是干净的两分——`BizInboxService.*` 全部落在 `admin-bff`，`MessageClientService.*` 全部落在 `bff-service`，没有交叉：

| gRPC 方法                                          | sl-sc1-admin-bff                         | sl-sc1-bff-service                       |
| -------------------------------------------------- | ---------------------------------------- | ---------------------------------------- |
| `BizInboxService/DeleteMessage`                  | 2 个接口                                 | 0（数组里仍有这一条，`consumers: []`） |
| `BizInboxService/GetMessageList`                 | 1 个接口                                 | 0                                        |
| `BizInboxService/PageSelectedMsg`                | 1 个接口                                 | 0                                        |
| `BizInboxService/SendMessageByConversation`      | 1 个接口                                 | 0                                        |
| `MessageClientService/SendMessage`               | 0（数组里仍有这一条，`consumers: []`） | 1 个接口                                 |
| `MessageClientService/TriggerSendWelcomeMessage` | 0                                        | 1 个接口                                 |

`sl-sc1-admin-bff` 的真实完整输出（一次调用，6 条 identity 全部带进去，命中 5 个接口）：

```jsonc
{
  "summary": {
    "impactedEndpointCount": 5,
    "impactedEndpoints": [
      {
        "method": "DELETE",
        "path": "/admin/api/bff-web/mc/message/inbox/:id",
        "routes": [
          {"method": "DELETE", "path": "/admin/api/bff-app/mc/message/inbox/:id"},
          {"method": "DELETE", "path": "/admin/api/bff-web/mc/message/inbox/:id"}
        ]
      },
      {
        "method": "DELETE",
        "path": "/inbox/v1/admin/admin/messages/:id",
        "routes": [{"method": "DELETE", "path": "/inbox/v1/admin/message/:id"}]
      },
      {
        "method": "GET",
        "path": "/officialmsg/v1/admin/messages",
        "routes": [{"method": "GET", "path": "/officialmsg/v1/admin/messages"}]
      },
      {
        "method": "POST",
        "path": "/inbox/v1/admin/admin/message/select/list",
        "routes": [{"method": "POST", "path": "/inbox/v1/admin/message/select/list"}]
      },
      {
        "method": "POST",
        "path": "/messages",
        "routes": [{"method": "POST", "path": "/officialmsg/v1/admin/messages"}]
      }
    ],
    "impactedIMCount": 0,
    "impactedIMEvents": []
  },
  "fileSources": [],
  "grpcSources": [
    {
      "grpc": {"identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/DeleteMessage", "goMethod": "DeleteMessage"},
      "consumers": [
        {
          "endpoint": {"method": "DELETE", "path": "/admin/api/bff-web/mc/message/inbox/:id"},
          "routes": [
            {"method": "DELETE", "path": "/admin/api/bff-app/mc/message/inbox/:id"},
            {"method": "DELETE", "path": "/admin/api/bff-web/mc/message/inbox/:id"}
          ],
          "relation": "may_call",
          "controller": [{
            "id": "func:sc1-admin-bff/controller/message::DeleteInboxMessage",
            "kind": "func", "name": "DeleteInboxMessage", "file": "controller/message/message.go"
          }],
          "chains": [{
            "symbols": [
              {"id": "func:sc1-admin-bff/controller/message::DeleteInboxMessage", "kind": "func", "name": "DeleteInboxMessage", "file": "controller/message/message.go"},
              {"id": "func:sc1-admin-bff/service/message::DeleteInboxMessage", "kind": "func", "name": "DeleteInboxMessage", "file": "service/message/message.go"}
            ],
            "callSite": {"file": "service/message/message.go", "line": 17, "column": 11}
          }]
        },
        {
          "endpoint": {"method": "DELETE", "path": "/inbox/v1/admin/admin/messages/:id"},
          "routes": [{"method": "DELETE", "path": "/inbox/v1/admin/message/:id"}],
          "relation": "may_call",
          "controller": [{
            "id": "func:sc1-admin-bff/controller/mc/inbox::DeleteMessage",
            "kind": "func", "name": "DeleteMessage", "file": "controller/mc/inbox/inbox.go"
          }],
          "chains": [{
            "symbols": [{"id": "func:sc1-admin-bff/controller/mc/inbox::DeleteMessage", "kind": "func", "name": "DeleteMessage", "file": "controller/mc/inbox/inbox.go"}],
            "callSite": {"file": "controller/mc/inbox/inbox.go", "line": 317, "column": 12}
          }]
        }
      ],
      "impactedEndpoints": [
        {
          "method": "DELETE", "path": "/admin/api/bff-web/mc/message/inbox/:id",
          "routes": [
            {"method": "DELETE", "path": "/admin/api/bff-app/mc/message/inbox/:id"},
            {"method": "DELETE", "path": "/admin/api/bff-web/mc/message/inbox/:id"}
          ]
        },
        {"method": "DELETE", "path": "/inbox/v1/admin/admin/messages/:id", "routes": [{"method": "DELETE", "path": "/inbox/v1/admin/message/:id"}]}
      ]
    },
    {
      "grpc": {"identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/GetMessageList", "goMethod": "GetMessageList"},
      "consumers": [{
        "endpoint": {"method": "GET", "path": "/officialmsg/v1/admin/messages"},
        "routes": [{"method": "GET", "path": "/officialmsg/v1/admin/messages"}],
        "relation": "may_call",
        "controller": [{
          "id": "func:sc1-admin-bff/controller/mc/inbox::GetMessageListAdmin",
          "kind": "func", "name": "GetMessageListAdmin", "file": "controller/mc/inbox/inbox.go"
        }],
        "chains": [{
          "symbols": [{"id": "func:sc1-admin-bff/controller/mc/inbox::GetMessageListAdmin", "kind": "func", "name": "GetMessageListAdmin", "file": "controller/mc/inbox/inbox.go"}],
          "callSite": {"file": "controller/mc/inbox/inbox.go", "line": 128, "column": 17}
        }]
      }],
      "impactedEndpoints": [{"method": "GET", "path": "/officialmsg/v1/admin/messages", "routes": [{"method": "GET", "path": "/officialmsg/v1/admin/messages"}]}]
    },
    {
      "grpc": {"identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/PageSelectedMsg", "goMethod": "PageSelectedMsg"},
      "consumers": [{
        "endpoint": {"method": "POST", "path": "/inbox/v1/admin/admin/message/select/list"},
        "routes": [{"method": "POST", "path": "/inbox/v1/admin/message/select/list"}],
        "relation": "may_call",
        "controller": [{
          "id": "func:sc1-admin-bff/controller/mc/inbox::PageSelectMsg",
          "kind": "func", "name": "PageSelectMsg", "file": "controller/mc/inbox/inbox.go"
        }],
        "chains": [{
          "symbols": [{"id": "func:sc1-admin-bff/controller/mc/inbox::PageSelectMsg", "kind": "func", "name": "PageSelectMsg", "file": "controller/mc/inbox/inbox.go"}],
          "callSite": {"file": "controller/mc/inbox/inbox.go", "line": 303, "column": 15}
        }]
      }],
      "impactedEndpoints": [{"method": "POST", "path": "/inbox/v1/admin/admin/message/select/list", "routes": [{"method": "POST", "path": "/inbox/v1/admin/message/select/list"}]}]
    },
    {
      "grpc": {"identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/SendMessageByConversation", "goMethod": "SendMessageByConversation"},
      "consumers": [{
        "endpoint": {"method": "POST", "path": "/messages"},
        "routes": [{"method": "POST", "path": "/officialmsg/v1/admin/messages"}],
        "relation": "may_call",
        "controller": [{
          "id": "func:sc1-admin-bff/controller/mc/inbox::SendAdminMessage",
          "kind": "func", "name": "SendAdminMessage", "file": "controller/mc/inbox/inbox.go"
        }],
        "chains": [{
          "symbols": [{"id": "func:sc1-admin-bff/controller/mc/inbox::SendAdminMessage", "kind": "func", "name": "SendAdminMessage", "file": "controller/mc/inbox/inbox.go"}],
          "callSite": {"file": "controller/mc/inbox/inbox.go", "line": 156, "column": 17}
        }]
      }],
      "impactedEndpoints": [{"method": "POST", "path": "/messages", "routes": [{"method": "POST", "path": "/officialmsg/v1/admin/messages"}]}]
    },
    {
      "grpc": {"identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_client.MessageClientService/SendMessage", "goMethod": "SendMessage"},
      "consumers": [],
      "impactedEndpoints": []
    },
    {
      "grpc": {"identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_client.MessageClientService/TriggerSendWelcomeMessage", "goMethod": "TriggerSendWelcomeMessage"},
      "consumers": [],
      "impactedEndpoints": []
    }
  ],
  "endpointSourcesSummary": [
    {
      "method": "DELETE", "path": "/admin/api/bff-web/mc/message/inbox/:id",
      "sources": [{
        "sourceType": "grpc",
        "grpcIdentity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/DeleteMessage",
        "rootSymbols": [],
        "chains": [["grpc gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/DeleteMessage", "call_site service/message/message.go:17:11", "func DeleteInboxMessage", "func DeleteInboxMessage", "DELETE /admin/api/bff-web/mc/message/inbox/:id"]]
      }]
    },
    {
      "method": "DELETE", "path": "/inbox/v1/admin/admin/messages/:id",
      "sources": [{
        "sourceType": "grpc",
        "grpcIdentity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/DeleteMessage",
        "rootSymbols": [],
        "chains": [["grpc gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/DeleteMessage", "call_site controller/mc/inbox/inbox.go:317:12", "func DeleteMessage", "DELETE /inbox/v1/admin/admin/messages/:id"]]
      }]
    },
    {
      "method": "GET", "path": "/officialmsg/v1/admin/messages",
      "sources": [{
        "sourceType": "grpc",
        "grpcIdentity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/GetMessageList",
        "rootSymbols": [],
        "chains": [["grpc gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/GetMessageList", "call_site controller/mc/inbox/inbox.go:128:17", "func GetMessageListAdmin", "GET /officialmsg/v1/admin/messages"]]
      }]
    },
    {
      "method": "POST", "path": "/inbox/v1/admin/admin/message/select/list",
      "sources": [{
        "sourceType": "grpc",
        "grpcIdentity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/PageSelectedMsg",
        "rootSymbols": [],
        "chains": [["grpc gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/PageSelectedMsg", "call_site controller/mc/inbox/inbox.go:303:15", "func PageSelectMsg", "POST /inbox/v1/admin/admin/message/select/list"]]
      }]
    },
    {
      "method": "POST", "path": "/messages",
      "sources": [{
        "sourceType": "grpc",
        "grpcIdentity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/SendMessageByConversation",
        "rootSymbols": [],
        "chains": [["grpc gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/SendMessageByConversation", "call_site controller/mc/inbox/inbox.go:156:17", "func SendAdminMessage", "POST /messages"]]
      }]
    }
  ]
}
```

`sl-sc1-bff-service` 的真实完整输出（同一批 6 条 identity 全量传入，命中 2 个接口，另外 4 条 `BizInboxService.*` 在这个项目里全部是空 `consumers`）：

```jsonc
{
  "summary": {
    "impactedEndpointCount": 2,
    "impactedEndpoints": [
      {
        "method": "POST",
        "path": "/sc1-internal/app-proxy/api/mc/customer/message/send",
        "routes": [{"method": "POST", "path": "/sc1-internal/app-proxy/api/mc/customer/message/send"}]
      },
      {
        "method": "POST",
        "path": "/sc1-internal/app-proxy/api/mc/customer/message/send/welcome",
        "routes": [{"method": "POST", "path": "/sc1-internal/app-proxy/api/mc/customer/message/send/welcome"}]
      }
    ],
    "impactedIMCount": 0,
    "impactedIMEvents": []
  },
  "fileSources": [],
  "grpcSources": [
    {"grpc": {"identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/DeleteMessage", "goMethod": "DeleteMessage"}, "consumers": [], "impactedEndpoints": []},
    {"grpc": {"identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/GetMessageList", "goMethod": "GetMessageList"}, "consumers": [], "impactedEndpoints": []},
    {"grpc": {"identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/PageSelectedMsg", "goMethod": "PageSelectedMsg"}, "consumers": [], "impactedEndpoints": []},
    {"grpc": {"identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_biz.BizInboxService/SendMessageByConversation", "goMethod": "SendMessageByConversation"}, "consumers": [], "impactedEndpoints": []},
    {
      "grpc": {"identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_client.MessageClientService/SendMessage", "goMethod": "SendMessage"},
      "consumers": [{
        "endpoint": {"method": "POST", "path": "/sc1-internal/app-proxy/api/mc/customer/message/send"},
        "routes": [{"method": "POST", "path": "/sc1-internal/app-proxy/api/mc/customer/message/send"}],
        "relation": "may_call",
        "controller": [{
          "id": "func:sc1-client-bff-service/controller/mc/app_proxy::SendMessage",
          "kind": "func", "name": "SendMessage", "file": "controller/mc/app_proxy/app_proxy.go"
        }],
        "chains": [{
          "symbols": [{"id": "func:sc1-client-bff-service/controller/mc/app_proxy::SendMessage", "kind": "func", "name": "SendMessage", "file": "controller/mc/app_proxy/app_proxy.go"}],
          "callSite": {"file": "controller/mc/app_proxy/app_proxy.go", "line": 293, "column": 15}
        }]
      }],
      "impactedEndpoints": [{"method": "POST", "path": "/sc1-internal/app-proxy/api/mc/customer/message/send", "routes": [{"method": "POST", "path": "/sc1-internal/app-proxy/api/mc/customer/message/send"}]}]
    },
    {
      "grpc": {"identity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_client.MessageClientService/TriggerSendWelcomeMessage", "goMethod": "TriggerSendWelcomeMessage"},
      "consumers": [{
        "endpoint": {"method": "POST", "path": "/sc1-internal/app-proxy/api/mc/customer/message/send/welcome"},
        "routes": [{"method": "POST", "path": "/sc1-internal/app-proxy/api/mc/customer/message/send/welcome"}],
        "relation": "may_call",
        "controller": [{
          "id": "func:sc1-client-bff-service/controller/mc/app_proxy::SendWelcomeMessage",
          "kind": "func", "name": "SendWelcomeMessage", "file": "controller/mc/app_proxy/app_proxy.go"
        }],
        "chains": [{
          "symbols": [{"id": "func:sc1-client-bff-service/controller/mc/app_proxy::SendWelcomeMessage", "kind": "func", "name": "SendWelcomeMessage", "file": "controller/mc/app_proxy/app_proxy.go"}],
          "callSite": {"file": "controller/mc/app_proxy/app_proxy.go", "line": 334, "column": 15}
        }]
      }],
      "impactedEndpoints": [{"method": "POST", "path": "/sc1-internal/app-proxy/api/mc/customer/message/send/welcome", "routes": [{"method": "POST", "path": "/sc1-internal/app-proxy/api/mc/customer/message/send/welcome"}]}]
    }
  ],
  "endpointSourcesSummary": [
    {
      "method": "POST", "path": "/sc1-internal/app-proxy/api/mc/customer/message/send",
      "sources": [{
        "sourceType": "grpc",
        "grpcIdentity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_client.MessageClientService/SendMessage",
        "rootSymbols": [],
        "chains": [["grpc gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_client.MessageClientService/SendMessage", "call_site controller/mc/app_proxy/app_proxy.go:293:15", "func SendMessage", "POST /sc1-internal/app-proxy/api/mc/customer/message/send"]]
      }]
    },
    {
      "method": "POST", "path": "/sc1-internal/app-proxy/api/mc/customer/message/send/welcome",
      "sources": [{
        "sourceType": "grpc",
        "grpcIdentity": "gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_client.MessageClientService/TriggerSendWelcomeMessage",
        "rootSymbols": [],
        "chains": [["grpc gopkg.inshopline.com/sc1/app/modules/inbox/proto/inbox_client.MessageClientService/TriggerSendWelcomeMessage", "call_site controller/mc/app_proxy/app_proxy.go:334:15", "func SendWelcomeMessage", "POST /sc1-internal/app-proxy/api/mc/customer/message/send/welcome"]]
      }]
    }
  ]
}
```

**串起来看完整通知链**：

```text
sc1-server: DTO GetMessageItem 加字段
  └─ grpc-impact 找到 6 个 gRPC 入口（2.5）
      └─ 6 条 identity 全量传入，每个下游 BFF 各跑一次 impact --grpc
          ├─ sl-sc1-admin-bff（一次调用，命中 5 个接口）
          │   ├─ DeleteMessage            → DELETE /admin/api/bff-web/mc/message/inbox/:id
          │   │                             DELETE /inbox/v1/admin/admin/messages/:id
          │   ├─ GetMessageList           → GET  /officialmsg/v1/admin/messages
          │   ├─ PageSelectedMsg          → POST /inbox/v1/admin/admin/message/select/list
          │   ├─ SendMessageByConversation → POST /messages
          │   └─ MessageClientService.*   → 未命中（consumers 为空）
          └─ sl-sc1-bff-service（一次调用，命中 2 个接口）
              ├─ BizInboxService.*         → 未命中（consumers 为空）
              ├─ SendMessage              → POST /sc1-internal/app-proxy/api/mc/customer/message/send
              └─ TriggerSendWelcomeMessage → POST /sc1-internal/app-proxy/api/mc/customer/message/send/welcome
```

**值得注意的**：

- **全量传入是关键，不是逐条试探。** 编排层不需要提前判断"这条 identity 是不是这个 BFF 用的"——判断本身就是分析器要做的事。把 2.5 产出的全部 6 条 identity 一次性带进每个 BFF 的 `impact --grpc`，一次调用就拿到这个 BFF 对全部 6 条的命中结果，没用到的那些只是在 `grpcSources` 里占一条空 `consumers`，不会报错、不会污染 `summary`。
- **两个 BFF 各自只认自己调过的那部分，互不干扰。** `BizInboxService.*` 4 个方法全部落在 `admin-bff`，`MessageClientService.*` 2 个方法全部落在 `bff-service`，没有一条同时命中两个项目。这不是分析器刻意去重——两个 BFF 项目本来就是分别独立分析的，命中关系纯粹取决于各自项目里到底调了谁。
- **同一个 gRPC 方法在同一个 BFF 里可能命中多个接口**（`DeleteMessage` 命中 2 个），这是 2.3 节讲过的"一个方法被多个 controller 复用"的情形，跨服务查询同样适用。
- **编排层要做的只是"把全量 identity 转发给每个已知的下游 BFF 项目，各跑一次"**，不需要理解 BFF 内部结构，也不需要维护一张"哪个 gRPC 方法归哪个 BFF"的映射表——每次调用自己会给出"这批 identity 里谁命中了"的完整答案，命中即通知，没命中就跳过。这正是第 1 节说的"一段 identity 字符串就能在项目间传递查询"在多下游场景下的样子。

---

### 3.2 继续追到前端：HTTP 接口 → 前端调用点

**项目**：`message-center`（前端）

**背景**：3.1 跑出来的 5 个 `sl-sc1-admin-bff` HTTP 接口，是后端改动链路目前的终点，也是前端影响面分析的**起点**——分析器不跨仓，接下来这一段由前端的 `mr-app-impact`（analyzer-ts）接手，用 go-analyzer 产出的稳定 `METHOD /path` 反查前端有哪些调用点、进而有哪些页面要回归。取其中一个接口 `DELETE /admin/api/bff-web/mc/message/inbox/:id` 落到 `message-center` 项目里的真实情况：

```ts
// src/feature/messageCenter/hooks/useDeleteChatroomMsg.ts
const handleDeleteMessage = async (curMessage: IMessage) => {
  if (platform) {
    if (conversationType === 'inbox') {
      await bffClient.fetch('DELETE /admin/api/bff-web/mc/message/inbox/:id', {
        platform: PlatformKey[platform],
        conversationId: currentConversationId,
        id: String(curMessage.id),
      });
    }
    // ...
  }
};
```

**两种接入方式，取决于项目改造进度**：

| 模式         | 前提                                                                                                  | 输入                                                                                                                                 | 稳不稳                                                                   |
| ------------ | ----------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------ |
| `--api`    | 项目已全量收敛到统一请求实例（如上面的`bffClient.fetch(...)`），接口身份是字符串字面量              | `--api "DELETE /admin/api/bff-web/mc/message/inbox/:id"`                                                                           | 最稳——直接用 go-analyzer 吐出来的`METHOD /path` 反查，不需要人工介入 |
| `--source` | 项目还没完成统一请求实例改造，接口调用散落在裸`fetch`/`axios`/各类 service 封装里，字符串反查不到 | 先让 AI 读代码，AI 确认这个接口实际对应哪个文件里的哪个 symbol，再传`--source "/abs/useDeleteChatroomMsg.ts::handleDeleteMessage"` | 过渡方案——多一步"AI 梳理 symbol"，才能拿到跟`--api` 同等的分析入口   |

`message-center` 这个例子里 `bffClient.fetch('DELETE /admin/api/bff-web/mc/message/inbox/:id', ...)` 已经是字符串字面量精确匹配 HTTP 接口身份，属于走 `--api` 模式的理想情况；改造不完整的老项目通常要退到 `--source` 模式。两种模式最终都产出同一种结构的 `app.impact.json`，下游报告生成不区分输入模式。

---

### 3.3 全链路怎么串：编排 skill

3.1、3.2 分别验证了两段串联——`grpc-impact`（后端）→ `impact --grpc --diff`（BFF）→ `mr-app-impact`（前端）——都能靠稳定身份（gRPC identity、HTTP `METHOD /path`）无损传递,不需要额外的映射表。三段目前是分开手动跑的。

后续会用一个 skill，把这三段接起来，一次输入（一段后端 Diff）产出一份端到端影响范围报告：

```text
sc1-server 改动
  → grpc-impact                        找到受影响的 gRPC / HTTP / Dubbo / Job 入口契约
      → impact --grpc（逐个已知下游 BFF）  全量传入契约 identity，找到受影响的 HTTP 接口
          → mr-app-impact（逐个已知下游前端项目） 用 HTTP 接口反查前端调用点，产出页面级回归报告
```

skill 要解决的是编排层的问题：知道每个后端服务对应哪些下游 BFF、每个 BFF 对应哪些下游前端项目（这份映射关系本身不由分析器维护，是团队级的静态配置），依次调用三层命令，把每一层的输出接到下一层的输入，最终汇总成一份测试人员能直接读的回归清单。分析器本身的职责边界不变——每一层仍然只回答自己那条链路的问题，跨仓串联永远是编排层的事。

---
