#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${ROOT_DIR}/.analyzer-smoke"

mkdir -p "${OUT_DIR}"

resolve_sibling() {
  local preferred="$1"
  local legacy="$2"
  local candidate
  for candidate in "${ROOT_DIR}/../${preferred}" "${ROOT_DIR}/../${legacy}"; do
    if [[ -d "${candidate}" ]]; then
      (cd "${candidate}" && pwd)
      return
    fi
  done
  echo "missing sibling project: ${preferred} or ${legacy}" >&2
  return 1
}

validate_compact_impact() {
  local out="$1"

  python3 - "$out" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)

for required in ("summary", "fileSources", "nodes"):
    if required not in data:
        raise SystemExit(f"missing compact impact field {required}: {sys.argv[1]}")

for source in data.get("fileSources") or []:
    if not source.get("diff"):
        raise SystemExit(f"ordinary source diff was not retained: {source}")

forbidden = {"meta", "schemaVersion", "projectRoot", "span", "raw", "package", "level", "cycle"}
def walk(value):
    if isinstance(value, dict):
        leaked = forbidden.intersection(value)
        if leaked:
            raise SystemExit(f"debug fields leaked into compact impact output: {sorted(leaked)}")
        if value.get("confidence") == "high":
            raise SystemExit("high confidence should be omitted from compact impact output")
        for item in value.values():
            walk(item)
    elif isinstance(value, list):
        for item in value:
            walk(item)

walk(data)
if any(node.get("kind") == "endpoint" for node in (data.get("nodes") or {}).values()):
    raise SystemExit("endpoint nodes should not be duplicated in compact graph")
PY
}

run_project() {
  local name="$1"
  local path="$2"
  local out="${OUT_DIR}/${name}.facts.json"

  echo "analyzing ${name} at ${path}"
  (cd "${ROOT_DIR}" && GOCACHE="${GOCACHE:-/private/tmp/go-build-go-analyzer-smoke}" go run ./cmd/go-analyzer facts --project "${path}" --format json > "${out}")
  python3 -m json.tool "${out}" > /dev/null
  python3 - "$out" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)
routes = data.get("routes") or []
route_to_handler = sum(1 for link in (data.get("links") or []) if link.get("kind") == "route_to_handler")
routes_without_handler = sum(1 for route in routes if not route.get("handler_symbol"))
routes_without_resolved_path = sum(1 for route in routes if not route.get("resolved_path"))
if routes_without_handler:
    raise SystemExit(f"{routes_without_handler} routes have no handler_symbol")
if routes_without_resolved_path:
    raise SystemExit(f"{routes_without_resolved_path} routes have no resolved_path")
if route_to_handler != len(routes):
    raise SystemExit(f"route_to_handler links={route_to_handler}, routes={len(routes)}")
print(
    "symbols={symbols} annotations={annotations} routes={routes} route_links={route_links} diagnostics={diagnostics}".format(
        symbols=len(data.get("symbols") or []),
        annotations=len(data.get("annotations") or []),
        routes=len(routes),
        route_links=route_to_handler,
        diagnostics=len(data.get("diagnostics") or []),
    )
)
PY
}

write_real_file_diff() {
  local project="$1"
  local rel_file="$2"
  local old="$3"
  local new="$4"
  local out="$5"

  python3 - "$project" "$rel_file" "$old" "$new" "$out" <<'PY'
import pathlib
import subprocess
import sys

project = pathlib.Path(sys.argv[1])
rel_file, old, new, out = sys.argv[2], sys.argv[3], sys.argv[4], pathlib.Path(sys.argv[5])
path = project / rel_file
original = path.read_text(encoding="utf-8")
if old not in original:
    raise SystemExit(f"text not found in {rel_file}: {old!r}")

def git(*args, check=True, stdout=None):
    return subprocess.run(["git", "-C", str(project), *args], check=check, stdout=stdout)

if git("diff", "--quiet", "--", rel_file, check=False).returncode != 0:
    raise SystemExit(f"{rel_file} already has unstaged changes")
if git("diff", "--cached", "--quiet", "--", rel_file, check=False).returncode != 0:
    raise SystemExit(f"{rel_file} already has staged changes")

try:
    path.write_text(original.replace(old, new, 1), encoding="utf-8")
    with out.open("wb") as f:
        git("diff", "--", rel_file, stdout=f)
finally:
    path.write_text(original, encoding="utf-8")

if not out.read_text(encoding="utf-8").strip():
    raise SystemExit(f"generated empty diff for {rel_file}")
if git("diff", "--quiet", "--", rel_file, check=False).returncode != 0:
    raise SystemExit(f"{rel_file} was not restored")
PY
}

write_real_multi_file_diff() {
  local project="$1"
  local out="$2"
  shift 2

  python3 - "$project" "$out" "$@" <<'PY'
import pathlib
import subprocess
import sys

project = pathlib.Path(sys.argv[1])
out = pathlib.Path(sys.argv[2])
items = sys.argv[3:]
if len(items) == 0 or len(items) % 3 != 0:
    raise SystemExit("expected replacement triples: rel_file old new")

replacements = []
for i in range(0, len(items), 3):
    rel_file, old, new = items[i], items[i + 1], items[i + 2]
    path = project / rel_file
    original = path.read_text(encoding="utf-8")
    if old not in original:
        raise SystemExit(f"text not found in {rel_file}: {old!r}")
    replacements.append((rel_file, path, original, old, new))

def git(*args, check=True, stdout=None):
    return subprocess.run(["git", "-C", str(project), *args], check=check, stdout=stdout)

for rel_file, _, _, _, _ in replacements:
    if git("diff", "--quiet", "--", rel_file, check=False).returncode != 0:
        raise SystemExit(f"{rel_file} already has unstaged changes")
    if git("diff", "--cached", "--quiet", "--", rel_file, check=False).returncode != 0:
        raise SystemExit(f"{rel_file} already has staged changes")

try:
    for _, path, original, old, new in replacements:
        path.write_text(original.replace(old, new, 1), encoding="utf-8")
    rel_files = [item[0] for item in replacements]
    with out.open("wb") as f:
        git("diff", "--", *rel_files, stdout=f)
finally:
    for _, path, original, _, _ in replacements:
        path.write_text(original, encoding="utf-8")

if not out.read_text(encoding="utf-8").strip():
    raise SystemExit("generated empty diff")
for rel_file, _, _, _, _ in replacements:
    if git("diff", "--quiet", "--", rel_file, check=False).returncode != 0:
        raise SystemExit(f"{rel_file} was not restored")
PY
}

run_real_impact_case() {
  local name="$1"
  local project="$2"
  local patch="$3"
  local method="$4"
  local endpoint_path="$5"
  local module_path="${6:-}"
  local module_kind="${7:-}"
  local out="${OUT_DIR}/${name}.impact.json"

  echo "analyzing ${name} real impact case"
  (cd "${ROOT_DIR}" && GOCACHE="${GOCACHE:-/private/tmp/go-build-go-analyzer-smoke}" go run ./cmd/go-analyzer impact --project "${project}" --diff "${patch}" --format json > "${out}")
  python3 -m json.tool "${out}" > /dev/null
  validate_compact_impact "${out}"
  python3 - "$out" "$method" "$endpoint_path" "$name" "$module_path" "$module_kind" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)

method, path, name = sys.argv[2], sys.argv[3], sys.argv[4]
module_path, module_kind = sys.argv[5], sys.argv[6]
summary = data.get("summary") or {}
endpoints = summary.get("impactedEndpoints") or []
if not any(endpoint.get("method") == method and endpoint.get("path") == path for endpoint in endpoints):
    raise SystemExit(f"{method} {path} not found for {name}: {endpoints}")
if module_path:
    module_sources = data.get("moduleSources") or []
    if not any(source.get("modulePath") == module_path and source.get("changeType") == module_kind for source in module_sources):
        raise SystemExit(f"{module_kind} {module_path} module source not found for {name}: {module_sources}")
print(
    "real_case={name} impactedEndpointCount={count} endpoints={endpoints}".format(
        name=name,
        count=summary.get("impactedEndpointCount"),
        endpoints=len(endpoints),
    )
)
PY
}

validate_decimal_module_case() {
  local out="${OUT_DIR}/real-client-gomod-and-checkin.impact.json"

  python3 - "$out" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)

expected_module_endpoints = {
    ("DELETE", "/api/bff-web/mc/form/:shortLinkId/user/:userId/cart/item/:cartItemId"),
    ("DELETE", "/mc/form/client/product/:shortLinkId/user/:userId/cart/item/:cartItemId"),
    ("GET", "/api/bff-web/mc/form/:shortLinkId/product"),
    ("GET", "/api/bff-web/mc/form/:shortLinkId/user/:userId/cart"),
    ("GET", "/api/bff-web/mc/form/preview/:formId"),
    ("GET", "/mc/form/client/product/:shortLinkId"),
    ("GET", "/mc/form/client/product/:shortLinkId/user/:userId/cart"),
    ("GET", "/mc/form/client/product/form/:formId"),
    ("POST", "/api/bff-web/mc/form/:shortLinkId/user/:userId/cart/item"),
    ("POST", "/mc/form/client/product/:shortLinkId/user/:userId/cart/item"),
}

file_sources = data.get("fileSources") or []
if len(file_sources) != 1 or file_sources[0].get("sourceFile") != "controller/common/common.go":
    raise SystemExit(f"unexpected fileSources: {file_sources}")
file_endpoints = {
    (item.get("method"), item.get("path"))
    for item in (file_sources[0].get("impactedEndpoints") or [])
}
if file_endpoints != {("POST", "/api/bff-web/common/checkIn")}:
    raise SystemExit(f"unexpected CheckIn file endpoints: {file_endpoints}")

module_sources = data.get("moduleSources") or []
decimal_sources = [
    item for item in module_sources
    if item.get("modulePath") == "github.com/shopspring/decimal"
]
if len(decimal_sources) != 1:
    raise SystemExit(f"unexpected decimal moduleSources: {decimal_sources}")
decimal = decimal_sources[0]
if decimal.get("changeType") != "upgraded":
    raise SystemExit(f"unexpected decimal changeType: {decimal}")
source_files = decimal.get("sourceFiles") or []
if len(source_files) != 1 or source_files[0].get("sourceFile") != "util/transform/transform.go":
    raise SystemExit(f"unexpected decimal sourceFiles: {source_files}")
module_endpoints = {
    (item.get("method"), item.get("path"))
    for item in (source_files[0].get("impactedEndpoints") or [])
}
if module_endpoints != expected_module_endpoints:
    raise SystemExit(
        f"decimal endpoints mismatch: missing={expected_module_endpoints - module_endpoints}, "
        f"unexpected={module_endpoints - expected_module_endpoints}"
    )

summary = data.get("summary") or {}
if summary.get("impactedEndpointCount") != 11:
    raise SystemExit(f"unexpected combined endpoint count: {summary}")
if data.get("diagnostics"):
    raise SystemExit(f"unrelated diagnostics leaked into impact output: {data['diagnostics']}")
PY
}

run_impact_fixture() {
  local project="${ROOT_DIR}/testdata/fixtures/type-impact"
  local patch="${ROOT_DIR}/testdata/diffs/type-impact.diff"
  local out="${OUT_DIR}/type-impact.impact.json"
  local started="${SECONDS}"

  echo "analyzing type-impact fixture"
  (cd "${ROOT_DIR}" && GOCACHE="${GOCACHE:-/private/tmp/go-build-go-analyzer-smoke}" go run ./cmd/go-analyzer impact --project "${project}" --diff "${patch}" --format json > "${out}")
  python3 -m json.tool "${out}" > /dev/null
  validate_compact_impact "${out}"
  python3 - "$out" "$((SECONDS - started))" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)

sources = data.get("fileSources") or []
roots = sum(len(source.get("roots") or []) for source in sources)
endpoints = [
    endpoint
    for source in sources
    for endpoint in (source.get("impactedEndpoints") or [])
]
if not any(endpoint.get("method") == "POST" and endpoint.get("path") == "/orders" for endpoint in endpoints):
    raise SystemExit("POST /orders not found")

nodes = len(data.get("nodes") or {})
unresolved = sum(
    diagnostic.get("code") in {"symbol_reference_unresolved", "type_reference_unresolved"}
    for diagnostic in (data.get("diagnostics") or [])
)
print(
    "changed_sources={sources} changed_roots={roots} nodes={nodes} "
    "endpoints={endpoints} unresolved={unresolved} runtime_seconds={runtime}".format(
        sources=len(sources),
        roots=roots,
        nodes=nodes,
        endpoints=len(endpoints),
        unresolved=unresolved,
        runtime=sys.argv[2],
    )
)
PY
}

run_impact_case() {
  local name="$1"
  local method="$2"
  local endpoint_path="$3"
  local project="${ROOT_DIR}/testdata/fixtures/${name}"
  local patch="${ROOT_DIR}/testdata/diffs/${name}.diff"
  local out="${OUT_DIR}/${name}.impact.json"

  echo "analyzing ${name} impact fixture"
  (cd "${ROOT_DIR}" && GOCACHE="${GOCACHE:-/private/tmp/go-build-go-analyzer-smoke}" go run ./cmd/go-analyzer impact --project "${project}" --diff "${patch}" --format json > "${out}")
  python3 -m json.tool "${out}" > /dev/null
  validate_compact_impact "${out}"
  python3 - "$out" "$method" "$endpoint_path" "$name" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)

method, path, name = sys.argv[2], sys.argv[3], sys.argv[4]
endpoints = (data.get("summary") or {}).get("impactedEndpoints") or []
if not any(endpoint.get("method") == method and endpoint.get("path") == path for endpoint in endpoints):
    raise SystemExit(f"{method} {path} not found for {name}")
if name == "gomod-impact" and not data.get("moduleSources"):
    raise SystemExit("gomod-impact did not emit moduleSources")
print(
    "fixture={name} changed_sources={sources} endpoints={endpoints} diagnostics={diagnostics}".format(
        name=name,
        sources=len(data.get("fileSources") or []),
        endpoints=len(endpoints),
        diagnostics=len(data.get("diagnostics") or []),
    )
)
PY
}

SC1_BFF="$(resolve_sibling "sl-sc1-bff-service" "sc1-bff-service")"
SC1_ADMIN_BFF="$(resolve_sibling "sl-sc1-admin-bff" "sc1-admin-bff")"

run_project "sl-sc1-bff-service" "${SC1_BFF}"
run_project "sl-sc1-admin-bff" "${SC1_ADMIN_BFF}"
run_impact_fixture
run_impact_case "deleted-route" "POST" "/public/orders"
run_impact_case "gomod-impact" "GET" "/api/checkIn"
run_impact_case "middleware-selector" "GET" "/orders"

write_real_file_diff \
  "${SC1_ADMIN_BFF}" \
  "controller/mc/customer/customer.go" \
  "	res, err := userGrpc.CustomerServiceClientGetCustomer(ctx, &customerService.CustomerGetReq{" \
  "	res, err := userGrpc.CustomerServiceClientGetCustomer(ctx, &customerService.CustomerGetReq{ // smoke empty path" \
  "${OUT_DIR}/real-admin-customer-empty-path.diff"
run_real_impact_case "real-admin-customer-empty-path" "${SC1_ADMIN_BFF}" "${OUT_DIR}/real-admin-customer-empty-path.diff" "GET" "/admin/api/bff-web/mc/customer/:customerId"

write_real_file_diff \
  "${SC1_ADMIN_BFF}" \
  "controller/mc/customer/customer.go" \
  "	return userGrpc.CustomerServiceClientUpdateCustomer(ctx, &customerService.CustomerUpdateReq{" \
  "	return userGrpc.CustomerServiceClientUpdateCustomer(ctx, &customerService.CustomerUpdateReq{ // smoke wrapper route" \
  "${OUT_DIR}/real-admin-customer-wrapper.diff"
run_real_impact_case "real-admin-customer-wrapper" "${SC1_ADMIN_BFF}" "${OUT_DIR}/real-admin-customer-wrapper.diff" "PUT" "/admin/api/bff-web/mc/customer/:customerId"

write_real_file_diff \
  "${SC1_ADMIN_BFF}" \
  "controller/trade/product/product.go" \
  "return productService.GetProductSetList(ctx, user.Token, req.Lang, openApi.SearchProductsReq{" \
  "return productService.GetProductSetList(ctx, user.Token, req.Lang, openApi.SearchProductsReq{ // smoke product" \
  "${OUT_DIR}/real-admin-product-set-list.diff"
run_real_impact_case "real-admin-product-set-list" "${SC1_ADMIN_BFF}" "${OUT_DIR}/real-admin-product-set-list.diff" "GET" "/admin/api/bff-web/trade/product/product_set/list"

write_real_file_diff \
  "${SC1_ADMIN_BFF}" \
  "controller/user/user.go" \
  "userInfo, err := auth.GetUserFromContextWithError(legoCtx)" \
  "userInfo, err := auth.GetUserFromContextWithError(legoCtx) // smoke user" \
  "${OUT_DIR}/real-admin-user-info.diff"
run_real_impact_case "real-admin-user-info" "${SC1_ADMIN_BFF}" "${OUT_DIR}/real-admin-user-info.diff" "GET" "/admin/api/bff-web/user/info"

write_real_file_diff \
  "${SC1_ADMIN_BFF}" \
  "controller/app/live/live.go" \
  "res, err := live.SalesStatisticsServiceClientGetStatistics(ctx, &salesStatistics.SalesStatisticsGetReq{" \
  "res, err := live.SalesStatisticsServiceClientGetStatistics(ctx, &salesStatistics.SalesStatisticsGetReq{ // smoke app live" \
  "${OUT_DIR}/real-admin-app-live-statistics.diff"
run_real_impact_case "real-admin-app-live-statistics" "${SC1_ADMIN_BFF}" "${OUT_DIR}/real-admin-app-live-statistics.diff" "GET" "/admin/api/bff-app/live/sale/:salesId/statistics"

write_real_file_diff \
  "${SC1_BFF}" \
  "controller/common/common.go" \
  "	// 获取当前商家/用户信息" \
  "	// smoke: 获取当前商家/用户信息" \
  "${OUT_DIR}/real-client-common-checkin.diff"
run_real_impact_case "real-client-common-checkin" "${SC1_BFF}" "${OUT_DIR}/real-client-common-checkin.diff" "POST" "/api/bff-web/common/checkIn"

write_real_multi_file_diff \
  "${SC1_BFF}" \
  "${OUT_DIR}/real-client-gomod-and-checkin.diff" \
  "go.mod" \
  "	github.com/shopspring/decimal v1.3.1" \
  "	github.com/shopspring/decimal v1.4.0" \
  "controller/common/common.go" \
  "			ReleaseTime:  time.Now().UnixMilli()," \
  "			ReleaseTime:  time.Now().UTC().UnixMilli(),"
run_real_impact_case "real-client-gomod-and-checkin" "${SC1_BFF}" "${OUT_DIR}/real-client-gomod-and-checkin.diff" "POST" "/api/bff-web/common/checkIn" "github.com/shopspring/decimal" "upgraded"
validate_decimal_module_case

write_real_file_diff \
  "${SC1_BFF}" \
  "controller/live/view/redirect.go" \
  "	resp, err := live.LiveViewClient.GetMerchantIdBySalesId(c, &live_viewer_client.GetMerchantIdReq{" \
  "	resp, err := live.LiveViewClient.GetMerchantIdBySalesId(c, &live_viewer_client.GetMerchantIdReq{ // smoke route" \
  "${OUT_DIR}/real-client-live-view.diff"
run_real_impact_case "real-client-live-view" "${SC1_BFF}" "${OUT_DIR}/real-client-live-view.diff" "GET" "/api/bff-web/live/view/:salesId/redirect"

echo "smoke outputs written to ${OUT_DIR}"
