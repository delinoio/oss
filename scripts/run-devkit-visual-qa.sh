#!/usr/bin/env bash

set -euo pipefail

log() {
  local level="$1"
  local message="$2"
  printf 'ts=%s level=%s component=visual-qa-runner msg="%s"\n' "$(date -Iseconds)" "$level" "$message"
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
DEVKIT_DIR="${REPO_ROOT}/apps/devkit"
ENV_FILE="${DEVKIT_DIR}/.env.visual-qa"

if [[ -f "${ENV_FILE}" ]]; then
  log "info" "loading env file ${ENV_FILE}"
  set -a
  # shellcheck disable=SC1090
  source "${ENV_FILE}"
  set +a
else
  log "warn" "${ENV_FILE} not found; using process environment only"
fi

export MIDSCENE_MODEL_BASE_URL="${MIDSCENE_MODEL_BASE_URL:-https://openrouter.ai/api/v1}"
if [[ -z "${MIDSCENE_MODEL_API_KEY:-}" && -n "${OPENROUTER_API_KEY:-}" ]]; then
  export MIDSCENE_MODEL_API_KEY="${OPENROUTER_API_KEY}"
fi
export MIDSCENE_MODEL_NAME="${MIDSCENE_MODEL_NAME:-openai/gpt-4.1-mini}"
export MIDSCENE_MODEL_FAMILY="${MIDSCENE_MODEL_FAMILY:-openai}"

missing_env=()
required_env=(
  "MIDSCENE_MODEL_BASE_URL"
  "MIDSCENE_MODEL_API_KEY"
  "MIDSCENE_MODEL_NAME"
  "MIDSCENE_MODEL_FAMILY"
)
for env_name in "${required_env[@]}"; do
  if [[ -z "${!env_name:-}" ]]; then
    missing_env+=("${env_name}")
  fi
done

if (( ${#missing_env[@]} > 0 )); then
  log "error" "missing required environment variables: ${missing_env[*]}"
  log "error" "copy apps/devkit/.env.visual-qa.example to apps/devkit/.env.visual-qa and fill in values"
  exit 1
fi

log "info" "ensuring Playwright Chromium is installed"
pnpm --filter devkit run qa:visual:install-browser

log "info" "running Midscene visual QA"
set +e
pnpm --filter devkit run qa:visual
visual_qa_exit="$?"
set -e

if ! command -v codex >/dev/null 2>&1; then
  log "warn" "codex command not found; skipping Codex summary generation"
elif [[ "${VISUAL_QA_SKIP_CODEX_SUMMARY:-0}" == "1" ]]; then
  log "info" "VISUAL_QA_SKIP_CODEX_SUMMARY=1; skipping Codex summary generation"
else
  report_dir="${DEVKIT_DIR}/playwright-report/visual-qa/codex"
  mkdir -p "${report_dir}"
  report_file="${report_dir}/summary-$(date +%Y%m%d-%H%M%S).md"
  log "info" "generating Codex summary at ${report_file}"

  if ! codex exec \
    --cd "${REPO_ROOT}" \
    --full-auto \
    --output-last-message "${report_file}" \
    "Inspect the latest visual QA artifacts under apps/devkit/playwright-report/visual-qa and apps/devkit/test-results/visual-qa.
Write a concise markdown report in Korean with:
1) pass/fail per scenario,
2) likely visual root causes,
3) prioritized fixes.
If all scenarios pass, summarize coverage and residual visual risks."; then
    log "warn" "codex summary generation failed; visual QA result is still valid"
  fi
fi

if [[ "${visual_qa_exit}" -ne 0 ]]; then
  log "error" "visual QA failed with exit code ${visual_qa_exit}"
  exit "${visual_qa_exit}"
fi

log "info" "visual QA completed successfully"
