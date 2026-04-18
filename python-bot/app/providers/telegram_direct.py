from __future__ import annotations

from typing import Any, Mapping

import httpx
import structlog

from app.providers.types import InboundEnvelope, OutboundMessage, SendResult

log = structlog.get_logger()

TELEGRAM_API_BASE = "https://api.telegram.org"


class TelegramDirectProvider:
    provider_name = "telegram_direct"
    channel = "telegram"

    def __init__(self, bot_token: str):
        self._api_base = f"{TELEGRAM_API_BASE}/bot{bot_token}"
        self._http = httpx.AsyncClient(timeout=httpx.Timeout(30.0))

    async def verify_request(self, headers: Mapping[str, str], body: bytes) -> bool:
        return True

    def parse_inbound(
        self,
        payload: dict[str, Any],
        *,
        signature_valid: bool,
    ) -> list[InboundEnvelope]:
        envelopes: list[InboundEnvelope] = []

        if "message" in payload:
            message = payload["message"]
            chat_id = str(message.get("chat", {}).get("id", ""))
            if chat_id:
                sender = message.get("from", {})
                sender_name = sender.get("first_name", "there")
                text = message.get("text", "").strip()
                media_type: str | None = None
                media_id: str | None = None

                if "photo" in message:
                    photos = message.get("photo", [])
                    if photos:
                        media_type = "image"
                        media_id = photos[-1].get("file_id")
                    text = message.get("caption", "").strip()
                elif any(key in message for key in ("voice", "audio", "document", "video", "sticker")):
                    media_type = next(
                        key
                        for key in ("voice", "audio", "document", "video", "sticker")
                        if key in message
                    )

                envelopes.append(
                    InboundEnvelope(
                        provider=self.provider_name,
                        channel=self.channel,
                        user_id=chat_id,
                        sender_name=sender_name,
                        message_id=str(message.get("message_id", "")),
                        text=text,
                        media_type=media_type,
                        media_id=media_id,
                        timestamp=str(message.get("date", "")) or None,
                        raw_payload=message,
                        signature_valid=signature_valid,
                    )
                )

        elif "callback_query" in payload:
            callback = payload["callback_query"]
            chat_id = str(callback.get("message", {}).get("chat", {}).get("id", ""))
            if chat_id:
                sender = callback.get("from", {})
                sender_name = sender.get("first_name", "there")
                envelopes.append(
                    InboundEnvelope(
                        provider=self.provider_name,
                        channel=self.channel,
                        user_id=chat_id,
                        sender_name=sender_name,
                        message_id=str(callback.get("id", "")),
                        text=str(callback.get("data", "")).strip(),
                        timestamp=str(callback.get("message", {}).get("date", "")) or None,
                        raw_payload=callback,
                        signature_valid=signature_valid,
                    )
                )

        return envelopes

    async def send_text(self, message: OutboundMessage) -> SendResult:
        payload = {
            "chat_id": message.recipient_id,
            "text": message.text,
            "parse_mode": "Markdown",
        }

        response = await self._http.post(f"{self._api_base}/sendMessage", json=payload)
        if not response.is_success:
            payload.pop("parse_mode", None)
            response = await self._http.post(f"{self._api_base}/sendMessage", json=payload)

        if response.is_success:
            body = response.json()
            provider_message_id = None
            if isinstance(body.get("result"), dict):
                provider_message_id = str(body["result"].get("message_id", ""))
            return SendResult(ok=True, provider_message_id=provider_message_id)

        log.warning("failed to send telegram text", status_code=response.status_code, body=response.text[:200])
        return SendResult(ok=False, error=f"telegram_http_{response.status_code}")

    async def send_interactive(self, message: OutboundMessage) -> SendResult:
        if not message.buttons:
            return await self.send_text(message)

        payload = {
            "chat_id": message.recipient_id,
            "text": message.text,
            "parse_mode": "Markdown",
            "reply_markup": {
                "inline_keyboard": [
                    [
                        {
                            "text": button["title"],
                            "callback_data": button["id"],
                        }
                    ]
                    for button in message.buttons
                ]
            },
        }

        response = await self._http.post(f"{self._api_base}/sendMessage", json=payload)
        if response.is_success:
            body = response.json()
            provider_message_id = None
            if isinstance(body.get("result"), dict):
                provider_message_id = str(body["result"].get("message_id", ""))
            return SendResult(ok=True, provider_message_id=provider_message_id)

        log.warning("failed to send telegram interactive", status_code=response.status_code, body=response.text[:200])
        return SendResult(ok=False, error=f"telegram_http_{response.status_code}")

    def supports(self, channel: str, capability: str) -> bool:
        if channel != self.channel:
            return False
        return capability in {"text", "interactive"}

    async def close(self) -> None:
        await self._http.aclose()
