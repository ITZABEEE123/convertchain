#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$ROOT_DIR/.env"
CONTAINER_NAME="convertchain-vault"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "[ERROR] Missing $ENV_FILE"
  exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "[ERROR] docker is not installed or not in PATH"
  exit 1
fi

if ! docker ps --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"; then
  echo "[ERROR] Vault container '$CONTAINER_NAME' is not running"
  echo "Run: docker compose up -d vault"
  exit 1
fi

set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a

VAULT_TOKEN="${VAULT_TOKEN:-devroot}"
VAULT_ADDR="${VAULT_ADDR:-http://127.0.0.1:8200}"
SERVICE_TOKEN="${SERVICE_TOKEN:-convertchain-dev-service-token-change-me}"
PII_KEY_HEX="${PII_KEY_HEX:-}"

if [[ -z "$PII_KEY_HEX" ]]; then
  if command -v openssl >/dev/null 2>&1; then
    PII_KEY_HEX="$(openssl rand -hex 32)"
  else
    PII_KEY_HEX="$(head -c 32 /dev/urandom | xxd -p -c 256)"
  fi
fi

run_vault() {
  docker exec \
    -e VAULT_ADDR="$VAULT_ADDR" \
    -e VAULT_TOKEN="$VAULT_TOKEN" \
    "$CONTAINER_NAME" vault "$@"
}

echo "[INFO] Writing required secrets to Vault..."
run_vault kv put secret/convertchain/binance \
  api_key="${BINANCE_API_KEY:-}" api_secret="${BINANCE_SECRET_KEY:-}"

run_vault kv put secret/convertchain/bybit \
  api_key="${BYBIT_API_KEY:-}" api_secret="${BYBIT_SECRET_KEY:-}"

run_vault kv put secret/convertchain/graph \
  api_key="${GRAPH_API_KEY:-}"

run_vault kv put secret/convertchain/smileid \
  partner_id="${SMILE_ID_PARTNER_ID:-}" api_key="${SMILE_ID_API_KEY:-}"

run_vault kv put secret/convertchain/pii_key key="$PII_KEY_HEX"
run_vault kv put secret/convertchain/service_token token="$SERVICE_TOKEN"

if [[ -n "${SUMSUB_APP_TOKEN:-}" || -n "${SUMSUB_SECRET_KEY:-}" ]]; then
  run_vault kv put secret/convertchain/sumsub \
    app_token="${SUMSUB_APP_TOKEN:-}" secret_key="${SUMSUB_SECRET_KEY:-}"
fi

echo "[INFO] Verifying required paths..."
for path in \
  convertchain/binance \
  convertchain/bybit \
  convertchain/graph \
  convertchain/smileid \
  convertchain/pii_key \
  convertchain/service_token
  do
  run_vault kv get "secret/$path" >/dev/null
  echo "  - ok: secret/$path"
done

echo "[DONE] Vault bootstrap complete."
