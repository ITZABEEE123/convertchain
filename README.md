# ConvertChain

ConvertChain is a crypto-to-fiat conversion platform with a Go financial engine and a Python bot gateway.

## Local Quick Start

```powershell
Copy-Item .env.example .env
docker compose up --build
```

In another terminal, run the sandbox happy-path smoke test:

```powershell
.\scripts\smoke_local_stack.ps1
```

For Linux/macOS/WSL:

```sh
cp .env.example .env
docker compose up --build
sh scripts/smoke_local_stack.sh
```

The local stack includes Postgres, Redis, Vault, migrations, the Go engine, and the Python bot. The smoke test creates a sandbox user and drives a USDT quote through trade confirmation and payout completion.

See [docs/local-stack.md](docs/local-stack.md) for the full local and staging runbook.
