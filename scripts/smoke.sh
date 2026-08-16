#!/usr/bin/env bash
# 冒烟测试：启动 examples/tasks，跑完整生命周期，断言状态码。
# 用法: bash scripts/smoke.sh
set -euo pipefail

PORT="${PORT:-8092}"
BASE="http://localhost:${PORT}"

cd "$(dirname "$0")/.."
go build -o /tmp/tasks-smoke ./examples/tasks
/tmp/tasks-smoke >/tmp/tasks-smoke.log 2>&1 &
PID=$!
trap 'kill $PID 2>/dev/null || true' EXIT

for i in $(seq 1 50); do
  if curl -sf "${BASE}/healthz" >/dev/null 2>&1; then break; fi
  sleep 0.1
done

check() { # method path expected_code [body] [token]
  local method=$1 path=$2 want=$3 body=${4:-} token=${5:-}
  local args=(-s -o /tmp/smoke-body -w '%{http_code}' -X "$method")
  [ -n "$token" ] && args+=(-H "Authorization: Bearer ${token}")
  [ -n "$body" ] && args+=(-d "$body")
  local got
  got=$(curl "${args[@]}" "${BASE}${path}")
  if [ "$got" != "$want" ]; then
    echo "FAIL ${method} ${path}: want ${want}, got ${got} ($(cat /tmp/smoke-body))" >&2
    exit 1
  fi
  echo "ok   ${method} ${path} -> ${got}"
}

check GET  /healthz 200
check GET  /tasks 401
check GET  /tasks 200 "" dev-token
check POST /tasks 201 '{"title":"smoke"}' dev-token
check GET  /tasks/1 200 "" dev-token
check PUT  /tasks/1 200 '{"title":"smoked"}' dev-token
check POST /tasks/1/done 200 "" dev-token
check POST /tasks/1/done 409 "" dev-token
check GET  /tasks/999 404 "" dev-token
check POST /tasks 400 '{"title":""}' dev-token
check GET  /renders/text 200
check GET  /renders/xml 200
check GET  /renders/html 200
check GET  /renders/json 200
check GET  /assets/app.css 200
check GET  /app/deep/route 200
check POST /form 200 'a=1'
check GET  /whoami-raw 200
check GET  /openapi.json 200

echo "SMOKE OK"
