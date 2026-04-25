#!/usr/bin/env sh
set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
COMPOSE_FILE="${1:-docker-compose.yml}"

cd "$REPO_ROOT"
docker compose -f "$COMPOSE_FILE" run --rm migrate
