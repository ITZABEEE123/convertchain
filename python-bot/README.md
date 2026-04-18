# ConvertChain Python Bot

## Testing Notes

Pytest configuration is defined in `pyproject.toml`:

- `asyncio_mode = "auto"` so async tests automatically get an event loop.
- `testpaths = ["tests"]` to keep test discovery focused in the tests directory.
- `python_files = ["test_*.py"]` and `python_functions = ["test_*"]` for naming conventions.

This means you do not need `@pytest.mark.asyncio` on every async test.

## Concepts to Understand

- Unit testing: tests one function/class in isolation with mocked dependencies.
- Integration testing: tests multiple components together (for example bot + Redis), but usually not external production services.
- `pytest.fixture`: reusable setup/teardown blocks injected into tests by name.
- `AsyncMock`: mock for async functions/coroutines used with `await`.
- `MagicMock`: flexible mock for synchronous objects.
- `@pytest.mark.parametrize`: runs the same test with multiple input cases.
- `side_effect`: makes mocks raise exceptions or return custom sequences.
- Code coverage: percentage of code executed by tests. Target at least 80% for financial logic paths.
- Test isolation: each test should be independent and not rely on other tests.

## Learning Resources

- pytest documentation: https://docs.pytest.org/en/stable/
- pytest-asyncio documentation: https://pytest-asyncio.readthedocs.io/
- unittest.mock documentation: https://docs.python.org/3/library/unittest.mock.html
- Real Python pytest tutorial: https://realpython.com/pytest-python-testing/
- Test-Driven Development in Python: https://www.obeythetestinggoat.com/

## Running the Bot

### Prerequisites Checklist

Before starting the bot, verify these services are running:

```bash
# 1. Check Redis
redis-cli -h localhost -p 6379 -a DevPassword123! ping
# Expected: PONG

# 2. Check Go engine
curl http://localhost:9000/health
# Expected: {"status": "ok"} or similar

# 3. Verify .env exists
cat .env | grep -v "^#" | grep "="
```

### Start the Bot

```bash
cd /c/projects/Convert-chain/python-bot
uv run uvicorn app.main:app --host 0.0.0.0 --port 8000 --reload
```

Flag meanings:

- `app.main:app`: FastAPI app object in `app/main.py`.
- `--host 0.0.0.0`: binds all interfaces (required for local tunneling tools).
- `--port 8000`: listens on port 8000.
- `--reload`: auto-restart on file changes (development only).

Expected startup output includes lines similar to:

```text
INFO:     Will watch for changes in these directories: ['.../python-bot']
INFO:     Uvicorn running on http://0.0.0.0:8000 (Press CTRL+C to quit)
INFO:     Started reloader process [...]
INFO:     Started server process [...]
INFO:     Application startup complete.
```

## Bot Verification and Webhook Testing

### Step 2: Test the Health Endpoint

```bash
curl http://localhost:8000/health
```

Expected response:

```json
{
	"status": "healthy",
	"environment": "development",
	"redis": "ok",
	"timestamp": 1704067200.0
}
```

If Redis is down:

```json
{
	"status": "degraded",
	"redis": "error: Connection refused"
}
```

### Step 3: Test the WhatsApp Verification Endpoint

```bash
curl "http://localhost:8000/webhook/whatsapp?hub.mode=subscribe&hub.verify_token=YOUR_VERIFY_TOKEN_HERE&hub.challenge=123456789"
```

Replace `YOUR_VERIFY_TOKEN_HERE` with the value of `WHATSAPP_VERIFY_TOKEN` in your `.env` file.

Expected response: `123456789`.

If you get 403, the verify token does not match your `.env` value.

### Step 4: Test the WhatsApp Webhook with a Simulated Message

Generate a test signature:

```bash
uv run python test_webhook_sign.py
```

Copy the generated `curl` command from the script output and run it. Expected response: `OK`.

### Step 5: Test with httpie

```bash
uv run http GET localhost:8000/health

uv run http GET localhost:8000/webhook/whatsapp \
		hub.mode==subscribe \
		hub.verify_token==your-verify-token \
		hub.challenge==123456789
```

httpie is included as a dev dependency in `pyproject.toml`, so you can run it through `uv` without installing it separately.

### Step 6: Expose Bot to the Internet with ngrok

```bash
# Install ngrok
winget install ngrok

# Add your authtoken
ngrok config add-authtoken YOUR_NGROK_AUTHTOKEN

# Start the tunnel in a new terminal while the bot is running
ngrok http 8000
```

Use the ngrok public URL as your webhook endpoint when testing Meta webhooks.

Note: ngrok is an external host tool, not a Python dependency, so it is not installed by the project itself. You need to install it on your machine before this step will work.

## OpenClaw Integration Baseline

The repository now tracks an explicit OpenClaw runtime contract via environment
variables. This keeps OpenClaw reproducible even when installed globally.

Expected env keys in `.env`:

- `OPENCLAW_BASE_URL` (default local control plane: `http://127.0.0.1:18789`)
- `OPENCLAW_GATEWAY_TOKEN` (optional bearer token)
- `OPENCLAW_TELEGRAM_ENABLED` (`true`/`false`)
- `OPENCLAW_WHATSAPP_ENABLED` (`true`/`false`)
- `OPENCLAW_WHATSAPP_MODE` (`primary`, `fallback`, `disabled`)
- `OPENCLAW_TELEGRAM_MODE` (`primary`, `fallback`, `disabled`)

Recommended rollout for this project:

- Telegram: `enabled=true`, `mode=primary`
- WhatsApp: `enabled=false`, `mode=fallback` until OpenClaw WhatsApp channel
	credentials are configured and validated

The project includes a minimal OpenClaw adapter in
`app/services/openclaw_gateway.py` so OpenClaw can be treated as a managed
external runtime while the official channel credentials are being finalized.

## Vault Bootstrap For Required Go Secret Paths

The Go engine expects these Vault KV v2 paths at startup:

- `secret/convertchain/binance`
- `secret/convertchain/bybit`
- `secret/convertchain/graph`
- `secret/convertchain/smileid`
- `secret/convertchain/pii_key`
- `secret/convertchain/service_token`

Optional for full KYC orchestration:

- `secret/convertchain/sumsub`

Use one of the included scripts after `docker compose up -d vault`:

```bash
# Git Bash / WSL
bash scripts/bootstrap_vault_dev.sh
```

```powershell
# PowerShell
powershell -ExecutionPolicy Bypass -File .\scripts\bootstrap_vault_dev.ps1
```

The scripts read provider values from the repository root `.env` and populate
the required Vault paths, including a generated 32-byte `pii_key` when missing.
