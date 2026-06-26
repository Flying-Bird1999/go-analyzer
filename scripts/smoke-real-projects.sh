#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${ROOT_DIR}/.analyzer-smoke"

mkdir -p "${OUT_DIR}"

run_project() {
  local name="$1"
  local path="$2"
  local out="${OUT_DIR}/${name}.facts.json"

  echo "analyzing ${name} at ${path}"
  (cd "${ROOT_DIR}" && go run ./cmd/go-analyzer facts --project "${path}" --format json > "${out}")
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

run_project "sc1-bff-service" "../sc1-bff-service"
run_project "sc1-admin-bff" "../sc1-admin-bff"

echo "smoke outputs written to ${OUT_DIR}"
