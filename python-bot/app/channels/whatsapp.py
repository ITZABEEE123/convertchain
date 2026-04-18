# app/channels/whatsapp.py
# ============================================================
# WhatsApp Cloud API webhook handler.
# Parses Meta webhook payloads and sends replies.
# ============================================================

from __future__ import annotations

import json
from typing import Any

import httpx
import structlog

from app.flows.router import FlowRouter
from app.services.engine_client import EngineClient
from app.services.session import SessionService

log = structlog.get_logger()

# WhatsApp Cloud API base URL
# All message sends go to this endpoint
WHATSAPP_API_BASE = "https://graph.facebook.com/v21.0"


class WhatsAppHandler:
    """
    Handles incoming WhatsApp webhook events.

    Responsibilities:
    1. Parse Meta's nested webhook payload structure
    2. Extract the user's message and sender info
    3. Delegate to FlowRouter for business logic
    4. Send the response back to the user via WhatsApp Cloud API
    """

    def __init__(
        self,
        session_service: SessionService,
        engine_client: EngineClient,
        access_token: str,
        phone_number_id: str,
    ):
        self._session = session_service
        self._engine = engine_client
        self._access_token = access_token
        self._phone_number_id = phone_number_id
        self._router = FlowRouter(session_service=session_service, engine_client=engine_client)

        # Persistent httpx client for sending WhatsApp messages.
        # Authorization: Bearer <token> is Meta's OAuth2 authentication.
        self._http = httpx.AsyncClient(
            headers={
                "Authorization": f"Bearer {access_token}",
                "Content-Type": "application/json",
            },
            timeout=httpx.Timeout(30.0),
        )

    async def handle(self, payload: dict[str, Any]) -> None:
        """
        Main entry point for all WhatsApp webhook events.

        Meta sends many event types, not just messages:
        - Message received (text, image, voice, video, document)
        - Message status updates (sent, delivered, read)
        - Account alerts
        - Template message status

        We only process incoming messages. Status updates are acknowledged
        but not acted upon (for now).
        """
        # Safely drill into the nested structure.
        # .get() returns None instead of raising KeyError if a key doesn't exist.
        # This is defensive programming — malformed payloads won't crash the bot.
        entries = payload.get("entry", [])

        for entry in entries:
            changes = entry.get("changes", [])
            for change in changes:
                value = change.get("value", {})

                # Check this change is about messages (not status updates, etc.)
                if change.get("field") != "messages":
                    continue

                # Process each message in this change
                messages = value.get("messages", [])
                for message in messages:
                    await self._process_message(value, message)

                # Process status updates (mark as read, track delivery)
                statuses = value.get("statuses", [])
                for status in statuses:
                    await self._process_status(status)

    async def _process_message(
        self, value: dict, message: dict
    ) -> None:
        """
        Process a single WhatsApp message.

        Args:
            value: The `value` object from the change (contains contacts + messages)
            message: A single message object from `value.messages[]`
        """
        # ── Extract sender info ────────────────────────────────────────────
        sender_phone = message.get("from")  # e.g., "2348012345678" (without +)
        if not sender_phone:
            log.warning("Message has no sender phone", message=message)
            return

        # WhatsApp sends phone numbers without the leading +
        # We normalize to E.164 format (with +) for consistency
        if not sender_phone.startswith("+"):
            sender_phone = f"+{sender_phone}"

        # Get the sender's display name from the contacts array
        contacts = value.get("contacts", [])
        sender_name = "there"
        for contact in contacts:
            if contact.get("wa_id") == sender_phone.lstrip("+"):
                sender_name = contact.get("profile", {}).get("name", "there")
                break

        message_id = message.get("id")
        message_type = message.get("type")

        log.info(
            "WhatsApp message received",
            sender=sender_phone,
            message_type=message_type,
            message_id=message_id,
        )

        # Mark the message as read (shows blue ticks to the user)
        # This is best-effort — failure here shouldn't break message processing.
        try:
            await self._mark_read(message_id)
        except Exception as e:
            log.warning("Failed to mark message as read", error=str(e))

        # ── Extract message content based on type ──────────────────────────
        text_body = None
        image_data = None

        if message_type == "text":
            # Simple text message: {"type": "text", "text": {"body": "Hello"}}
            text_body = message.get("text", {}).get("body", "").strip()

        elif message_type == "image":
            # Image message: {"type": "image", "image": {"id": "...", "mime_type": "image/jpeg"}}
            # The actual image bytes are fetched separately using the image ID.
            image_data = message.get("image", {})
            text_body = image_data.get("caption", "")  # Optional caption
            log.info("Image received", image_id=image_data.get("id"))

        elif message_type == "interactive":
            # Button or list reply:
            # {"type": "interactive", "interactive": {"type": "button_reply", "button_reply": {"id": "...", "title": "YES"}}}
            interactive = message.get("interactive", {})
            if interactive.get("type") == "button_reply":
                text_body = interactive.get("button_reply", {}).get("title", "")
            elif interactive.get("type") == "list_reply":
                text_body = interactive.get("list_reply", {}).get("title", "")

        elif message_type == "audio":
            # Voice note — not currently supported
            await self.send_text(sender_phone, "Sorry, I can't process voice messages yet. Please type your message.")
            return

        elif message_type in ("video", "document", "sticker", "location", "contacts"):
            # Other unsupported types
            await self.send_text(sender_phone, "Sorry, I can only process text and images right now.")
            return

        # ── Route to flow logic ────────────────────────────────────────────
        # The FlowRouter determines what to do and returns a response string.
        try:
            response_text = await self._router.route(
                channel="whatsapp",
                user_id=sender_phone,
                message_text=text_body or "",
                image_id=image_data.get("id") if image_data else None,
                sender_name=sender_name,
            )
        except Exception as e:
            log.error("Flow routing error", error=str(e), exc_info=True, user_id=sender_phone)
            response_text = (
                "⚠️ Something went wrong on our end. Please try again in a moment.\n"
                "If this keeps happening, contact support."
            )

        # ── Send the response ──────────────────────────────────────────────
        if response_text:
            await self.send_text(sender_phone, response_text)

    async def _process_status(self, status: dict) -> None:
        """
        Handle message status updates (sent → delivered → read).
        Currently just logs — can be extended to update trade status display.
        """
        log.debug(
            "WhatsApp status update",
            message_id=status.get("id"),
            status=status.get("status"),
            recipient=status.get("recipient_id"),
        )

    async def send_text(self, to: str, text: str) -> None:
        """
        Send a text message to a WhatsApp user.

        Args:
            to: Recipient phone number in E.164 format (+2348012345678)
               Note: WhatsApp API needs it WITHOUT the + prefix.
            text: Message text. Supports WhatsApp formatting:
                 *bold* _italic_ ~strikethrough~ ```code```

        The send URL is:
        POST https://graph.facebook.com/v21.0/{phone_number_id}/messages
        """
        # Remove the + prefix — WhatsApp API wants numbers without it
        recipient = to.lstrip("+")

        payload = {
            "messaging_product": "whatsapp",
            "recipient_type": "individual",
            "to": recipient,
            "type": "text",
            "text": {
                "preview_url": False,  # Don't show URL previews
                "body": text,
            },
        }

        url = f"{WHATSAPP_API_BASE}/{self._phone_number_id}/messages"

        try:
            response = await self._http.post(url, json=payload)

            if response.is_success:
                log.info("WhatsApp message sent", to=recipient[:6] + "****")  # Partial phone for privacy
            else:
                log.error(
                    "Failed to send WhatsApp message",
                    status_code=response.status_code,
                    response=response.text[:200],
                )

        except Exception as e:
            log.error("Exception while sending WhatsApp message", error=str(e), exc_info=True)
            raise

    async def send_interactive_buttons(
        self, to: str, body_text: str, buttons: list[dict]
    ) -> None:
        """
        Send an interactive button message.
        WhatsApp supports up to 3 buttons per message.

        Args:
            to: Recipient phone number
            body_text: Main message text
            buttons: List of button dicts:
                     [{"id": "confirm", "title": "Yes, Confirm"}, ...]

        Example usage (in a flow):
            await handler.send_interactive_buttons(
                to=user_phone,
                body_text="Sell 0.25 BTC for ₦162,762.35. Confirm?",
                buttons=[
                    {"id": "confirm_trade", "title": "✅ Confirm"},
                    {"id": "cancel_trade", "title": "❌ Cancel"},
                ]
            )
        """
        recipient = to.lstrip("+")

        payload = {
            "messaging_product": "whatsapp",
            "recipient_type": "individual",
            "to": recipient,
            "type": "interactive",
            "interactive": {
                "type": "button",
                "body": {"text": body_text},
                "action": {
                    "buttons": [
                        {"type": "reply", "reply": {"id": btn["id"], "title": btn["title"]}}
                        for btn in buttons[:3]  # Max 3 buttons
                    ]
                },
            },
        }

        url = f"{WHATSAPP_API_BASE}/{self._phone_number_id}/messages"
        response = await self._http.post(url, json=payload)

        if not response.is_success:
            log.error("Failed to send WhatsApp buttons", status_code=response.status_code)

    async def _mark_read(self, message_id: str) -> None:
        """
        Mark a received message as read (shows blue ticks in WhatsApp).
        """
        url = f"{WHATSAPP_API_BASE}/{self._phone_number_id}/messages"
        await self._http.post(url, json={
            "messaging_product": "whatsapp",
            "status": "read",
            "message_id": message_id,
        })