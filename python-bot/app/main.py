from __future__ import annotations

import time
from contextlib import asynccontextmanager
from typing import Any

import redis.asyncio as aioredis
import structlog
import uvicorn
from fastapi import Depends, FastAPI, HTTPException, Query, Request, Response, status
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse

from app.config import settings
from app.providers.meta_whatsapp import MetaWhatsAppProvider
from app.providers.openclaw_relay import OpenClawRelayProvider
from app.providers.telegram_direct import TelegramDirectProvider
from app.providers.types import OutboundMessage
from app.services.engine_client import EngineClient
from app.services.message_runtime import MessageRuntime
from app.services.openclaw_gateway import OpenClawGatewayClient
from app.services.replay_guard import ReplayGuard
from app.services.session import SessionService

structlog.configure(
    processors=[
        structlog.stdlib.add_log_level,
        structlog.processors.TimeStamper(fmt="iso"),
        structlog.dev.ConsoleRenderer()
        if not settings.is_production
        else structlog.processors.JSONRenderer(),
    ],
    wrapper_class=structlog.make_filtering_bound_logger(20),
    context_class=dict,
    logger_factory=structlog.PrintLoggerFactory(),
)

log = structlog.get_logger()
app_state: dict[str, Any] = {}


@asynccontextmanager
async def lifespan(app: FastAPI):
    log.info("ConvertChain bot starting", environment=settings.environment)

    redis_client = aioredis.from_url(
        settings.redis_url,
        encoding="utf-8",
        decode_responses=True,
        max_connections=10,
    )
    await redis_client.ping()

    session_service = SessionService(redis_client)
    engine_client = EngineClient(
        base_url=settings.engine_url_str,
        service_token=settings.service_token,
    )
    replay_guard = ReplayGuard(redis_client)
    runtime = MessageRuntime(session_service=session_service, engine_client=engine_client)

    openclaw_gateway = OpenClawGatewayClient(
        base_url=settings.openclaw_base_url_str,
        token=settings.openclaw_gateway_token,
    )

    providers = {
        "meta_whatsapp": MetaWhatsAppProvider(
            access_token=settings.whatsapp_access_token,
            phone_number_id=settings.whatsapp_phone_number_id,
            app_secret=settings.whatsapp_app_secret,
        ),
        "telegram_direct": TelegramDirectProvider(
            bot_token=settings.telegram_bot_token,
        ),
        "openclaw_telegram": OpenClawRelayProvider(
            channel="telegram",
            inbound_secret=settings.openclaw_inbound_secret,
            gateway_client=openclaw_gateway,
            outbound_enabled=bool(settings.openclaw_gateway_token and settings.telegram_uses_openclaw),
        ),
        "openclaw_whatsapp": OpenClawRelayProvider(
            channel="whatsapp",
            inbound_secret=settings.openclaw_inbound_secret,
            gateway_client=openclaw_gateway,
            outbound_enabled=bool(settings.openclaw_gateway_token and settings.whatsapp_openclaw_enabled),
        ),
    }

    app_state["redis"] = redis_client
    app_state["session"] = session_service
    app_state["engine"] = engine_client
    app_state["replay_guard"] = replay_guard
    app_state["runtime"] = runtime
    app_state["providers"] = providers
    app_state["openclaw_gateway"] = openclaw_gateway

    log.info(
        "providers initialized",
        telegram_provider=settings.telegram_provider,
        whatsapp_primary_provider=settings.whatsapp_primary_provider,
        whatsapp_fallback_provider=settings.whatsapp_fallback_provider,
    )

    yield

    log.info("ConvertChain bot shutting down")
    for provider in providers.values():
        await provider.close()
    await openclaw_gateway.close()
    await engine_client.close()
    await redis_client.aclose()


app = FastAPI(
    title="ConvertChain Bot",
    description="Provider-agnostic messaging bot for crypto-to-fiat conversion",
    version="1.1.0",
    lifespan=lifespan,
)

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"] if settings.environment == "development" else ["https://dashboard.convertchain.com"],
    allow_credentials=True,
    allow_methods=["GET", "POST"],
    allow_headers=["Content-Type", "X-Service-Token", "Authorization", "X-Hub-Signature-256", "X-OpenClaw-Signature"],
)


def get_provider_registry() -> dict[str, Any]:
    return app_state["providers"]


def get_runtime() -> MessageRuntime:
    return app_state["runtime"]


def get_replay_guard() -> ReplayGuard:
    return app_state["replay_guard"]


async def _process_provider_request(
    request: Request,
    provider_key: str,
    *,
    providers: dict[str, Any],
    runtime: MessageRuntime,
    replay_guard: ReplayGuard,
) -> Response:
    provider = providers[provider_key]
    body = await request.body()
    headers = {key.lower(): value for key, value in request.headers.items()}

    is_valid = await provider.verify_request(headers, body)
    if not is_valid:
        raise HTTPException(status_code=401, detail="Invalid webhook signature or token")

    try:
        payload = await request.json()
    except Exception as exc:
        raise HTTPException(status_code=400, detail="Invalid JSON body") from exc

    envelopes = provider.parse_inbound(payload, signature_valid=is_valid)
    for envelope in envelopes:
        is_fresh = await replay_guard.claim(envelope.provider, envelope.channel, envelope.message_id)
        if not is_fresh:
            log.info("duplicate webhook skipped", provider=envelope.provider, channel=envelope.channel, message_id=envelope.message_id)
            continue

        outbound = await runtime.handle_inbound(envelope)
        if outbound is None:
            continue

        result = await _send_provider_message(provider, outbound)
        if not result.ok:
            log.warning(
                "provider send failed",
                provider=provider_key,
                channel=outbound.channel,
                recipient=outbound.recipient_id,
                error=result.error,
            )

    return Response(content="OK", media_type="text/plain")


async def _send_provider_message(provider: Any, message: OutboundMessage):
    if message.buttons:
        return await provider.send_interactive(message)
    return await provider.send_text(message)


@app.get("/health", tags=["system"])
async def health_check() -> dict:
    redis_client: aioredis.Redis = app_state["redis"]
    openclaw_gateway: OpenClawGatewayClient = app_state["openclaw_gateway"]

    try:
        await redis_client.ping()
        redis_status = "ok"
    except Exception as exc:
        redis_status = f"error: {exc}"

    openclaw_status = await openclaw_gateway.health() if settings.openclaw_enabled else {"ok": False}
    is_healthy = redis_status == "ok"

    payload = {
        "status": "healthy" if is_healthy else "degraded",
        "environment": settings.environment,
        "redis": redis_status,
        "openclaw": openclaw_status,
        "providers": {
            "telegram": settings.telegram_provider,
            "whatsapp_primary": settings.whatsapp_primary_provider,
            "whatsapp_fallback": settings.whatsapp_fallback_provider,
        },
        "timestamp": time.time(),
    }

    return JSONResponse(
        content=payload,
        status_code=status.HTTP_200_OK if is_healthy else status.HTTP_503_SERVICE_UNAVAILABLE,
    )


@app.get("/webhook/whatsapp", tags=["webhooks"])
async def whatsapp_verify(
    hub_mode: str | None = Query(default=None, alias="hub.mode"),
    hub_verify_token: str | None = Query(default=None, alias="hub.verify_token"),
    hub_challenge: str | None = Query(default=None, alias="hub.challenge"),
):
    if hub_mode != "subscribe":
        raise HTTPException(status_code=400, detail="Invalid hub.mode")
    if hub_verify_token != settings.whatsapp_verify_token:
        raise HTTPException(status_code=403, detail="Verify token mismatch")
    return Response(content=hub_challenge, media_type="text/plain")


@app.post("/webhook/whatsapp", tags=["webhooks"])
async def whatsapp_webhook(
    request: Request,
    providers: dict[str, Any] = Depends(get_provider_registry),
    runtime: MessageRuntime = Depends(get_runtime),
    replay_guard: ReplayGuard = Depends(get_replay_guard),
):
    return await _process_provider_request(
        request,
        "meta_whatsapp",
        providers=providers,
        runtime=runtime,
        replay_guard=replay_guard,
    )


@app.post("/webhook/telegram", tags=["webhooks"])
async def telegram_webhook(
    request: Request,
    providers: dict[str, Any] = Depends(get_provider_registry),
    runtime: MessageRuntime = Depends(get_runtime),
    replay_guard: ReplayGuard = Depends(get_replay_guard),
):
    return await _process_provider_request(
        request,
        "telegram_direct",
        providers=providers,
        runtime=runtime,
        replay_guard=replay_guard,
    )


@app.post("/webhook/openclaw/telegram", tags=["webhooks"])
async def openclaw_telegram_webhook(
    request: Request,
    providers: dict[str, Any] = Depends(get_provider_registry),
    runtime: MessageRuntime = Depends(get_runtime),
    replay_guard: ReplayGuard = Depends(get_replay_guard),
):
    if not settings.openclaw_inbound_secret:
        raise HTTPException(status_code=503, detail="OpenClaw inbound secret is not configured")
    return await _process_provider_request(
        request,
        "openclaw_telegram",
        providers=providers,
        runtime=runtime,
        replay_guard=replay_guard,
    )


@app.post("/webhook/openclaw/whatsapp", tags=["webhooks"])
async def openclaw_whatsapp_webhook(
    request: Request,
    providers: dict[str, Any] = Depends(get_provider_registry),
    runtime: MessageRuntime = Depends(get_runtime),
    replay_guard: ReplayGuard = Depends(get_replay_guard),
):
    if not settings.openclaw_inbound_secret:
        raise HTTPException(status_code=503, detail="OpenClaw inbound secret is not configured")
    return await _process_provider_request(
        request,
        "openclaw_whatsapp",
        providers=providers,
        runtime=runtime,
        replay_guard=replay_guard,
    )


if __name__ == "__main__":
    uvicorn.run(
        "app.main:app",
        host="0.0.0.0",
        port=8000,
        reload=settings.environment == "development",
        log_level="debug" if not settings.is_production else "info",
    )
