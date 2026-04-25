# Reproducible Local and Staging Stack

This runbook is the fastest path from a fresh clone to a working ConvertChain local stack. It runs Postgres, Redis, Vault, the Go engine, the Python bot, deterministic migrations, and a smoke test that proves the sandbox trade lifecycle.

## Prerequisites

- Docker Desktop or Docker Engine with Compose v2.
- PowerShell 7+ on Windows, or POSIX `sh`, `curl`, and `python3` on Linux/macOS/WSL.
- No live provider credentials are required for the default local smoke path.

## Local Startup

From the repository root:

```powershell
Copy-Item .env.example .env
docker compose up --build
```

The stack exposes:

- Go engine: `http://localhost:9000`
- Python bot gateway: `http://localhost:8000`
- Postgres: `localhost:5433`
- Redis: `localhost:6379`
- Vault dev server: `http://localhost:8200`

`docker compose up` waits for Postgres, runs all migrations through the `migrate` service, starts Redis and Vault, then starts the Go engine and Python bot with health checks.

## Migration Runner

To run migrations deterministically against the Compose database:

```powershell
.\scripts\migrate.ps1
```

On Linux/macOS/WSL:

```sh
sh scripts/migrate.sh
```

The migration service mounts `go-engine/migrations` read-only and runs `migrate/migrate` against the Compose Postgres instance. Empty databases should migrate from `000001` through the latest migration with no manual ordering.

## Smoke Test

Start the stack first, then run:

```powershell
.\scripts\smoke_local_stack.ps1
```

On Linux/macOS/WSL:

```sh
sh scripts/smoke_local_stack.sh
```

The smoke test performs the full sandbox happy path:

- Creates a Telegram sandbox user.
- Records consent.
- Submits auto-approved Tier 1 KYC.
- Sets a transaction password.
- Adds the sandbox bank account `000000 / 0000000001`.
- Creates a USDT-to-NGN quote.
- Confirms the trade.
- Waits for deposit, conversion, and payout workers to reach `PAYOUT_COMPLETED`.

The scripts read `SERVICE_TOKEN` from the current environment first, then from the repo `.env`, and finally fall back to the local example token.

The default path uses USDT because it exercises the trade and payout lifecycle without requiring exchange credentials. BTC/ETH conversion tests should be run separately with configured testnet/sandbox provider keys.

## Staging Compose Overlay

Use the staging override when running a staging-like stack:

```powershell
docker compose -f docker-compose.yml -f docker-compose.staging.yml up --build -d
```

Staging keeps the same reproducible service graph but changes the app environment to `staging`, disables auto-KYC, and expects real staging/sandbox provider credentials to be supplied through the environment or a secrets manager.

## Environment Notes

- `.env.example` contains safe local defaults only. Do not reuse the dev service token, admin token, database password, Redis password, or webhook secrets outside local development.
- The Python bot requires non-empty WhatsApp and Telegram settings at startup. Local placeholder values satisfy validation but cannot send real messages.
- Vault runs in development mode locally. Production and long-lived staging environments must use a real Vault deployment, sealed storage, and proper auth policies.

## Troubleshooting

- If `go-engine` does not start, check `docker compose logs migrate go-engine`.
- If the smoke test stops at `PENDING_DEPOSIT`, confirm the Go engine worker logs show the deposit watcher running every 5 seconds.
- If the smoke test fails at trade confirmation with a Graph preflight error, verify `GRAPH_USE_SANDBOX=true` and that the bank account was created with bank code `000000`.
- If Python bot health checks fail, confirm the placeholder WhatsApp and Telegram env values are present or copy `python-bot/.env.example` when running outside Compose.
