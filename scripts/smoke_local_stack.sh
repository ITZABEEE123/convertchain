#!/usr/bin/env sh
set -eu

ENGINE_URL="${ENGINE_URL:-http://localhost:9000}"
SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
SERVICE_TOKEN="${SERVICE_TOKEN:-}"
TRANSACTION_PASSWORD="${TRANSACTION_PASSWORD:-123456}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-90}"

if [ -z "$SERVICE_TOKEN" ] && [ -f "$REPO_ROOT/.env" ]; then
  SERVICE_TOKEN="$(awk -F= '/^[[:space:]]*SERVICE_TOKEN=/ { value=substr($0, index($0, "=") + 1) } END { print value }' "$REPO_ROOT/.env" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//; s/^"//; s/"$//; s/^'\''//; s/'\''$//')"
fi
SERVICE_TOKEN="${SERVICE_TOKEN:-dev-service-token}"

api() {
  method="$1"
  path="$2"
  body="${3:-}"

  if [ -n "$body" ]; then
    curl -fsS -X "$method" "$ENGINE_URL$path" \
      -H "X-Service-Token: $SERVICE_TOKEN" \
      -H "Content-Type: application/json" \
      -d "$body"
  else
    curl -fsS -X "$method" "$ENGINE_URL$path" \
      -H "X-Service-Token: $SERVICE_TOKEN"
  fi
}

json_field() {
  field="$1"
  python3 -c 'import json, sys; data=json.load(sys.stdin); print(data.get(sys.argv[1], ""))' "$field"
}

deadline=$(( $(date +%s) + TIMEOUT_SECONDS ))
until curl -fsS "$ENGINE_URL/health" >/dev/null 2>&1; do
  if [ "$(date +%s)" -ge "$deadline" ]; then
    echo "Go engine did not become healthy at $ENGINE_URL within $TIMEOUT_SECONDS seconds." >&2
    exit 1
  fi
  sleep 2
done

stamp="$(date -u +%s)"
channel_user_id="smoke-$stamp"
consented_at="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

echo "Creating sandbox user $channel_user_id"
user_json="$(api POST /api/v1/users "{\"channel_type\":\"telegram\",\"channel_user_id\":\"$channel_user_id\",\"username\":\"local_smoke\",\"phone_number\":\"08000000001\",\"locale\":\"en-NG\"}")"
user_id="$(printf '%s' "$user_json" | json_field user_id)"

echo "Recording consent"
api POST /api/v1/consent "{\"user_id\":\"$user_id\",\"consent_version\":\"local-smoke-v1\",\"consented_at\":\"$consented_at\"}" >/dev/null

echo "Submitting auto-approved Tier 1 KYC"
kyc_json="$(api POST /api/v1/kyc/submit "{\"user_id\":\"$user_id\",\"first_name\":\"Smoke\",\"last_name\":\"Tester\",\"date_of_birth\":\"1990-01-01\",\"phone_number\":\"08000000001\",\"nin\":\"12345678901\",\"bvn\":\"10987654321\",\"tier\":\"TIER_1\"}")"
kyc_status="$(printf '%s' "$kyc_json" | json_field status)"
if [ "$kyc_status" != "APPROVED" ]; then
  echo "Expected KYC APPROVED, got '$kyc_status'. Ensure AUTO_APPROVE_KYC=true for local smoke tests." >&2
  exit 1
fi

echo "Setting transaction password"
api POST /api/v1/security/transaction-password/setup "{\"user_id\":\"$user_id\",\"transaction_password\":\"$TRANSACTION_PASSWORD\",\"confirm_password\":\"$TRANSACTION_PASSWORD\"}" >/dev/null

echo "Adding sandbox payout bank account"
bank_json="$(api POST /api/v1/bank-accounts "{\"user_id\":\"$user_id\",\"bank_code\":\"000000\",\"account_number\":\"0000000001\",\"account_name\":\"Sandbox Test Account\"}")"
bank_account_id="$(printf '%s' "$bank_json" | json_field bank_account_id)"

echo "Creating sandbox USDT quote"
quote_json="$(api POST /api/v1/quotes "{\"user_id\":\"$user_id\",\"asset\":\"USDT\",\"amount\":\"25\",\"direction\":\"sell\"}")"
quote_id="$(printf '%s' "$quote_json" | json_field quote_id)"

echo "Confirming trade"
trade_json="$(api POST /api/v1/trades/confirm "{\"user_id\":\"$user_id\",\"quote_id\":\"$quote_id\",\"bank_account_id\":\"$bank_account_id\",\"transaction_password\":\"$TRANSACTION_PASSWORD\"}")"
trade_id="$(printf '%s' "$trade_json" | json_field trade_id)"

echo "Polling trade lifecycle for payout completion"
deadline=$(( $(date +%s) + TIMEOUT_SECONDS ))
last_status=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  trade_json="$(api GET "/api/v1/trades/$trade_id")"
  last_status="$(printf '%s' "$trade_json" | json_field status)"
  echo "Trade $trade_id status: $last_status"

  if [ "$last_status" = "PAYOUT_COMPLETED" ]; then
    printf '%s' "$trade_json" | python3 -c 'import json, sys; data=json.load(sys.stdin); print(json.dumps({"user_id": data.get("user_id"), "trade_id": data.get("trade_id"), "trade_ref": data.get("trade_ref"), "final_status": data.get("status"), "net_amount_kobo": data.get("net_amount_kobo"), "payout_ref": data.get("payout_ref")}, indent=2))'
    exit 0
  fi

  case "$last_status" in
    PAYOUT_FAILED|DISPUTE|CANCELLED)
      echo "Trade reached terminal failure status '$last_status'." >&2
      exit 1
      ;;
  esac

  sleep 5
done

echo "Timed out waiting for payout completion. Last status: $last_status" >&2
exit 1
