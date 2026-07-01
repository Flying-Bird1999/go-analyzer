#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${ROOT_DIR}/.analyzer-smoke"
ANALYZER_BIN="${OUT_DIR}/go-analyzer"
ACTIVE_PROJECT=""
ACTIVE_PATCH=""

mkdir -p "${OUT_DIR}"

restore_active_change() {
  if [[ -z "${ACTIVE_PROJECT}" ]]; then
    return
  fi
  git -C "${ACTIVE_PROJECT}" apply --reverse "${ACTIVE_PATCH}"
  ACTIVE_PROJECT=""
  ACTIVE_PATCH=""
}

cleanup() {
  local status=$?
  trap - EXIT
  if ! restore_active_change; then
    status=1
  fi
  exit "${status}"
}

trap cleanup EXIT

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

validate_raw_impact() {
  local out="$1"

  python3 - "$out" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)

for required in ("summary", "fileSources"):
    if required not in data:
        raise SystemExit(f"missing raw impact field {required}: {sys.argv[1]}")
if "nodes" in data:
    raise SystemExit("raw impact report must keep trees in sources, not top-level nodes")

summary = data["summary"]
if not isinstance(summary.get("impactedIMCount"), int):
    raise SystemExit(f"summary impactedIMCount missing: {summary}")
if not isinstance(summary.get("impactedIMEvents"), list):
    raise SystemExit(f"summary impactedIMEvents missing: {summary}")

for source in data.get("fileSources") or []:
    if not source.get("diff"):
        raise SystemExit(f"ordinary source diff was not retained: {source}")
    if not isinstance(source.get("symbols"), dict):
        raise SystemExit(f"ordinary source symbols missing: {source}")
    if not isinstance(source.get("impactedIMEvents"), list):
        raise SystemExit(f"ordinary source impactedIMEvents missing: {source}")
for module in data.get("moduleSources") or []:
    for source in module.get("sourceFiles") or []:
        if not isinstance(source.get("symbols"), dict):
            raise SystemExit(f"module source symbols missing: {source}")
        if not isinstance(source.get("impactedIMEvents"), list):
            raise SystemExit(f"module source impactedIMEvents missing: {source}")

forbidden = {"meta", "schemaVersion", "projectRoot", "span"}
def walk(value):
    if isinstance(value, dict):
        leaked = forbidden.intersection(value)
        if leaked:
            raise SystemExit(f"retired fields leaked into raw impact output: {sorted(leaked)}")
        if "kind" in value and ("id" not in value or not isinstance(value.get("children"), list)):
            raise SystemExit(f"invalid recursive impact node: {value}")
        for item in value.values():
            walk(item)
    elif isinstance(value, list):
        for item in value:
            walk(item)

walk(data)
PY
}

run_project() {
  local name="$1"
  local path="$2"
  local out="${OUT_DIR}/${name}.facts.json"

  echo "analyzing ${name} at ${path}"
  "${ANALYZER_BIN}" facts --project "${path}" --format json > "${out}"
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

  if [[ -n "${ACTIVE_PROJECT}" ]]; then
    echo "previous smoke change is still active: ${ACTIVE_PATCH}" >&2
    return 1
  fi
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
    if not out.read_text(encoding="utf-8").strip():
        raise RuntimeError(f"generated empty diff for {rel_file}")
except BaseException:
    path.write_text(original, encoding="utf-8")
    raise
PY
  ACTIVE_PROJECT="${project}"
  ACTIVE_PATCH="${out}"
}

write_real_multi_file_diff() {
  local project="$1"
  local out="$2"
  shift 2

  if [[ -n "${ACTIVE_PROJECT}" ]]; then
    echo "previous smoke change is still active: ${ACTIVE_PATCH}" >&2
    return 1
  fi
  python3 - "$project" "$out" "$@" <<'PY'
import pathlib
import subprocess
import sys

project = pathlib.Path(sys.argv[1])
out = pathlib.Path(sys.argv[2])
items = sys.argv[3:]
if len(items) == 0 or len(items) % 3 != 0:
    raise SystemExit("expected replacement triples: rel_file old new")

files = {}
for i in range(0, len(items), 3):
    rel_file, old, new = items[i], items[i + 1], items[i + 2]
    if rel_file not in files:
        path = project / rel_file
        original = path.read_text(encoding="utf-8")
        files[rel_file] = {
            "path": path,
            "original": original,
            "updated": original,
        }
    item = files[rel_file]
    if old not in item["updated"]:
        raise SystemExit(f"text not found in {rel_file}: {old!r}")
    item["updated"] = item["updated"].replace(old, new, 1)

def git(*args, check=True, stdout=None):
    return subprocess.run(["git", "-C", str(project), *args], check=check, stdout=stdout)

for rel_file in files:
    if git("diff", "--quiet", "--", rel_file, check=False).returncode != 0:
        raise SystemExit(f"{rel_file} already has unstaged changes")
    if git("diff", "--cached", "--quiet", "--", rel_file, check=False).returncode != 0:
        raise SystemExit(f"{rel_file} already has staged changes")

try:
    for item in files.values():
        item["path"].write_text(item["updated"], encoding="utf-8")
    rel_files = list(files)
    with out.open("wb") as f:
        git("diff", "--", *rel_files, stdout=f)
    if not out.read_text(encoding="utf-8").strip():
        raise RuntimeError("generated empty diff")
except BaseException:
    for item in files.values():
        item["path"].write_text(item["original"], encoding="utf-8")
    raise
PY
  ACTIVE_PROJECT="${project}"
  ACTIVE_PATCH="${out}"
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
  local repeated_out="${OUT_DIR}/${name}.repeat.impact.json"

  echo "analyzing ${name} real impact case"
  if [[ "${ACTIVE_PROJECT}" != "${project}" || "${ACTIVE_PATCH}" != "${patch}" ]]; then
    echo "real impact case must run against its active post-change source: ${name}" >&2
    return 1
  fi
  "${ANALYZER_BIN}" impact --project "${project}" --diff "${patch}" --format json > "${out}"
  "${ANALYZER_BIN}" impact --project "${project}" --diff "${patch}" --format json > "${repeated_out}"
  cmp "${out}" "${repeated_out}"
  rm -f "${repeated_out}"
  python3 -m json.tool "${out}" > /dev/null
  validate_raw_impact "${out}"
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
  restore_active_change
}

run_real_im_case() {
  local name="$1"
  local project="$2"
  local patch="$3"
  shift 3
  local out="${OUT_DIR}/${name}.impact.json"
  local repeated_out="${OUT_DIR}/${name}.repeat.impact.json"

  echo "analyzing ${name} real IM impact case"
  if [[ "${ACTIVE_PROJECT}" != "${project}" || "${ACTIVE_PATCH}" != "${patch}" ]]; then
    echo "real IM impact case must run against its active post-change source: ${name}" >&2
    return 1
  fi
  "${ANALYZER_BIN}" impact --project "${project}" --diff "${patch}" --format json > "${out}"
  "${ANALYZER_BIN}" impact --project "${project}" --diff "${patch}" --format json > "${repeated_out}"
  cmp "${out}" "${repeated_out}"
  rm -f "${repeated_out}"
  python3 -m json.tool "${out}" > /dev/null
  validate_raw_impact "${out}"
  python3 - "$out" "$name" "$@" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)

name = sys.argv[2]
expected = set(sys.argv[3:])
summary = data.get("summary") or {}
actual = set(summary.get("impactedIMEvents") or [])
if actual != expected:
    raise SystemExit(
        f"{name} IM event mismatch: missing={expected - actual}, unexpected={actual - expected}"
    )
if summary.get("impactedIMCount") != len(expected):
    raise SystemExit(f"{name} invalid impactedIMCount: {summary}")
if summary.get("impactedEndpointCount") != 0:
    raise SystemExit(f"{name} unexpectedly impacted HTTP endpoints: {summary}")

source_events = set()
for source in data.get("fileSources") or []:
    source_events.update(source.get("impactedIMEvents") or [])
if source_events != expected:
    raise SystemExit(f"{name} source IM event mismatch: {source_events}")

terminals = []
def walk(value):
    if isinstance(value, dict):
        if value.get("kind") == "im_event":
            terminals.append(value.get("name"))
        for item in value.values():
            walk(item)
    elif isinstance(value, list):
        for item in value:
            walk(item)

walk(data.get("fileSources") or [])
if set(terminals) != expected:
    raise SystemExit(f"{name} IM terminal mismatch: {terminals}")
print(f"real_im_case={name} impactedIMCount={len(expected)} events={sorted(expected)}")
PY
  restore_active_change
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
symbols = source_files[0].get("symbols") or {}
parse_id = "func:sc1-client-bff-service/util/transform::ParseStringToFloat64"
convert_id = "func:sc1-client-bff-service/model::ConvertPrice"
parse_node = symbols.get(parse_id)
if not parse_node:
    raise SystemExit(f"ParseStringToFloat64 root missing from module source: {symbols.keys()}")
convert_nodes = [
    child for child in (parse_node.get("children") or [])
    if child.get("id") == convert_id
]
if len(convert_nodes) != 1:
    raise SystemExit(f"ParseStringToFloat64 -> ConvertPrice chain missing: {parse_node}")

def collect_endpoint_nodes(node, out):
    if node.get("kind") == "endpoint":
        out.add((node.get("method"), node.get("path")))
    for child in node.get("children") or []:
        collect_endpoint_nodes(child, out)

tree_endpoints = set()
collect_endpoint_nodes(parse_node, tree_endpoints)
if tree_endpoints != expected_module_endpoints:
    raise SystemExit(
        f"decimal tree endpoint mismatch: missing={expected_module_endpoints - tree_endpoints}, "
        f"unexpected={tree_endpoints - expected_module_endpoints}"
    )
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

validate_multi_module_case() {
  local out="${OUT_DIR}/real-client-multi-module-and-multi-source.impact.json"

  python3 - "$out" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)

expected_file_roots = {
    "controller/common/common.go": "func:sc1-client-bff-service/controller/common::CheckIn",
    "model/form_product.go": "func:sc1-client-bff-service/model::ConvertPrice",
    "service/merchant.go": "func:sc1-client-bff-service/service::GetMerchantInfo",
}
file_sources = {
    source.get("sourceFile"): source
    for source in (data.get("fileSources") or [])
}
if set(file_sources) != set(expected_file_roots):
    raise SystemExit(f"unexpected multi-source files: {file_sources.keys()}")
for source_file, root_id in expected_file_roots.items():
    source = file_sources[source_file]
    if not source.get("diff"):
        raise SystemExit(f"diff missing from {source_file}")
    if root_id not in (source.get("symbols") or {}):
        raise SystemExit(f"root {root_id} missing from {source_file}: {source.get('symbols')}")

expected_modules = {
    "github.com/shopspring/decimal": (
        "util/transform/transform.go",
        "func:sc1-client-bff-service/util/transform::ParseStringToFloat64",
    ),
    "github.com/google/uuid": (
        "util/trace/trace.go",
        "func:sc1-client-bff-service/util/trace::GetTraceID",
    ),
    "go.opentelemetry.io/otel/trace": (
        "util/trace/trace.go",
        "func:sc1-client-bff-service/util/trace::GetTraceID",
    ),
}
module_sources = {
    source.get("modulePath"): source
    for source in (data.get("moduleSources") or [])
}
if set(module_sources) != set(expected_modules):
    raise SystemExit(f"unexpected module sources: {module_sources.keys()}")

def collect_endpoint_nodes(node, out):
    if node.get("kind") == "endpoint":
        out.add((node.get("method"), node.get("path")))
    for child in node.get("children") or []:
        collect_endpoint_nodes(child, out)

all_source_endpoints = set()
for source in file_sources.values():
    all_source_endpoints.update(
        (item.get("method"), item.get("path"))
        for item in (source.get("impactedEndpoints") or [])
    )

for module_path, (source_file, root_id) in expected_modules.items():
    module = module_sources[module_path]
    if module.get("changeType") != "upgraded":
        raise SystemExit(f"module was not upgraded: {module}")
    sources = module.get("sourceFiles") or []
    if len(sources) != 1 or sources[0].get("sourceFile") != source_file:
        raise SystemExit(f"unexpected usage files for {module_path}: {sources}")
    root = (sources[0].get("symbols") or {}).get(root_id)
    if not root:
        raise SystemExit(f"root {root_id} missing for {module_path}: {sources[0]}")
    tree_endpoints = set()
    collect_endpoint_nodes(root, tree_endpoints)
    summary_endpoints = {
        (item.get("method"), item.get("path"))
        for item in (sources[0].get("impactedEndpoints") or [])
    }
    if tree_endpoints != summary_endpoints:
        raise SystemExit(
            f"tree/summary endpoint mismatch for {module_path}: "
            f"tree={tree_endpoints}, summary={summary_endpoints}"
        )
    if not tree_endpoints:
        raise SystemExit(f"module {module_path} did not propagate to endpoints")
    all_source_endpoints.update(summary_endpoints)

summary = data.get("summary") or {}
global_endpoints = {
    (item.get("method"), item.get("path"))
    for item in (summary.get("impactedEndpoints") or [])
}
if global_endpoints != all_source_endpoints:
    raise SystemExit(
        f"global endpoint union mismatch: missing={all_source_endpoints - global_endpoints}, "
        f"unexpected={global_endpoints - all_source_endpoints}"
    )
if summary.get("impactedEndpointCount") != len(global_endpoints):
    raise SystemExit(f"invalid endpoint count: {summary}")

print(
    "multi_module_case files={files} modules={modules} endpoints={endpoints}".format(
        files=len(file_sources),
        modules=len(module_sources),
        endpoints=len(global_endpoints),
    )
)
PY
}

validate_returned_group_case() {
  local out="${OUT_DIR}/real-admin-returned-group-middleware.impact.json"

  python3 - "$out" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)

summary = data.get("summary") or {}
endpoints = {
    (item.get("method"), item.get("path"))
    for item in summary.get("impactedEndpoints") or []
}
expected_examples = {
    ("GET", "/admin/api/bff-web/user/info"),
    ("GET", "/admin/api/bff-app/live/sale/:salesId/statistics"),
    ("GET", "/uc/customers/:customerId"),
    ("GET", "/internal/*proxyPath"),
}
missing = expected_examples - endpoints
if missing:
    raise SystemExit(f"returned-group representative endpoints missing: {missing}")
if ("POST", "/admin/api/bff-web/auth/revokeToken/:clientId") in endpoints:
    raise SystemExit("without-auth revokeToken route leaked into returned auth group impact")
if summary.get("impactedEndpointCount") != 424 or len(endpoints) != 424:
    raise SystemExit(f"unexpected returned-group endpoint count: {summary.get('impactedEndpointCount')}, set={len(endpoints)}")
PY
}

validate_known_real_endpoint_sets() {
  python3 - "${OUT_DIR}" <<'PY'
import json
import pathlib
import sys

out_dir = pathlib.Path(sys.argv[1])
expected = {
    "real-admin-customer-empty-path": {
        ("GET", "/admin/api/bff-web/mc/customer/:customerId"),
        ("GET", "/uc/customers/:customerId"),
    },
    "real-admin-customer-wrapper": {
        ("PUT", "/admin/api/bff-web/mc/customer/:customerId"),
        ("PUT", "/uc/customers/:customerId"),
    },
    "real-admin-product-set-list": {
        ("GET", "/admin/api/bff-web/trade/product/product_set/list"),
    },
    "real-admin-user-info": {
        ("GET", "/admin/api/bff-web/user/info"),
    },
    "real-admin-app-live-statistics": {
        ("GET", "/admin/api/bff-app/live/sale/:salesId/statistics"),
        ("GET", "/api/posts/post/sales/statistics/:salesId"),
    },
    "real-admin-route-helper": {
        ("DELETE", "/admin/api/bff-web/live/sale/:salesId/sale_list/product_set"),
        ("DELETE", "/admin/api/bff-web/live/activity/:activityId"),
        ("GET", "/admin/api/bff-web/live/sale/:salesId/keyword/cur_available_seq"),
        ("PATCH", "/admin/api/bff-web/live/activity/voucher/:activityId"),
        ("POST", "/admin/api/bff-web/live/activity/:id"),
        ("POST", "/admin/api/bff-web/live/activity/:id/comment/post"),
        ("POST", "/admin/api/bff-web/live/activity/:id/end"),
        ("POST", "/admin/api/bff-web/live/activity/:id/reward/again"),
        ("POST", "/admin/api/bff-web/live/activity/:id/reward/manual"),
        ("POST", "/admin/api/bff-web/live/activity/:id/rewards/invalid"),
        ("POST", "/admin/api/bff-web/live/activity/:id/start"),
        ("POST", "/admin/api/bff-web/live/activity/bidding/:activityId/publish-maximum-bid"),
        ("POST", "/admin/api/bff-web/live/activity/end/manual"),
        ("POST", "/admin/api/bff-web/live/sale/:salesId/config/commentSync"),
        ("POST", "/admin/api/bff-web/live/sale/:salesId/config/shopperApp"),
        ("POST", "/admin/api/bff-web/live/sale/:salesId/sale_list/product_set"),
        ("POST", "/admin/api/bff-web/live/sale/:salesId/sale_list/product_set/recommend"),
        ("PUT", "/admin/api/bff-web/live/activity/:activityId"),
        ("PUT", "/admin/api/bff-web/live/sale/:salesId/sale_list/product_set"),
        ("PUT", "/admin/api/bff-web/live/sale/:salesId/sale_list/product_set/keyword/status"),
    },
    "real-admin-assigned-route-helper": {
        ("GET", "/admin/api/bff-web/live/activity/answerFirst/:activityId"),
        ("GET", "/admin/api/bff-web/live/activity/answerFirst/:activityId/winner/detail/list"),
        ("GET", "/admin/api/bff-web/live/activity/answerFirst/:activityId/winner/list"),
        ("GET", "/admin/api/bff-web/live/activity/bidding/:activityId"),
        ("GET", "/admin/api/bff-web/live/activity/bidding/:activityId/task/complete/log"),
        ("GET", "/admin/api/bff-web/live/activity/bidding/:activityId/user/list"),
        ("GET", "/admin/api/bff-web/live/activity/bidding/:activityId/winner/list"),
        ("GET", "/admin/api/bff-web/live/activity/last-progress/:salesId/info"),
        ("GET", "/admin/api/bff-web/live/activity/list/:businessType/:salesId"),
        ("GET", "/admin/api/bff-web/live/activity/list/business/:businessType/:businessId/validation"),
        ("GET", "/admin/api/bff-web/live/activity/list/sync-status/:businessType/:salesId"),
        ("GET", "/admin/api/bff-web/live/activity/luckyDraw/:activityId"),
        ("GET", "/admin/api/bff-web/live/activity/luckyDraw/:activityId/user/list"),
        ("GET", "/admin/api/bff-web/live/activity/luckyDraw/:activityId/winner/detail/list"),
        ("GET", "/admin/api/bff-web/live/activity/luckyDraw/:activityId/winner/list"),
        ("GET", "/admin/api/bff-web/live/activity/post/sales/keys/repeat/:salesId"),
        ("GET", "/admin/api/bff-web/live/activity/vote/:activityId"),
        ("GET", "/admin/api/bff-web/live/activity/vote/:activityId/option/:optionId/user/list"),
        ("GET", "/admin/api/bff-web/live/activity/vote/:activityId/winner/list"),
        ("GET", "/admin/api/bff-web/live/activity/vote/listOption/:activityId"),
        ("GET", "/admin/api/bff-web/live/activity/voucher/:activityId"),
        ("GET", "/admin/api/bff-web/live/activity/voucher/:activityId/winner/list"),
        ("GET", "/admin/api/bff-web/live/sale/:salesId/comments/product_set/detail"),
        ("GET", "/admin/api/bff-web/live/sale/:salesId/keyword/list"),
        ("GET", "/admin/api/bff-web/live/sale/:salesId/product/product_set/:id"),
        ("GET", "/admin/api/bff-web/live/sale/:salesId/product_list"),
        ("GET", "/admin/api/bff-web/live/sale/:salesId/sale_list/product/buyers"),
        ("GET", "/admin/api/bff-web/live/sale/:salesId/statistics"),
        ("GET", "/admin/api/bff-web/live/sale/:salesId/statistics/simple"),
        ("GET", "/api/posts/comments/report/export"),
        ("GET", "/api/posts/post/sales/statistics/dynamics/:salesId"),
        ("POST", "/admin/api/bff-web/live/sale/:salesId/product/sales_statistics"),
        ("POST", "/admin/api/bff-web/live/sale/:salesId/product/sales_statistics/sku_report"),
        ("POST", "/admin/api/bff-web/live/sale/:salesId/product/sales_statistics/spu_report"),
        ("POST", "/admin/api/bff-web/live/sale/:salesId/product_set/sales_statistics/spu_report"),
        ("POST", "/admin/api/bff-web/live/sale/:salesId/sale_list/product_set/spuId"),
        ("PUT", "/admin/api/bff-web/live/sale/:salesId/product/sales_statistics/sort_priority"),
    },
    "real-admin-route-param-group": {
        ("POST", "/admin/api/bff-web/auth/revokeToken/:clientId"),
    },
    "real-admin-path-param-flow-control": {
        ("POST", "/admin/api/bff-web/live/activity/:id/end"),
        ("POST", "/admin/api/bff-web/live/activity/:id/reward/manual"),
        ("POST", "/admin/api/bff-web/live/activity/:id/start"),
        ("POST", "/admin/api/bff-web/live/activity/bidding/:activityId/publish-maximum-bid"),
    },
    "real-admin-conversation-action-map": {
        ("POST", "/admin/api/bff-app/mc/conversation/status/report"),
        ("POST", "/admin/api/bff-web/mc/syncConversation"),
    },
    "real-client-common-checkin": {
        ("POST", "/api/bff-web/common/checkIn"),
    },
    "real-client-live-view": {
        ("GET", "/api/bff-web/live/view/:salesId/redirect"),
    },
    "real-client-interface-dispatch": {
        ("GET", "/api/bff-web/live/view/:salesId/redirect"),
        ("GET", "/api/bff-web/mc/form/redirect_info/:formId"),
        ("GET", "/sc1-internal/app-proxy/api/merchant/info"),
    },
    "real-admin-new-builtin": {
        ("GET", "/admin/api/bff-web/auth/oauth/callback"),
    },
    "real-admin-typed-const": {
        ("POST", "/admin/api/bff-web/uc/merchant/setting/get"),
    },
}

for name, want in expected.items():
    with (out_dir / f"{name}.impact.json").open(encoding="utf-8") as f:
        data = json.load(f)
    got = {
        (item.get("method"), item.get("path"))
        for item in (data.get("summary") or {}).get("impactedEndpoints") or []
    }
    if got != want:
        raise SystemExit(
            f"{name} endpoint mismatch: missing={want - got}, unexpected={got - want}"
        )
PY
}

run_impact_fixture() {
  local project="${ROOT_DIR}/testdata/fixtures/type-impact"
  local patch="${ROOT_DIR}/testdata/diffs/type-impact.diff"
  local out="${OUT_DIR}/type-impact.impact.json"
  local started="${SECONDS}"

  echo "analyzing type-impact fixture"
  "${ANALYZER_BIN}" impact --project "${project}" --diff "${patch}" --format json > "${out}"
  python3 -m json.tool "${out}" > /dev/null
  validate_raw_impact "${out}"
  python3 - "$out" "$((SECONDS - started))" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)

sources = data.get("fileSources") or []
roots = sum(len(source.get("symbols") or {}) for source in sources)
endpoints = [
    endpoint
    for source in sources
    for endpoint in (source.get("impactedEndpoints") or [])
]
if not any(endpoint.get("method") == "POST" and endpoint.get("path") == "/orders" for endpoint in endpoints):
    raise SystemExit("POST /orders not found")

def count_node(node):
    return 1 + sum(count_node(child) for child in (node.get("children") or []))

nodes = sum(
    count_node(node)
    for source in sources
    for node in (source.get("symbols") or {}).values()
)
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
  "${ANALYZER_BIN}" impact --project "${project}" --diff "${patch}" --format json > "${out}"
  python3 -m json.tool "${out}" > /dev/null
  validate_raw_impact "${out}"
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

(cd "${ROOT_DIR}" && \
  GOCACHE="${GOCACHE:-/private/tmp/go-build-go-analyzer-smoke}" \
  GOMODCACHE="${GOMODCACHE:-/private/tmp/go-mod-go-analyzer-smoke}" \
  go build -o "${ANALYZER_BIN}" ./cmd/go-analyzer)

run_project "sl-sc1-bff-service" "${SC1_BFF}"
run_project "sl-sc1-admin-bff" "${SC1_ADMIN_BFF}"
run_impact_fixture
run_impact_case "deleted-route" "POST" "/internal/orders"
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
  "${SC1_ADMIN_BFF}" \
  "router/live/activity.go" \
  "	liveWritePermissionMid := middleware.Permission(middleware.Live, middleware.ShoplineStudio, middleware.CreateUpdate, false)" \
  "	liveWritePermissionMid := middleware.Permission(middleware.Live, middleware.ShoplineStudio, middleware.CreateUpdate, false) // smoke route helper" \
  "${OUT_DIR}/real-admin-route-helper.diff"
run_real_impact_case "real-admin-route-helper" "${SC1_ADMIN_BFF}" "${OUT_DIR}/real-admin-route-helper.diff" "POST" "/admin/api/bff-web/live/activity/:id"

write_real_file_diff \
  "${SC1_ADMIN_BFF}" \
  "router/live/activity.go" \
  "	liveReadPermissionMid := middleware.Permission(middleware.Live, middleware.ShoplineStudio, middleware.Read, false)" \
  "	liveReadPermissionMid := middleware.Permission(middleware.Live, middleware.ShoplineStudio, middleware.Read, false) // smoke assigned route helper" \
  "${OUT_DIR}/real-admin-assigned-route-helper.diff"
run_real_impact_case "real-admin-assigned-route-helper" "${SC1_ADMIN_BFF}" "${OUT_DIR}/real-admin-assigned-route-helper.diff" "GET" "/admin/api/bff-web/live/sale/:salesId/statistics"

write_real_file_diff \
  "${SC1_ADMIN_BFF}" \
  "middleware/mock/mock_auth.go" \
  "		mid := configx.Get(\"dev-setup.merchant-id\")" \
  "		mid := configx.Get(\"dev-setup.merchant-id\") // smoke returned route group" \
  "${OUT_DIR}/real-admin-returned-group-middleware.diff"
run_real_impact_case "real-admin-returned-group-middleware" "${SC1_ADMIN_BFF}" "${OUT_DIR}/real-admin-returned-group-middleware.diff" "GET" "/admin/api/bff-web/user/info"
validate_returned_group_case

write_real_file_diff \
  "${SC1_ADMIN_BFF}" \
  "pkg/auth/cache/auth_redis.go" \
  "	tokenKey := fmt.Sprintf(constant.TokenKey, token)" \
  "	tokenKey := fmt.Sprintf(constant.TokenKey, token) // smoke route param group" \
  "${OUT_DIR}/real-admin-route-param-group.diff"
run_real_impact_case "real-admin-route-param-group" "${SC1_ADMIN_BFF}" "${OUT_DIR}/real-admin-route-param-group.diff" "POST" "/admin/api/bff-web/auth/revokeToken/:clientId"

write_real_file_diff \
  "${SC1_ADMIN_BFF}" \
  "router/live/activity.go" \
  "			pathParams := ctx.Param(path)" \
  "			pathParams := ctx.Param(path) // smoke package initializer" \
  "${OUT_DIR}/real-admin-path-param-flow-control.diff"
run_real_impact_case "real-admin-path-param-flow-control" "${SC1_ADMIN_BFF}" "${OUT_DIR}/real-admin-path-param-flow-control.diff" "POST" "/admin/api/bff-web/live/activity/:id/start"

write_real_file_diff \
  "${SC1_ADMIN_BFF}" \
  "service/mc/conversation_service.go" \
  "	inputKey := sc_redisx.BuildKey(sc_redisx.CONVERSATION_KEY, INPUT.String(), params.MerchantId, params.ConversationId)" \
  "	inputKey := sc_redisx.BuildKey(sc_redisx.CONVERSATION_KEY, INPUT.String(), params.MerchantId, params.ConversationId) // smoke static map interface dispatch" \
  "${OUT_DIR}/real-admin-conversation-action-map.diff"
run_real_impact_case "real-admin-conversation-action-map" "${SC1_ADMIN_BFF}" "${OUT_DIR}/real-admin-conversation-action-map.diff" "POST" "/admin/api/bff-app/mc/conversation/status/report"

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

write_real_multi_file_diff \
  "${SC1_BFF}" \
  "${OUT_DIR}/real-client-multi-module-and-multi-source.diff" \
  "go.mod" \
  "	github.com/shopspring/decimal v1.3.1" \
  "	github.com/shopspring/decimal v1.4.0" \
  "go.mod" \
  "	github.com/google/uuid v1.6.0" \
  "	github.com/google/uuid v1.7.0" \
  "go.mod" \
  "	go.opentelemetry.io/otel/trace v1.36.0" \
  "	go.opentelemetry.io/otel/trace v1.37.0" \
  "controller/common/common.go" \
  "			ReleaseTime:  time.Now().UnixMilli()," \
  "			ReleaseTime:  time.Now().UTC().UnixMilli()," \
  "model/form_product.go" \
  "	if price == nil {" \
  "	if price == nil { // smoke multi-source" \
  "service/merchant.go" \
  "	merchantInfo := redis.GetOaMerchantInfo(c, merchantId)" \
  "	merchantInfo := redis.GetOaMerchantInfo(c, merchantId) // smoke multi-source"
run_real_impact_case "real-client-multi-module-and-multi-source" "${SC1_BFF}" "${OUT_DIR}/real-client-multi-module-and-multi-source.diff" "POST" "/api/bff-web/common/checkIn" "github.com/google/uuid" "upgraded"
validate_multi_module_case

write_real_file_diff \
  "${SC1_BFF}" \
  "controller/live/view/redirect.go" \
  "	resp, err := live.LiveViewClient.GetMerchantIdBySalesId(c, &live_viewer_client.GetMerchantIdReq{" \
  "	resp, err := live.LiveViewClient.GetMerchantIdBySalesId(c, &live_viewer_client.GetMerchantIdReq{ // smoke route" \
  "${OUT_DIR}/real-client-live-view.diff"
run_real_impact_case "real-client-live-view" "${SC1_BFF}" "${OUT_DIR}/real-client-live-view.diff" "GET" "/api/bff-web/live/view/:salesId/redirect"

write_real_file_diff \
  "${SC1_BFF}" \
  "remote/oa/oa.go" \
  "	if response.Status < 200 || response.Status >= 400 {" \
  "	if response.Status <= 199 || response.Status >= 400 {" \
  "${OUT_DIR}/real-client-interface-dispatch.diff"
run_real_impact_case "real-client-interface-dispatch" "${SC1_BFF}" "${OUT_DIR}/real-client-interface-dispatch.diff" "GET" "/sc1-internal/app-proxy/api/merchant/info"

write_real_file_diff \
  "${SC1_ADMIN_BFF}" \
  "pkg/auth/cache/auth_redis.go" \
  "func (r *AuthRedis) GetRedirectData(ctx context.Context, key string, oauthSid string) (*model.RedirectCacheDo, error) {
	if r.CheckNilClient() {" \
  "func (r *AuthRedis) GetRedirectData(ctx context.Context, key string, oauthSid string) (*model.RedirectCacheDo, error) {
	if r.CheckNilClient() == true {" \
  "${OUT_DIR}/real-admin-new-builtin.diff"
run_real_impact_case "real-admin-new-builtin" "${SC1_ADMIN_BFF}" "${OUT_DIR}/real-admin-new-builtin.diff" "GET" "/admin/api/bff-web/auth/oauth/callback"

write_real_file_diff \
  "${SC1_ADMIN_BFF}" \
  "service/uc/merchant_setting_code.go" \
  "	return string(m)" \
  "	return \"\" + string(m)" \
  "${OUT_DIR}/real-admin-typed-const.diff"
run_real_impact_case "real-admin-typed-const" "${SC1_ADMIN_BFF}" "${OUT_DIR}/real-admin-typed-const.diff" "POST" "/admin/api/bff-web/uc/merchant/setting/get"

write_real_file_diff \
  "${SC1_BFF}" \
  "remote/pulsar/consumer/mc/inbox.go" \
  '	Id             string  `json:"id"`' \
  '	AnalyzerProbe  string  `json:"analyzerProbe,omitempty"`
	Id             string  `json:"id"`' \
  "${OUT_DIR}/real-client-im-message.diff"
run_real_im_case \
  "real-client-im-message" \
  "${SC1_BFF}" \
  "${OUT_DIR}/real-client-im-message.diff" \
  "inbox_customer_msg" \
  "inbox_msg"

write_real_file_diff \
  "${SC1_ADMIN_BFF}" \
  "service/im/im.go" \
  '	ID                             string         `json:"id"`' \
  '	AnalyzerProbe                  string         `json:"analyzerProbe,omitempty"`
	ID                             string         `json:"id"`' \
  "${OUT_DIR}/real-admin-im-lock.diff"
run_real_im_case \
  "real-admin-im-lock" \
  "${SC1_ADMIN_BFF}" \
  "${OUT_DIR}/real-admin-im-lock.diff" \
  "POST/LOCK_INVENTORY_UPDATE"

write_real_file_diff \
  "${SC1_ADMIN_BFF}" \
  "remote/pulsar/consumer/activity_convert.go" \
  '	WinLogId     int64   `json:"winLogId"`     // 获奖记录ID' \
  '	AnalyzerProbe string  `json:"analyzerProbe,omitempty"`
	WinLogId     int64   `json:"winLogId"`     // 获奖记录ID' \
  "${OUT_DIR}/real-admin-im-voucher.diff"
run_real_im_case \
  "real-admin-im-voucher" \
  "${SC1_ADMIN_BFF}" \
  "${OUT_DIR}/real-admin-im-voucher.diff" \
  "ACTIVITY/VOUCHER_WINNER"

validate_known_real_endpoint_sets

echo "smoke outputs written to ${OUT_DIR}"
