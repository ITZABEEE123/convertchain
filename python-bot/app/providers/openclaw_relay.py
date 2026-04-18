from __future__ import annotations

import hashlib
import hmac
from typing import Any, Mapping

import structlog

from app.providers.types import InboundEnvelope, OutboundMessage, SendResult
from app.services.openclaw_gateway import OpenClawGatewayClient

log = structlog.get_logger()


class OpenClawRelayProvider:
    provider_name = "openclaw"

    def __init__(
        self,
        channel: str,
        inbound_secret: str | None,
        gateway_client: OpenClawGatewayClient | None = None,
        outbound_enabled: bool = False,
    ):
        self.channel = channel
        self._inbound_secret = inbound_secret
        self._gateway_client = gateway_client
        self._outbound_enabled = outbound_enabled

    async def verify_request(self, headers: Mapping[str, str], body: bytes) -> bool:
        if not self._inbound_secret:
            return False

        authorization = headers.get("authorization") or headers.get("Authorization")
        expected_bearer = f"Bearer {self._inbound_secret}"
        if authorization and hmac.compare_digest(authorization, expected_bearer):
            return True

        signature = headers.get("x-openclaw-signature") or headers.get("X-OpenClaw-Signature")
        if not signature:
            return False

        digest = hmac.new(self._inbound_secret.encode("utf-8"), body, hashlib.sha256).hexdigest()
        normalized = signature.removeprefix("sha256=")
        return hmac.compare_digest(digest, normalized)

    def parse_inbound(
        self,
        payload: dict[str, Any],
        *,
        signature_valid: bool,
    ) -> list[InboundEnvelope]:
        events = payload.get("messages") or payload.get("events")
        if not isinstance(events, list):
            events = [payload]

        envelopes: list[InboundEnvelope] = []
        for event in events:
            if not isinstance(event, dict):
                continue

            user_id = (
                event.get("user_id")
                or event.get("sender_id")
                or event.get("from")
                or event.get("sender", {}).get("id")
            )
            if not user_id:
                continue

            message_block = event.get("message", {})
            envelopes.append(
                InboundEnvelope(
                    provider=self.provider_name,
                    channel=str(event.get("channel") or self.channel),
                    user_id=str(user_id),
                    sender_name=str(
                        event.get("sender_name")
                        or event.get("sender", {}).get("name")
                        or "there"
                    ),
                    message_id=str(event.get("message_id") or event.get("id") or ""),
                    text=str(event.get("text") or message_block.get("text") or "").strip(),
                    media_type=event.get("media_type") or message_block.get("media_type"),
                    media_id=event.get("media_id") or message_block.get("media_id"),
                    timestamp=str(event.get("timestamp") or message_block.get("timestamp") or "") or None,
                    raw_payload=event,
                    signature_valid=signature_valid,
                )
            )

        return envelopes

    async def send_text(self, message: OutboundMessage) -> SendResult:
        if not self._outbound_enabled or self._gateway_client is None:
            return SendResult(
                ok=False,
                error="openclaw outbound relay contract is not configured yet",
            )

        try:
            response = await self._gateway_client.forward_inbound_event(
                {
                    "type": "outbound_message",
                    "channel": self.channel,
                    "recipient_id": message.recipient_id,
                    "text": message.text,
                    "metadata": message.metadata,
                }
            )
        except Exception as exc:
            log.warning("openclaw outbound send failed", channel=self.channel, error=str(exc))
            return SendResult(ok=False, error=str(exc))

        if response.is_success:
            return SendResult(ok=True)
        return SendResult(ok=False, error=f"openclaw_http_{response.status_code}")

    async def send_interactive(self, message: OutboundMessage) -> SendResult:
        if not self._outbound_enabled or self._gateway_client is None:
            return SendResult(
                ok=False,
                error="openclaw outbound relay contract is not configured yet",
            )

        try:
            response = await self._gateway_client.forward_inbound_event(
                {
                    "type": "outbound_message",
                    "channel": self.channel,
                    "recipient_id": message.recipient_id,
                    "text": message.text,
                    "buttons": message.buttons,
                    "metadata": message.metadata,
                }
            )
        except Exception as exc:
            log.warning("openclaw outbound interactive send failed", channel=self.channel, error=str(exc))
            return SendResult(ok=False, error=str(exc))

        if response.is_success:
            return SendResult(ok=True)
        return SendResult(ok=False, error=f"openclaw_http_{response.status_code}")

    def supports(self, channel: str, capability: str) -> bool:
        if channel != self.channel:
            return False
        if capability in {"text", "interactive"}:
            return self._outbound_enabled and self._gateway_client is not None
        return False

    async def close(self) -> None:
        return None
