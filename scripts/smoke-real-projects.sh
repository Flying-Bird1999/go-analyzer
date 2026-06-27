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
print(
    "symbols={symbols} annotations={annotations} routes={routes} diagnostics={diagnostics}".format(
        symbols=len(data.get("symbols") or []),
        annotations=len(data.get("annotations") or []),
        routes=len(data.get("routes") or []),
        diagnostics=len(data.get("diagnostics") or []),
    )
)
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
  python3 - "$out" "$((SECONDS - started))" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)

if data.get("meta", {}).get("schemaVersion") != "go-impact/v1alpha1":
    raise SystemExit("unexpected impact schema version")

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
    for diagnostic in (data.get("meta", {}).get("diagnostics") or [])
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

run_project "sl-sc1-bff-service" "$(resolve_sibling "sl-sc1-bff-service" "sc1-bff-service")"
run_project "sl-sc1-admin-bff" "$(resolve_sibling "sl-sc1-admin-bff" "sc1-admin-bff")"
run_impact_fixture

echo "smoke outputs written to ${OUT_DIR}"
