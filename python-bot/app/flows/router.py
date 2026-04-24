from __future__ import annotations

import re

import structlog

from app.config import settings
from app.services.engine_client import EngineClient, EngineError
from app.services.session import SessionService

log = structlog.get_logger()

GREETING_KEYWORDS = {"hi", "hello", "start", "/start", "hey", "begin", "helo", "hii"}
HELP_KEYWORDS = {"help", "menu", "options", "what can you do"}
CANCEL_KEYWORDS = {"cancel", "stop", "abort", "quit", "exit", "no"}
STATUS_KEYWORDS = {"status", "track", "where", "update", "check"}
BANKS_KEYWORDS = {"banks", "my banks"}

ADD_BANK_PATTERN = re.compile(r"^add\s+bank\b", re.IGNORECASE)
DELETE_ACCOUNT_PATTERN = re.compile(r"^delete\s+account\b", re.IGNORECASE)
USE_BANK_PATTERN = re.compile(r"^use\s+bank\s+\d+\b", re.IGNORECASE)


class FlowRouter:
    def __init__(self, session_service: SessionService, engine_client: EngineClient):
        self._session = session_service
        self._engine = engine_client

    async def route(
        self,
        channel: str,
        user_id: str,
        message_text: str,
        image_id: str | None,
        sender_name: str,
    ) -> str:
        from app.flows.bank import BankFlow
        from app.flows.account_control import AccountControlFlow
        from app.flows.admin_ops import AdminFlow
        from app.flows.onboarding import OnboardingFlow
        from app.flows.trade import TradeFlow

        session = await self._session.get(user_id)
        session = await self._rehydrate_session_from_engine(channel=channel, user_id=user_id, session=session)
        text = message_text.strip()
        text_lower = text.lower()
        current_flow = session.get("flow")
        current_step = session.get("step")
        is_ready = bool(session.get("onboarded") and session.get("transaction_password_set"))
        is_admin_user = self._is_admin_user(channel=channel, user_id=user_id)

        log.info(
            "Routing message",
            channel=channel,
            user_id=user_id[:6] + "****",
            flow=current_flow,
            step=current_step,
            has_image=image_id is not None,
        )

        if session.get("onboarded") and current_flow == "onboarding" and current_step == "COMPLETED":
            session = await self._clear_transient_state(user_id, session)
            current_flow = None
            current_step = None

        if AdminFlow.is_admin_command(text):
            if channel != "telegram":
                return "Admin commands are available only through Telegram."
            if not is_admin_user:
                return "That command is restricted to an allowlisted admin Telegram account."
            flow = AdminFlow(self._engine)
            return await flow.handle(text=text, admin_user_id=user_id, sender_name=sender_name)

        if self._is_greeting(text_lower):
            if is_ready:
                if current_flow:
                    await self._clear_transient_state(user_id, session)
                return self._main_menu(sender_name, is_admin_user=is_admin_user)

            flow = OnboardingFlow(self._session, self._engine, channel)
            return await flow.start(user_id=user_id, sender_name=sender_name)

        if self._is_cancel(text_lower) and current_flow:
            return await self._cancel_current(user_id, session)

        if is_ready and self._is_help(text_lower):
            if current_flow:
                await self._clear_transient_state(user_id, session)
            return self._help_menu(is_admin_user=is_admin_user)

        if is_ready and self._is_bank_list(text_lower):
            flow = BankFlow(self._session, self._engine, channel)
            return await flow.show_accounts(user_id, session)

        if is_ready and ADD_BANK_PATTERN.match(text):
            flow = BankFlow(self._session, self._engine, channel)
            return await flow.start(user_id, session)

        if is_ready and DELETE_ACCOUNT_PATTERN.match(text):
            flow = AccountControlFlow(self._session, self._engine, channel)
            return await flow.start(user_id, session)

        if is_ready and USE_BANK_PATTERN.match(text):
            flow = BankFlow(self._session, self._engine, channel)
            return await flow.select_account(user_id, session, text)

        if current_flow == "onboarding" and current_step:
            flow = OnboardingFlow(self._session, self._engine, channel)
            return await flow.handle_step(user_id=user_id, session=session, text=text, image_id=image_id)

        if current_flow == "bank" and current_step:
            flow = BankFlow(self._session, self._engine, channel)
            return await flow.handle_step(user_id=user_id, session=session, text=text)

        if current_flow == "account_control" and current_step:
            flow = AccountControlFlow(self._session, self._engine, channel)
            return await flow.handle_step(user_id=user_id, session=session, text=text)

        if current_flow == "trade" and current_step:
            flow = TradeFlow(self._session, self._engine, channel)
            if self._is_status(text_lower):
                return await flow.handle_status(user_id=user_id, session=session)
            return await flow.handle_step(user_id=user_id, session=session, text=text)

        if text_lower.startswith("sell"):
            if not is_ready:
                return (
                    "Welcome to *ConvertChain*!\n\n"
                    "You need to complete your account setup and transaction password before trading.\n"
                    "Type *hi* to get started."
                )
            flow = TradeFlow(self._session, self._engine, channel)
            return await flow.handle_sell_intent(user_id=user_id, session=session, text=text)

        if self._is_status(text_lower):
            if not is_ready:
                return "You do not have any active trades yet. Type *hi* to set up your account."
            flow = TradeFlow(self._session, self._engine, channel)
            return await flow.handle_status(user_id=user_id, session=session)

        if self._is_help(text_lower):
            return self._help_menu(is_admin_user=is_admin_user)

        if not session:
            return (
                "Welcome to *ConvertChain*!\n\n"
                "Type *hi* to set up your account and start converting crypto to Naira."
            )

        if is_ready:
            response = (
                "I did not understand that.\n\n"
                "Try one of these:\n"
                "- `add bank`\n"
                "- `delete account`\n"
                "- `banks`\n"
                "- `sell 0.25 BTC`\n"
                "- `status`\n"
                "- `help`"
            )
            if is_admin_user:
                response += "\n- `admin disputes`\n- `admin readiness`"
            return response

        return "Type *hi* to continue your account setup."

    @staticmethod
    def _is_greeting(text_lower: str) -> bool:
        return text_lower in GREETING_KEYWORDS

    @staticmethod
    def _is_help(text_lower: str) -> bool:
        return text_lower in HELP_KEYWORDS

    @staticmethod
    def _is_cancel(text_lower: str) -> bool:
        return text_lower in CANCEL_KEYWORDS

    @staticmethod
    def _is_status(text_lower: str) -> bool:
        return text_lower in STATUS_KEYWORDS

    @staticmethod
    def _is_bank_list(text_lower: str) -> bool:
        return text_lower in BANKS_KEYWORDS

    @staticmethod
    def _is_admin_user(*, channel: str, user_id: str) -> bool:
        return (
            channel == "telegram"
            and bool(settings.admin_api_token)
            and user_id.strip() in settings.admin_telegram_user_id_set
        )

    async def _cancel_current(self, user_id: str, session: dict) -> str:
        if session.get("onboarded"):
            await self._clear_transient_state(user_id, session)
            return (
                "Current operation cancelled.\n\n"
                "You can now:\n"
                "- `add bank`\n"
                "- `delete account`\n"
                "- `banks`\n"
                "- `sell 0.25 BTC`\n"
                "- `help`"
            )

        await self._session.delete(user_id)
        return "Operation cancelled. Type *hi* whenever you want to start again."

    async def _clear_transient_state(self, user_id: str, session: dict) -> dict:
        session.pop("trade_data", None)
        session.pop("bank_data", None)
        session.pop("account_control", None)
        session["flow"] = None
        session["step"] = None
        await self._session.set(user_id, session)
        return session

    async def _rehydrate_session_from_engine(self, *, channel: str, user_id: str, session: dict) -> dict:
        if session.get("flow") == "onboarding" and session.get("step") not in {None, "COMPLETED"}:
            return session

        original = dict(session)
        create_result: dict | None = None
        engine_user_id = session.get("engine_user_id")

        if not engine_user_id:
            try:
                create_result = await self._engine.create_user(channel_type=channel, channel_user_id=user_id)
            except EngineError as exc:
                log.debug("failed to rehydrate session via create_user", channel=channel, user_id=user_id[:6], error=str(exc))
                return session

            engine_user_id = create_result.get("user_id") or create_result.get("id")
            if not engine_user_id:
                return session
            session["engine_user_id"] = engine_user_id

        transaction_password_set = bool(session.get("transaction_password_set"))
        if create_result is not None:
            transaction_password_set = bool(create_result.get("transaction_password_set"))

        try:
            kyc_status_result = await self._engine.get_kyc_status(engine_user_id)
        except (EngineError, TypeError, AttributeError) as exc:
            log.debug("failed to rehydrate session via get_kyc_status", engine_user_id=engine_user_id, error=str(exc))
            if create_result is not None:
                session["transaction_password_set"] = transaction_password_set
                if session != original:
                    await self._session.set(user_id, session)
            return session

        transaction_password_set = bool(kyc_status_result.get("transaction_password_set", transaction_password_set))
        session["transaction_password_set"] = transaction_password_set
        session["onboarded"] = bool(kyc_status_result.get("kyc_status") == "approved" and transaction_password_set)

        if session != original:
            await self._session.set(user_id, session)

        return session

    def _main_menu(self, name: str, *, is_admin_user: bool = False) -> str:
        response = (
            f"Welcome back, {name}!\n\n"
            "*What would you like to do?*\n\n"
            "Trade\n"
            "- `sell 0.25 BTC`\n"
            "- `sell 1 ETH`\n"
            "- `sell 100 USDT`\n\n"
            "Banks\n"
            "- `add bank`\n"
            "- `banks`\n"
            "- `use bank 1`\n\n"
            "Account\n"
            "- `delete account`\n\n"
            "Status\n"
            "- `status`\n\n"
            "Help\n"
            "- `help`"
        )
        if is_admin_user:
            response += (
                "\n\nAdmin\n"
                "- `admin disputes`\n"
                "- `admin readiness`"
            )
        return response

    def _help_menu(self, *, is_admin_user: bool = False) -> str:
        response = (
            "*ConvertChain Help*\n\n"
            "Available commands:\n\n"
            "*Account Setup*\n"
            "`hi` or `start` - begin or refresh account setup\n\n"
            "*Banks*\n"
            "`add bank` - add a payout bank account\n"
            "`banks` - list your saved bank accounts\n"
            "`use bank 1` - choose the bank account to use for payout\n\n"
            "*Account Control*\n"
            "`delete account` - anonymize and deactivate your account\n\n"
            "*Trading*\n"
            "`sell [amount] [coin]` - request a quote and start a trade\n"
            "Examples: `sell 0.25 BTC`, `sell 100 USDT`\n"
            "Supported coins: BTC, ETH, USDT, USDC, BNB\n\n"
            "*Trade Management*\n"
            "`status` - check your active trade\n"
            "`cancel` - cancel the current operation\n\n"
            "Need help? Email support@convertchain.com"
        )
        if is_admin_user:
            response += (
                "\n\n*Admin*\n"
                "`admin disputes` - list open disputes\n"
                "`admin dispute TRD-XXXXXXX` - inspect a dispute by trade or ticket\n"
                "`admin readiness` - check provider readiness\n"
                "`admin resolve TRD-XXXXXXX retry` - retry a blocked trade\n"
                "`admin resolve TRD-XXXXXXX close` - close without payout\n"
                "`admin resolve TRD-XXXXXXX force_paid` - force-complete a trade"
            )
        return response
