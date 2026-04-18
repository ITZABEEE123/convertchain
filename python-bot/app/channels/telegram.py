# app/channels/telegram.py
# ============================================================
# Telegram Bot API webhook handler.
# Parses Telegram update payloads and sends replies.
# ============================================================

from __future__ import annotations

from typing import Any

import httpx
import structlog

from app.flows.router import FlowRouter
from app.services.engine_client import EngineClient
from app.services.session import SessionService

log = structlog.get_logger()

# Telegram Bot API base URL.
# All API calls use: https://api.telegram.org/bot{token}/{method}
TELEGRAM_API_BASE = "https://api.telegram.org"


class TelegramHandler:
    """
    Handles incoming Telegram webhook updates.

    Telegram's update structure is much simpler than WhatsApp's.
    The chat.id serves as the unique user identifier.
    """

    def __init__(
        self,
        session_service: SessionService,
        engine_client: EngineClient,
        bot_token: str,
    ):
        self._session = session_service
        self._engine = engine_client
        self._bot_token = bot_token
        self._api_base = f"{TELEGRAM_API_BASE}/bot{bot_token}"
        self._router = FlowRouter(session_service=session_service, engine_client=engine_client)

        self._http = httpx.AsyncClient(
            timeout=httpx.Timeout(30.0),
        )

    async def handle(self, payload: dict[str, Any]) -> None:
        """
        Main entry point for all Telegram webhook updates.

        Telegram sends many update types:
        - message: New message from a user
        - callback_query: User clicked an inline keyboard button
        - edited_message: User edited a message
        - channel_post, inline_query, etc.

        We handle messages and callback_queries.
        """
        # Regular message
        if "message" in payload:
            await self._process_message(payload["message"])

        # Inline keyboard button click
        elif "callback_query" in payload:
            await self._process_callback(payload["callback_query"])

        # Other update types — acknowledge but ignore
        else:
            log.debug("Telegram update type not handled", update_id=payload.get("update_id"))

    async def _process_message(self, message: dict) -> None:
        """
        Process a single Telegram message.
        """
        # ── Extract user info ──────────────────────────────────────────────
        chat_id = str(message.get("chat", {}).get("id", ""))
        if not chat_id:
            log.warning("Telegram message has no chat_id")
            return

        sender = message.get("from", {})
        sender_name = sender.get("first_name", "there")

        log.info("Telegram message received", chat_id=chat_id)

        # ── Extract content ────────────────────────────────────────────────
        text_body = None
        image_file_id = None

        if "text" in message:
            text_body = message["text"].strip()

        elif "photo" in message:
            # Photos come as an array of resolutions.
            # The LAST item is always the largest — use that.
            photos = message["photo"]
            if photos:
                largest = photos[-1]
                image_file_id = largest.get("file_id")
            # Caption (optional text with the photo)
            text_body = message.get("caption", "")

        elif "voice" in message or "audio" in message:
            await self.send_text(chat_id, "Sorry, I can't process voice messages yet. Please type your message.")
            return

        elif "document" in message or "video" in message or "sticker" in message:
            await self.send_text(chat_id, "Sorry, I can only process text and images right now.")
            return

        # ── Route to flow logic ────────────────────────────────────────────
        try:
            response_text = await self._router.route(
                channel="telegram",
                user_id=chat_id,
                message_text=text_body or "",
                image_id=image_file_id,
                sender_name=sender_name,
            )
        except Exception as e:
            log.error("Telegram routing error", error=str(e), exc_info=True, chat_id=chat_id)
            response_text = "⚠️ Something went wrong. Please try again in a moment."

        if response_text:
            await self.send_text(chat_id, response_text)

    async def _process_callback(self, callback_query: dict) -> None:
        """
        Process an inline keyboard button click.
        The `data` field contains the button's payload.
        """
        chat_id = str(callback_query.get("message", {}).get("chat", {}).get("id", ""))
        button_data = callback_query.get("data", "")
        callback_id = callback_query.get("id")

        log.info("Telegram callback", chat_id=chat_id, data=button_data)

        # Acknowledge the callback (removes loading spinner from button)
        if callback_id:
            await self._answer_callback(callback_id)

        # Treat button click as a text message
        try:
            response_text = await self._router.route(
                channel="telegram",
                user_id=chat_id,
                message_text=button_data,
                image_id=None,
                sender_name="",
            )
        except Exception as e:
            log.error("Telegram callback routing error", error=str(e), exc_info=True)
            response_text = "⚠️ Something went wrong. Please try again."

        if response_text:
            await self.send_text(chat_id, response_text)

    async def send_text(self, chat_id: str, text: str, parse_mode: str = "Markdown") -> None:
        """
        Send a text message to a Telegram user.

        Args:
            chat_id: Telegram chat ID
            text: Message text. With parse_mode="Markdown":
                 *bold* _italic_ `inline_code` ```code_block```
            parse_mode: "Markdown", "MarkdownV2", or "HTML"

        Note: Telegram Markdown is slightly different from WhatsApp.
        """
        url = f"{self._api_base}/sendMessage"

        payload = {
            "chat_id": chat_id,
            "text": text,
            "parse_mode": parse_mode,
        }

        try:
            response = await self._http.post(url, json=payload)
            if not response.is_success:
                # Telegram might reject our message if Markdown is malformed.
                # Retry without parse_mode (plain text).
                log.warning(
                    "Failed to send Telegram message with Markdown, retrying as plain text",
                    status_code=response.status_code,
                )
                payload.pop("parse_mode", None)
                response = await self._http.post(url, json=payload)

            if response.is_success:
                log.info("Telegram message sent", chat_id=chat_id[:4] + "****")
            else:
                log.error("Failed to send Telegram message", status_code=response.status_code)

        except Exception as e:
            log.error("Exception while sending Telegram message", error=str(e), exc_info=True)

    async def _answer_callback(self, callback_query_id: str) -> None:
        """Acknowledge a callback query to remove the loading state."""
        await self._http.post(
            f"{self._api_base}/answerCallbackQuery",
            json={"callback_query_id": callback_query_id},
        )