#!/usr/bin/env bash
set -euo pipefail

trade_mode="${1:-warn}"
graph_mode="${2:-warn}"

trade_mode="$(printf '%s' "$trade_mode" | tr '[:upper:]' '[:lower:]' | xargs)"
graph_mode="$(printf '%s' "$graph_mode" | tr '[:upper:]' '[:lower:]' | xargs)"

case "$trade_mode" in
  allow|warn|enforce) ;;
  *)
    echo "Invalid TRADE_CREATE_ENDPOINT_MODE: '$trade_mode' (allowed: allow, warn, enforce)" >&2
    exit 1
    ;;
esac

case "$graph_mode" in
  off|warn|enforce) ;;
  *)
    echo "Invalid GRAPH_WEBHOOK_EVENT_ID_MODE: '$graph_mode' (allowed: off, warn, enforce)" >&2
    exit 1
    ;;
esac

echo "Rollout flags validated"
echo "TRADE_CREATE_ENDPOINT_MODE=$trade_mode"
echo "GRAPH_WEBHOOK_EVENT_ID_MODE=$graph_mode"

if [[ "$trade_mode" == "enforce" ]]; then
  echo "WARNING: Legacy POST /api/v1/trades endpoint will be blocked (410)."
fi

if [[ "$graph_mode" == "enforce" ]]; then
  echo "WARNING: Graph webhooks without event-id header will be rejected (400)."
fi
