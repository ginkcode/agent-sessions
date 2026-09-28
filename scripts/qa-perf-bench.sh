#!/usr/bin/env bash
# scripts/qa-perf-bench.sh — Automated QA and performance benchmarking script
# Verifies SPEC §8 targets against real local session data.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

CLI="${REPO_ROOT}/bin/agent-sessions-cli"

echo "=== Agent Sessions MVP QA & Performance Benchmarking ==="
echo "Date: $(date -u '+%Y-%m-%d %H:%M:%S UTC')"
echo "Host: $(uname -s) $(uname -r) ($(uname -m))"

if [[ ! -x "${CLI}" ]]; then
  echo "Building CLI binary..."
  (cd "${REPO_ROOT}" && make build)
fi

echo ""
echo "--- 1. Provider Detection ---"
"${CLI}" detect || true

echo ""
echo "--- 2. Cold Full Scan Benchmark ---"
# Cold full scan across all providers
START_TIME=$(date +%s%N)
SCAN_JSON=$("${CLI}" scan --json 2>/dev/null)
END_TIME=$(date +%s%N)
SCAN_DURATION_MS=$(( (END_TIME - START_TIME) / 1000000 ))
TOTAL_SESSIONS=$(echo "${SCAN_JSON}" | jq 'length')

echo "Total sessions found: ${TOTAL_SESSIONS}"
echo "Full scan duration:   ${SCAN_DURATION_MS} ms"
if [[ "${SCAN_DURATION_MS}" -le 5000 ]]; then
  echo "  [PASS] Target < 5000 ms: Achieved ${SCAN_DURATION_MS} ms"
else
  echo "  [FAIL] Target < 5000 ms exceeded: ${SCAN_DURATION_MS} ms"
fi

echo ""
echo "--- 3. Session Breakdown by Provider ---"
echo "${SCAN_JSON}" | jq -r 'group_by(.ref.agent)[] | "\(. [0].ref.agent): \(length) sessions"'

echo ""
echo "--- 4. Largest Session Loading Benchmark ---"
# Find largest session by total message count
LARGEST_SESSION=$(echo "${SCAN_JSON}" | jq -r 'sort_by(.counts.user + .counts.assistant + .counts.toolCalls) | last')
AGENT=$(echo "${LARGEST_SESSION}" | jq -r '.ref.agent')
SESSION_ID=$(echo "${LARGEST_SESSION}" | jq -r '.ref.id')
MSG_COUNT=$(echo "${LARGEST_SESSION}" | jq -r '.counts.user + .counts.assistant + .counts.toolCalls')

echo "Largest session: agent=${AGENT} id=${SESSION_ID} total_msgs=${MSG_COUNT}"

LOAD_START=$(date +%s%N)
LOAD_JSON=$("${CLI}" show "${AGENT}" "${SESSION_ID}" --json 2>/dev/null)
LOAD_END=$(date +%s%N)
LOAD_DURATION_MS=$(( (LOAD_END - LOAD_START) / 1000000 ))
LOADED_MSGS=$(echo "${LOAD_JSON}" | jq '.messages | length')

echo "Loaded messages: ${LOADED_MSGS}"
echo "Load duration:   ${LOAD_DURATION_MS} ms"
if [[ "${LOAD_DURATION_MS}" -le 1000 ]]; then
  echo "  [PASS] Target < 1000 ms: Achieved ${LOAD_DURATION_MS} ms"
else
  echo "  [FAIL] Target < 1000 ms exceeded: ${LOAD_DURATION_MS} ms"
fi

echo ""
echo "--- 5. Diagnostics & Parse Errors ---"
"${CLI}" scan 2>&1 >/dev/null || true

echo ""
echo "=== Benchmarking Completed ==="
