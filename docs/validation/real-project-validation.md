# Real Project Validation

This document tracks smoke validation for the first three target BFF projects:

- `sl-sc1-bff-service`
- `sl-sc1-admin-bff`
- `sl-sc2-admin-bff`

Run:

```bash
bash scripts/smoke-real-projects.sh
```

The CLI accepts absolute paths at the command boundary. The smoke script
resolves the sibling demo projects to absolute paths, writes JSON outputs to
`.analyzer-smoke/`, and validates that each output is parseable JSON. Real
impact cases temporarily edit business files, collect `git diff` from the BFF
project, keep the files in their post-change state while running impact twice,
compare the JSON byte-for-byte, validate exact endpoint sets, and then restore
the files. IM cases additionally validate exact event sets and zero unexpected
HTTP endpoints. Lego BFF syntax and IM transports are recognized by built-in
rules; no analyzer config is required for them.

Facts counts and diagnostic code distributions are compared against
`testdata/baselines/real-project-facts.json`; intentional analyzer behavior
changes must update that baseline in the same review.

The gRPC dependency validation additionally checks that `facts` exposes
generated operations and project call sites, then verifies selected relations
through both `endpoint-assets` and `grpc-consumers`. The two directions must
contain the same endpoint/gRPC pair for one project snapshot and build context.

## Current Expectations

The MVP validation target is stability rather than perfect precision:

- Both projects should run without panic.
- Facts JSON should be parseable and every route should have both
  `handler_symbol` and `resolved_path`.
- Annotation, route, symbol, and diagnostic counts should be recorded from each
  smoke run and compared against the checked-in facts baseline.
- Impact smoke should record changed source count, changed root count,
  recursive tree node count and endpoint count.
- Real BFF impact smoke should assert exact impacted endpoint/IM event sets and deterministic output.
- Unsupported patterns should appear as diagnostics instead of being silently
  lost where the analyzer can identify them.

## Latest Facts Smoke Snapshot

Local smoke run on 2026-07-02:

| Project | Symbols | Annotations | Routes | Diagnostics |
| --- | ---: | ---: | ---: | ---: |
| `sl-sc1-bff-service` | 781 | 32 | 32 | 0 |
| `sl-sc1-admin-bff` | 5132 | 463 | 559 | 5 |
| `sl-sc2-admin-bff` | 1397 | 98 | 136 | 0 |

The analyzer now honors the default Go build context when loading files, so
files excluded by build constraints such as `//go:build ignore` and
`//go:build race_test` are not included in these symbol counts.

All five remaining diagnostics are `symbol_reference_ambiguous_interface` for
`sc_redisx.SentinelClient.Eval/Scan`. Production `.go` files assign both
`RedisClusterClient` and `RedisClientMock`, so strict interface dispatch reports
the ambiguous binding instead of guessing. Unique package-level interface
bindings, static map interface dispatch where every map value is known,
`new(T)` package/local values, and methods on typed constants now resolve.
External SDK methods reached through project package variables or locals that
shadow project imports are not reported as unresolved project symbols.

## Impact Fixture Snapshot

The checked-in `type-impact` fixture validates the post-change single-snapshot
path:

```text
Address
  -> CreateOrderRequest
  -> OrderAPI.Create
  -> POST route
  -> POST /orders annotation
  -> POST /orders endpoint
```

The smoke script records:

- changed source count.
- changed root count.
- recursive impact tree node count.
- endpoint count.
- unresolved-reference diagnostics.
- runtime.

Latest fixture result:

| Fixture | Changed sources | Changed roots | Tree nodes | Endpoints | Unresolved diagnostics | Runtime |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `type-impact` | 1 | 1 | 9 | 1 (`POST /orders`) | 0 | <1s |

## Specialized Impact Fixtures

The smoke script also validates the three post-MVP propagation paths:

| Fixture | Scenario | Expected endpoint |
| --- | --- | --- |
| `deleted-route` | multiline route registration removed from post-change source | `POST /internal/orders` plus deletion-anchor `GET /health` |
| `gomod-impact` | require-block dependency upgrade to local import usage | `GET /api/checkIn` |
| `middleware-selector` | package var + struct field middleware method change | `GET /orders` |

The module and middleware fixtures complete with one endpoint. The deleted-route
fixture conservatively includes the surviving route at the deletion anchor.

## Real BFF Impact Cases

The smoke script validates twenty-seven real-file diff cases across the three target BFF
projects:

| Case | Project file | Expected impact |
| --- | --- | --- |
| `real-admin-customer-empty-path` | `sl-sc1-admin-bff/controller/mc/customer/customer.go` | 2 endpoints: `GET /admin/api/bff-web/mc/customer/:customerId` and compatibility `GET /uc/customers/:customerId` |
| `real-admin-customer-wrapper` | `sl-sc1-admin-bff/controller/mc/customer/customer.go` | 2 endpoints: `PUT /admin/api/bff-web/mc/customer/:customerId` and compatibility `PUT /uc/customers/:customerId` |
| `real-admin-product-set-list` | `sl-sc1-admin-bff/controller/trade/product/product.go` | `GET /admin/api/bff-web/trade/product/product_set/list` |
| `real-admin-user-info` | `sl-sc1-admin-bff/controller/user/user.go` | `GET /admin/api/bff-web/user/info` |
| `real-admin-app-live-statistics` | `sl-sc1-admin-bff/controller/app/live/live.go` | 2 endpoints: `GET /admin/api/bff-app/live/sale/:salesId/statistics` and compatibility `GET /api/posts/post/sales/statistics/:salesId` |
| `real-admin-route-helper` | `sl-sc1-admin-bff/router/live/activity.go` | 20 inline/assigned-group routes using `AddLiveWriteGuard` |
| `real-admin-assigned-route-helper` | `sl-sc1-admin-bff/router/live/activity.go` | 37 activity/sale routes using inline or assigned `AddLiveReadGuard` groups |
| `real-admin-returned-group-middleware` | `sl-sc1-admin-bff/middleware/mock/mock_auth.go` | 424 endpoints reached through `createAdminAuthGroup` return values and child router parameters |
| `real-admin-route-param-group` | `sl-sc1-admin-bff/pkg/auth/cache/auth_redis.go` | `POST /admin/api/bff-web/auth/revokeToken/:clientId` through the second route-group parameter |
| `real-admin-path-param-flow-control` | `sl-sc1-admin-bff/router/live/activity.go` | 4 routes using package-level `createPathParamsFlowControlMid(...)` initializers |
| `real-admin-conversation-action-map` | `sl-sc1-admin-bff/service/mc/conversation_service.go` | app + web conversation routes through static map interface dispatch |
| `real-admin-user-annotation-drift` | `sl-sc1-admin-bff/controller/user/user.go` | annotation path drift is corrected back to registered route `GET /admin/api/bff-web/user/info` |
| `real-client-common-checkin` | `sl-sc1-bff-service/controller/common/common.go` | `POST /api/bff-web/common/checkIn` |
| `real-client-checkin-annotation-drift` | `sl-sc1-bff-service/controller/common/common.go` | annotation path drift is corrected back to registered route `POST /api/bff-web/common/checkIn` |
| `real-client-gomod-and-checkin` | `sl-sc1-bff-service/go.mod` + `sl-sc1-bff-service/controller/common/common.go` | 1 `fileSources` endpoint plus 10 Nexus endpoints from upgraded `github.com/shopspring/decimal` |
| `real-client-multi-module-and-multi-source` | `go.mod` + `controller/common/common.go` + `model/form_product.go` + `service/merchant.go` | 3 file roots, 3 upgraded module sources and 31 deduplicated endpoints |
| `real-client-live-view` | `sl-sc1-bff-service/controller/live/view/redirect.go` | `GET /api/bff-web/live/view/:salesId/redirect` |
| `real-client-interface-dispatch` | `sl-sc1-bff-service/remote/oa/oa.go` | 3 exact endpoints through both direct and service callers of `oaClient.GetMerchant` |
| `real-admin-new-builtin` | `sl-sc1-admin-bff/pkg/auth/cache/auth_redis.go` | `GET /admin/api/bff-web/auth/oauth/callback` |
| `real-admin-typed-const` | `sl-sc1-admin-bff/service/uc/merchant_setting_code.go` | `POST /admin/api/bff-web/uc/merchant/setting/get` |
| `real-sc2-channel-count` | `sl-sc2-admin-bff/controller/channel/base/channel_config.go` | `GET /admin/api/bff-web/sc/channel/count/:type` through const-concatenated route groups and a parenthesized handler |
| `real-sc2-deleted-sms-record-route` | `sl-sc2-admin-bff/router/sms_router.go` | deleted route recovery outputs full endpoint `GET /admin/api/bff-web/sc/message/sms/records` |
| `real-sc2-generic-error-wrapmsg` | `sl-sc2-admin-bff/pkg/errors/errors.go` | 109 endpoints through `NewGenericError() IGenericError` narrowed to `*GenericError`, including `GET /admin/api/bff-web/sc/mc/conversation/inbox` |
| `real-client-im-message` | `sl-sc1-bff-service/remote/pulsar/consumer/mc/inbox.go` | `inbox_msg` and `inbox_customer_msg`, excluding `inbox_conv` |
| `real-admin-im-lock` | `sl-sc1-admin-bff/service/im/im.go` | `POST/LOCK_INVENTORY_UPDATE` |
| `real-admin-im-voucher` | `sl-sc1-admin-bff/remote/pulsar/consumer/activity_convert.go` | `ACTIVITY/VOUCHER_WINNER` through a local multi-return converter |
| `real-sc2-im-message` | `sl-sc2-admin-bff/service/im/im_do.go` | `mc/message` through the generic topic/msg wrapper |

Controller handlers registered under both current and compatibility routes
produce both endpoints; the exact sets are checked. The route-helper case
proves that changing `AddLiveWriteGuard` reaches its 20 inline and assigned-group
routes. The assigned-route-helper case independently verifies 37 read-guard
routes across `activity.go` and `sale.go`, including
`saleGroupInLiveReadGuard := AddLiveReadGuard(saleGroup)`. The returned-group
case covers the real `MiddlewareWithAuthLocal -> createAdminAuthGroup ->
InitRouter -> child router` chain, checks representative web/app/legacy/internal
endpoints, and verifies that the without-auth revoke-token route is excluded.
The route-param case proves that a non-first
`*lego.RouterGroup` parameter can register endpoints. The path-param case proves
package-level initializer dependencies propagate from middleware factory helpers
to the routes that consume the initialized middleware values. The conversation
case proves strict static map interface dispatch and route-prefix propagation
through app router child functions. The annotation-drift cases prove changed
controller comments do not override the registered Lego route when the route
path is authoritative. The combined
go.mod and logic case completes with 11 endpoints: `CheckIn` remains under
`fileSources`, while the ten decimal-dependent Nexus routes are grouped under
`moduleSources`. The module source tree explicitly contains
`ParseStringToFloat64 -> ConvertPrice -> endpoint`.

The checked-in IM smoke cases cover exact common-SDK argument matching,
legacy `iota + String()`/closure wrappers, local converter return values, and
same-sender payload separation. Each case runs twice against the post-change
snapshot and compares output byte-for-byte.

`sl-sc2-admin-bff` is now part of the portable sibling-project smoke. Its route
case proves constant-concatenated group prefixes and parenthesized handlers are
linked to the same endpoint. Its error case proves a project constructor that
declares an interface return but always returns one concrete project type can
propagate method changes without guessing. Its IM case proves the generic
topic/msg wrapper reaches only `mc/message`. Its deleted-route case proves
single-line route deletions can recover the full endpoint from the surviving
group prefix plus handler annotation, rather than falling back to a local path.

The multi-module case generates six real diff hunks in four files. It verifies
independent module trees for `github.com/shopspring/decimal`,
`github.com/google/uuid`, and `go.opentelemetry.io/otel/trace`, plus ordinary
file roots for `CheckIn`, `ConvertPrice`, and `GetMerchantInfo`.

Nexus/codegen standard templates are covered by these real cases. The generated
`RegisterRouters -> RegisterRouter -> g.GET/POST/...` chain is extracted as
ordinary Lego routes, and decimal-related module changes reach generated Nexus
endpoints through `ParseStringToFloat64 -> ConvertPrice`.

The strict receiver cases validate these complete chains:

```text
oaClient.GetMerchant
  -> direct controller helper and service.GetMerchantInfo
  -> 3 exact GET endpoints

AuthRedis.GetRedirectData
  -> auth web callback
  -> auth controller callback
  -> GET /admin/api/bff-web/auth/oauth/callback

MerchantSettingCode.String
  -> merchantSettingController.Get
  -> POST /admin/api/bff-web/uc/merchant/setting/get
```

`AuthRedis.GetSessionData` was also inspected manually. It reaches
`CtxTokenMiddleWare`, but that middleware has configuration-driven early exits;
therefore it is not used as the exact endpoint fixture for receiver inference.

## Known Unsupported Patterns

- Dynamic route paths are preserved as raw expressions and reported with
  `route_dynamic_path`.
- Indirect route handlers such as map/slice lookups are reported with
  `route_unresolved_handler`.
- go.mod changes propagate conservatively from changed modules to all resolved
  local import usages; external module API/source differences are not analyzed.
- Dynamic IM event expressions remain as `im_event_unresolved` tree terminals
  and are excluded from event summaries.
- Upstream sc1-server or proto repository changes are not propagated across
  repositories into BFF IM events.
- Declarations absent from the post-change snapshot can degrade to
  `deleted_symbol_unresolved`.
- Ambiguous or unknown interface dispatch, reflection, arbitrary control-flow
  receiver reassignment and full runtime route reconstruction remain outside the
  AST-only scope. Static map literals are resolved only when every value can be
  mapped to a project concrete type implementing the declared project interface.
- Configuration-driven middleware exclusions are not path-sensitive; a method
  below such a branch can conservatively reach every statically bound route.
