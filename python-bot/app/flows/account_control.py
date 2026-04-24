from __future__ import annotations

from datetime import datetime, timezone

import structlog

from app.services.engine_client import EngineClient, EngineError
from app.services.session import SessionService

log = structlog.get_logger()

STEP_CONFIRM_DELETE = "CONFIRM_DELETE"
STEP_ENTER_TX_PASSWORD = "ENTER_TX_PASSWORD"
DELETE_PASSWORD_PROMPT_TTL_SECONDS = 300


class AccountControlFlow:
    def __init__(
        self,
        session_service: SessionService,
        engine_client: EngineClient,
        channel: str,
    ):
        self._session = session_service
        self._engine = engine_client
        self._channel = channel

    async def start(self, user_id: str, session: dict) -> str:
        engine_user_id = session.get("engine_user_id")
        if not engine_user_id:
            return "Account error. Type *hi* to refresh your session."

        try:
            quota = await self._engine.get_deletion_quota(engine_user_id)
        except EngineError as exc:
            log.error("Failed to fetch deletion quota", error=str(exc), user_id=user_id[:6])
            return "Could not load your deletion quota right now. Please try again."

        remaining = int(quota.get("remaining_deletions", 0) or 0)
        if remaining <= 0:
            return (
                "You have reached the account deletion limit for the current 7-day window.\n\n"
                "Please wait before trying again."
            )

        session["flow"] = "account_control"
        session["step"] = STEP_CONFIRM_DELETE
        session["account_control"] = {"remaining_deletions": remaining}
        await self._session.set(user_id, session)

        return (
            "*Delete Account*\n\n"
            "This will deactivate your account and anonymize your personal data.\n"
            "Financial records are retained for audit and compliance.\n\n"
            f"Remaining deletions this 7-day window: *{remaining}*\n\n"
            "Type *DELETE* to continue, or *cancel* to stop."
        )

    async def handle_step(self, user_id: str, session: dict, text: str) -> str:
        step = session.get("step")
        if step == STEP_CONFIRM_DELETE:
            return await self._handle_confirm_delete(user_id, session, text)
        if step == STEP_ENTER_TX_PASSWORD:
            return await self._handle_transaction_password(user_id, session, text)
        return "Unexpected account control state. Type *delete account* to start again."

    async def _handle_confirm_delete(self, user_id: str, session: dict, text: str) -> str:
        if text.strip().upper() != "DELETE":
            return "Type *DELETE* to confirm account deletion, or *cancel* to stop."

        session["step"] = STEP_ENTER_TX_PASSWORD
        session.setdefault("account_control", {})["password_prompted_at"] = datetime.now(timezone.utc).isoformat()
        await self._session.set(user_id, session)

        return (
            "Enter your transaction password to authorize account deletion.\n\n"
            "This prompt expires after 5 minutes of inactivity."
        )

    async def _handle_transaction_password(self, user_id: str, session: dict, text: str) -> str:
        if self._password_prompt_expired(session):
            session["step"] = STEP_CONFIRM_DELETE
            session.setdefault("account_control", {}).pop("password_prompted_at", None)
            await self._session.set(user_id, session)
            return (
                "Your deletion authorization session expired.\n\n"
                "Type *DELETE* again if you still want to continue."
            )

        engine_user_id = session.get("engine_user_id")
        try:
            await self._engine.delete_account(
                {
                    "user_id": engine_user_id,
                    "confirmation_text": "DELETE",
                    "transaction_password": text.strip(),
                }
            )
        except EngineError as exc:
            if exc.code == "TRANSACTION_PASSWORD_INVALID":
                return "That transaction password is incorrect. Please try again."
            if exc.code == "TRANSACTION_PASSWORD_LOCKED":
                await self._session.delete(user_id)
                return (
                    "Your transaction password is temporarily locked after too many failed attempts.\n\n"
                    "Please wait about 15 minutes before trying again."
                )
            if exc.code == "ACCOUNT_DELETION_BLOCKED":
                session.pop("account_control", None)
                session["flow"] = None
                session["step"] = None
                await self._session.set(user_id, session)
                details = exc.details if isinstance(exc.details, dict) else {}
                trade_ref = str(details.get("trade_ref") or details.get("trade_id") or "").strip()
                trade_status = str(details.get("status") or "").strip()
                detail_text = ""
                if trade_ref or trade_status:
                    detail_text = (
                        "\n"
                        f"Blocking trade: `{trade_ref or '-'}`\n"
                        f"Current state: *{trade_status or '-'}*\n"
                    )
                return (
                    "Your account cannot be deleted right now because you still have an active trade, payout, or dispute.\n\n"
                    f"{detail_text}"
                    "Please settle or resolve those obligations first."
                )
            if exc.code == "ACCOUNT_DELETION_QUOTA_EXCEEDED":
                session.pop("account_control", None)
                session["flow"] = None
                session["step"] = None
                await self._session.set(user_id, session)
                return "You have reached the account deletion limit for the current 7-day window."

            log.error("Failed to delete account", error=str(exc), user_id=user_id[:6])
            return "Could not delete your account right now. Please try again later."

        await self._session.delete(user_id)
        return (
            "Your account has been deleted successfully.\n\n"
            "Your personal profile data has been anonymized, and your conversation session has been cleared.\n"
            "You can register again later by sending *hi*."
        )

    @staticmethod
    def _password_prompt_expired(session: dict) -> bool:
        started_at = session.get("account_control", {}).get("password_prompted_at")
        if not started_at:
            return False

        try:
            started = datetime.fromisoformat(started_at)
        except ValueError:
            return True

        return (datetime.now(timezone.utc) - started).total_seconds() > DELETE_PASSWORD_PROMPT_TTL_SECONDS
