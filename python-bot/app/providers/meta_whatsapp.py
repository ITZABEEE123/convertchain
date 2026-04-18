from __future__ import annotations

from typing import Any, Mapping

import httpx
import structlog

from app.middleware.webhook_validator import WebhookValidator
from app.providers.types import InboundEnvelope, OutboundMessage, SendResult

log = structlog.get_logger()

WHATSAPP_API_BASE = "https://graph.facebook.com/v21.0"


class MetaWhatsAppProvider:
    provider_name = "meta"
    channel = "whatsapp"

    def __init__(self, access_token: str, phone_number_id: str, app_secret: str):
        self._phone_number_id = phone_number_id
        self._validator = WebhookValidator(whatsapp_secret=app_secret)
        self._http = httpx.AsyncClient(
            headers={
                "Authorization": f"Bearer {access_token}",
                "Content-Type": "application/json",
            },
            timeout=httpx.Timeout(30.0),
        )

    async def verify_request(self, headers: Mapping[str, str], body: bytes) -> bool:
        signature = headers.get("x-hub-signature-256") or headers.get("X-Hub-Signature-256")
        if not signature:
            return False
        return self._validator.verify_whatsapp(body, signature)

    def parse_inbound(
        self,
        payload: dict[str, Any],
        *,
        signature_valid: bool,
    ) -> list[InboundEnvelope]:
        envelopes: list[InboundEnvelope] = []

        for entry in payload.get("entry", []):
            for change in entry.get("changes", []):
                if change.get("field") != "messages":
                    continue

                value = change.get("value", {})
                contacts = value.get("contacts", [])
                names_by_wa_id = {
                    contact.get("wa_id"): contact.get("profile", {}).get("name", "there")
                    for contact in contacts
                }

                for message in value.get("messages", []):
                    sender_id = message.get("from")
                    if not sender_id:
                        continue

                    sender_name = names_by_wa_id.get(sender_id, "there")
                    message_type = message.get("type", "text")
                    text = ""
                    media_type: str | None = None
                    media_id: str | None = None

                    if message_type == "text":
                        text = message.get("text", {}).get("body", "").strip()
                    elif message_type == "image":
                        media_type = "image"
                        media_id = message.get("image", {}).get("id")
                        text = message.get("image", {}).get("caption", "").strip()
                    elif message_type == "interactive":
                        interactive = message.get("interactive", {})
                        if interactive.get("type") == "button_reply":
                            reply = interactive.get("button_reply", {})
                            text = (reply.get("title") or reply.get("id") or "").strip()
                        elif interactive.get("type") == "list_reply":
                            reply = interactive.get("list_reply", {})
                            text = (reply.get("title") or reply.get("id") or "").strip()
                    else:
                        media_type = message_type

                    envelopes.append(
                        InboundEnvelope(
                            provider=self.provider_name,
                            channel=self.channel,
                            user_id=f"+{sender_id}" if not str(sender_id).startswith("+") else str(sender_id),
                            sender_name=sender_name,
                            message_id=message.get("id", ""),
                            text=text,
                            media_type=media_type,
                            media_id=media_id,
                            timestamp=message.get("timestamp"),
                            raw_payload=message,
                            signature_valid=signature_valid,
                        )
                    )

        return envelopes

    async def send_text(self, message: OutboundMessage) -> SendResult:
        payload = {
            "messaging_product": "whatsapp",
            "recipient_type": "individual",
            "to": message.recipient_id.lstrip("+"),
            "type": "text",
            "text": {
                "preview_url": False,
                "body": message.text,
            },
        }

        response = await self._http.post(f"{WHATSAPP_API_BASE}/{self._phone_number_id}/messages", json=payload)
        if response.is_success:
            body = response.json()
            message_id = None
            if isinstance(body.get("messages"), list) and body["messages"]:
                message_id = body["messages"][0].get("id")
            return SendResult(ok=True, provider_message_id=message_id)

        log.warning("failed to send whatsapp text", status_code=response.status_code, body=response.text[:200])
        return SendResult(ok=False, error=f"meta_whatsapp_http_{response.status_code}")

    async def send_interactive(self, message: OutboundMessage) -> SendResult:
        if not message.buttons:
            return await self.send_text(message)

        payload = {
            "messaging_product": "whatsapp",
            "recipient_type": "individual",
            "to": message.recipient_id.lstrip("+"),
            "type": "interactive",
            "interactive": {
                "type": "button",
                "body": {"text": message.text},
                "action": {
                    "buttons": [
                        {
                            "type": "reply",
                            "reply": {
                                "id": button["id"],
                                "title": button["title"],
                            },
                        }
                        for button in message.buttons[:3]
                    ]
                },
            },
        }

        response = await self._http.post(f"{WHATSAPP_API_BASE}/{self._phone_number_id}/messages", json=payload)
        if response.is_success:
            body = response.json()
            message_id = None
            if isinstance(body.get("messages"), list) and body["messages"]:
                message_id = body["messages"][0].get("id")
            return SendResult(ok=True, provider_message_id=message_id)

        log.warning("failed to send whatsapp interactive", status_code=response.status_code, body=response.text[:200])
        return SendResult(ok=False, error=f"meta_whatsapp_http_{response.status_code}")

    def supports(self, channel: str, capability: str) -> bool:
        if channel != self.channel:
            return False
        return capability in {"text", "interactive"}

    async def close(self) -> None:
        await self._http.aclose()
