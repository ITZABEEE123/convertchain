from __future__ import annotations

from datetime import datetime, timezone
from typing import Any

import structlog

from app.flows.onboarding import STEP_SET_TX_PASSWORD
from app.flows.router import FlowRouter
from app.providers.types import InboundEnvelope, OutboundMessage
from app.services.engine_client import EngineClient, EngineError
from app.services.scoped_session import ScopedSessionService
from app.services.session import SessionService

log = structlog.get_logger()

SUPPORTED_MEDIA_TYPES = {None, "", "image"}


class MessageRuntime:
    def __init__(self, session_service: SessionService, engine_client: EngineClient):
        self._session = session_service
        self._engine = engine_client

    async def handle_inbound(self, envelope: InboundEnvelope) -> OutboundMessage | None:
        if envelope.media_type not in SUPPORTED_MEDIA_TYPES:
            return OutboundMessage(
                channel=envelope.channel,
                recipient_id=envelope.user_id,
                text="Sorry, I can only process text and images right now.",
                metadata={"provider": envelope.provider, "message_id": envelope.message_id},
            )

        router = FlowRouter(
            session_service=ScopedSessionService(self._session, envelope.provider, envelope.channel),
            engine_client=self._engine,
        )

        response_text = await router.route(
            channel=envelope.channel,
            user_id=envelope.user_id,
            message_text=envelope.text or "",
            image_id=envelope.media_id if envelope.media_type == "image" else None,
            sender_name=envelope.sender_name or "there",
        )

        if not response_text:
            log.debug("no response generated for inbound envelope", provider=envelope.provider, channel=envelope.channel)
            return None

        return OutboundMessage(
            channel=envelope.channel,
            recipient_id=envelope.user_id,
            text=response_text,
            metadata={"provider": envelope.provider, "message_id": envelope.message_id},
        )

    async def deliver_pending_notifications(
        self,
        *,
        provider: Any,
        channel_type: str = "TELEGRAM",
        outbound_channel: str | None = None,
    ) -> None:
        try:
            response = await self._engine.get_pending_notifications(channel=channel_type, limit=50)
        except EngineError as exc:
            log.warning("failed to fetch pending notifications", channel_type=channel_type, error=str(exc))
            return

        for notification in response.get("notifications", []):
            notification_id = str(notification.get("id", "")).strip()
            if not notification_id:
                continue
            claim_token = str(notification.get("claim_token", "") or "").strip()

            try:
                outbound = await self._build_notification_message(
                    notification,
                    provider_name=getattr(provider, "provider_name", ""),
                    outbound_channel=outbound_channel or channel_type.lower(),
                )
                if outbound is None:
                    await self._ack_notification(notification_id, delivered=True, claim_token=claim_token)
                    continue

                result = await provider.send_text(outbound)
                if result.ok:
                    await self._ack_notification(notification_id, delivered=True, claim_token=claim_token)
                else:
                    await self._ack_notification(
                        notification_id,
                        delivered=False,
                        delivery_error=result.error or "provider_send_failed",
                        claim_token=claim_token,
                    )
            except Exception as exc:
                log.warning("notification delivery failed", notification_id=notification_id, error=str(exc))
                try:
                    await self._ack_notification(
                        notification_id,
                        delivered=False,
                        delivery_error=str(exc),
                        claim_token=claim_token,
                    )
                except EngineError as ack_exc:
                    log.warning("failed to ack notification error", notification_id=notification_id, error=str(ack_exc))

    async def _ack_notification(
        self,
        notification_id: str,
        *,
        delivered: bool,
        delivery_error: str = "",
        claim_token: str = "",
    ) -> None:
        try:
            await self._engine.ack_notification(
                notification_id,
                delivered=delivered,
                delivery_error=delivery_error,
                claim_token=claim_token,
            )
        except EngineError as exc:
            if exc.code == "NOTIFICATION_CLAIM_CONFLICT":
                log.info("notification lease lost before ack", notification_id=notification_id)
                return
            raise

    async def _build_notification_message(
        self,
        notification: dict[str, Any],
        *,
        provider_name: str = "",
        outbound_channel: str,
    ) -> OutboundMessage | None:
        event_type = str(notification.get("event_type") or "").strip()
        recipient_id = str(notification.get("recipient_id") or "").strip()
        payload = notification.get("payload") or {}
        trade_ref = payload.get("trade_ref") or payload.get("trade_id") or "trade"
        trade_id = str(notification.get("trade_id") or payload.get("trade_id") or "").strip()

        text: str | None = None
        if event_type in {"kyc.verified", "KYC_VERIFIED"}:
            first_name = str(payload.get("first_name") or "there").strip() or "there"
            await self._activate_kyc_verified_session(
                recipient_id=recipient_id,
                provider_name=provider_name or "telegram_direct",
                outbound_channel=outbound_channel,
                payload=payload,
            )
            text = (
                "✅ *Step 7 of 9 — Identity Verified*\n\n"
                f"Congratulations, {first_name}! Your identity has been verified.\n\n"
                "*Step 8 of 9 — Secure Your Account*\n\n"
                "Before you can trade, set a transaction password.\n\n"
                "You will use this password whenever you confirm a trade or delete your account.\n\n"
                "Send a transaction password with at least 6 characters.\n"
                "This setup prompt expires after 5 minutes of inactivity."
            )
        elif event_type == "trade.deposit_detected":
            confirmations = int(payload.get("confirmations", 1) or 1)
            required = int(payload.get("required_confirmations", 2) or 2)
            text = (
                "Deposit received.\n\n"
                f"Trade: `{trade_ref}`\n"
                f"Confirmations: {confirmations}/{required}\n\n"
                "We have detected your deposit and are waiting for final confirmation."
            )
        elif event_type == "trade.deposit_confirmed":
            text = (
                "Deposit confirmed.\n\n"
                f"Trade: `{trade_ref}`\n\n"
                "Your deposit is fully confirmed. Conversion will begin now."
            )
        elif event_type == "trade.conversion_started":
            text = (
                "Conversion started.\n\n"
                f"Trade: `{trade_ref}`\n\n"
                "Your crypto is now being converted."
            )
        elif event_type == "trade.conversion_completed":
            text = (
                "Conversion complete.\n\n"
                f"Trade: `{trade_ref}`\n"
                f"Net payout ready: *{self._format_naira(payload.get('net_amount_kobo'))}*\n\n"
                "We are preparing your payout."
            )
        elif event_type == "trade.payout_processing":
            bank_name = payload.get("bank_name") or "Bank"
            masked_account = payload.get("masked_account_number") or "******0000"
            text = (
                "Payout processing.\n\n"
                f"Trade: `{trade_ref}`\n"
                f"Destination: {bank_name} {masked_account}\n\n"
                "Your bank transfer is being processed now."
            )
        elif event_type == "trade.payout_completed":
            receipt = None
            if trade_id:
                try:
                    receipt = await self._engine.get_trade_receipt(trade_id)
                except EngineError as exc:
                    log.warning("failed to fetch receipt for completed payout", trade_id=trade_id, error=str(exc))
            text = self._render_payout_completed(payload, receipt)
        elif event_type == "trade.payout_failed":
            bank_name = payload.get("bank_name") or "Bank"
            masked_account = payload.get("masked_account_number") or "******0000"
            text = (
                "Payout failed.\n\n"
                f"Trade: `{trade_ref}`\n"
                f"Destination: {bank_name} {masked_account}\n"
                f"Reason: {payload.get('reason') or 'Unknown error'}\n\n"
                "The team has been alerted. Please check back shortly or contact support."
            )
        elif event_type == "trade.dispute_opened":
            text = (
                "Trade moved to manual review.\n\n"
                f"Trade: `{trade_ref}`\n"
                f"Ticket: `{payload.get('ticket_ref') or payload.get('dispute_id') or '-'}`\n"
                f"Reason: {payload.get('reason') or 'Manual review required.'}\n\n"
                "This trade is paused until an admin resolves the dispute."
            )
        elif event_type == "trade.dispute_resolved":
            resolution_mode = payload.get("resolution_mode") or "resolved"
            resolution_note = payload.get("resolution_note") or ""
            resolution_summary = {
                "retry_processing": "Processing will resume automatically from the last safe stage.",
                "close_no_payout": "This trade was closed without payout and no longer blocks account deletion.",
                "force_complete": "This trade was force-completed by an admin.",
            }.get(str(resolution_mode), "The dispute was resolved.")
            text = (
                "Trade review resolved.\n\n"
                f"Trade: `{trade_ref}`\n"
                f"Ticket: `{payload.get('ticket_ref') or payload.get('dispute_id') or '-'}`\n"
                f"Resolution: `{resolution_mode}`\n"
                f"{resolution_summary}"
            )
            if resolution_note:
                text += f"\n\nAdmin note: {resolution_note}"

        if not text:
            return None

        return OutboundMessage(
            channel=outbound_channel,
            recipient_id=recipient_id,
            text=text,
            metadata={"notification_id": notification.get("id"), "event_type": event_type},
        )

    async def _activate_kyc_verified_session(
        self,
        *,
        recipient_id: str,
        provider_name: str,
        outbound_channel: str,
        payload: dict[str, Any],
    ) -> None:
        if not recipient_id:
            return
        scoped = ScopedSessionService(self._session, provider_name, outbound_channel)
        session = await scoped.get(recipient_id)
        if session.get("transaction_password_set"):
            return

        session["flow"] = "onboarding"
        session["step"] = STEP_SET_TX_PASSWORD
        session["onboarded"] = False
        session["engine_user_id"] = str(payload.get("user_id") or session.get("engine_user_id") or "")
        data = session.setdefault("data", {})
        if payload.get("first_name"):
            data["first_name"] = str(payload.get("first_name"))
        data["tx_password_started_at"] = datetime.now(timezone.utc).isoformat()
        await scoped.set(recipient_id, session)

    def _render_payout_completed(self, payload: dict[str, Any], receipt: dict[str, Any] | None) -> str:
        trade_ref = payload.get("trade_ref") or payload.get("trade_id") or "-"
        if receipt:
            bank_name = receipt.get("bank_name") or "Bank"
            masked_account = receipt.get("masked_account_number") or "******0000"
            payout_ref = receipt.get("payout_ref") or "-"
            completed_at = receipt.get("payout_completed_at") or receipt.get("created_at") or "-"
            pricing_mode = receipt.get("pricing_mode") or payload.get("pricing_mode") or "sandbox_live_rates"
            return (
                "Payout successful.\n\n"
                f"Trade Ref: `{trade_ref}`\n"
                f"Amount Sent: *{self._format_naira(receipt.get('payout_amount_kobo'))}*\n"
                f"Fee: {self._format_naira(receipt.get('fee_amount_kobo'))}\n"
                f"Bank: {bank_name} {masked_account}\n"
                f"Payout Ref: `{payout_ref}`\n"
                f"Pricing Mode: {pricing_mode}\n"
                f"Completed At: {completed_at}"
            )

        bank_name = payload.get("bank_name") or "Bank"
        masked_account = payload.get("masked_account_number") or "******0000"
        payout_ref = payload.get("payout_ref") or "-"
        return (
            "Payout successful.\n\n"
            f"Trade Ref: `{trade_ref}`\n"
            f"Amount Sent: *{self._format_naira(payload.get('net_amount_kobo'))}*\n"
            f"Bank: {bank_name} {masked_account}\n"
            f"Payout Ref: `{payout_ref}`"
        )

    @staticmethod
    def _format_naira(value: Any) -> str:
        try:
            amount = int(value or 0)
        except (TypeError, ValueError):
            amount = 0
        return f"\u20A6{amount / 100:,.2f}"
