from __future__ import annotations

import re

import structlog

from app.services.engine_client import EngineClient
from app.services.session import SessionService

log = structlog.get_logger()

GREETING_KEYWORDS = {"hi", "hello", "start", "/start", "hey", "begin", "helo", "hii"}
HELP_KEYWORDS = {"help", "menu", "options", "what can you do"}
CANCEL_KEYWORDS = {"cancel", "stop", "abort", "quit", "exit", "no"}
STATUS_KEYWORDS = {"status", "track", "where", "update", "check"}
BANKS_KEYWORDS = {"banks", "my banks"}

ADD_BANK_PATTERN = re.compile(r"^add\s+bank\b", re.IGNORECASE)
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
        from app.flows.onboarding import OnboardingFlow
        from app.flows.trade import TradeFlow

        session = await self._session.get(user_id)
        text = message_text.strip()
        text_lower = text.lower()
        current_flow = session.get("flow")
        current_step = session.get("step")

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

        if self._is_greeting(text_lower):
            if session.get("onboarded"):
                if current_flow:
                    await self._clear_transient_state(user_id, session)
                return self._main_menu(sender_name)

            flow = OnboardingFlow(self._session, self._engine, channel)
            return await flow.start(user_id=user_id, sender_name=sender_name)

        if self._is_cancel(text_lower) and current_flow:
            return await self._cancel_current(user_id, session)

        if session.get("onboarded") and self._is_help(text_lower):
            if current_flow in {"bank", "trade"}:
                await self._clear_transient_state(user_id, session)
            return self._help_menu()

        if session.get("onboarded") and self._is_bank_list(text_lower):
            flow = BankFlow(self._session, self._engine, channel)
            return await flow.show_accounts(user_id, session)

        if session.get("onboarded") and ADD_BANK_PATTERN.match(text):
            flow = BankFlow(self._session, self._engine, channel)
            return await flow.start(user_id, session)

        if session.get("onboarded") and USE_BANK_PATTERN.match(text):
            flow = BankFlow(self._session, self._engine, channel)
            return await flow.select_account(user_id, session, text)

        if current_flow == "onboarding" and current_step:
            flow = OnboardingFlow(self._session, self._engine, channel)
            return await flow.handle_step(user_id=user_id, session=session, text=text, image_id=image_id)

        if current_flow == "bank" and current_step:
            flow = BankFlow(self._session, self._engine, channel)
            return await flow.handle_step(user_id=user_id, session=session, text=text)

        if current_flow == "trade" and current_step:
            flow = TradeFlow(self._session, self._engine, channel)
            if self._is_status(text_lower):
                return await flow.handle_status(user_id=user_id, session=session)
            return await flow.handle_step(user_id=user_id, session=session, text=text)

        if text_lower.startswith("sell"):
            if not session.get("onboarded"):
                return (
                    "Welcome to *ConvertChain*!\n\n"
                    "You need to complete your account setup before trading.\n"
                    "Type *hi* to get started."
                )
            flow = TradeFlow(self._session, self._engine, channel)
            return await flow.handle_sell_intent(user_id=user_id, session=session, text=text)

        if self._is_status(text_lower):
            if not session.get("onboarded"):
                return "You do not have any active trades yet. Type *hi* to set up your account."
            flow = TradeFlow(self._session, self._engine, channel)
            return await flow.handle_status(user_id=user_id, session=session)

        if self._is_help(text_lower):
            return self._help_menu()

        if not session:
            return (
                "Welcome to *ConvertChain*!\n\n"
                "Type *hi* to set up your account and start converting crypto to Naira."
            )

        if session.get("onboarded"):
            return (
                "I did not understand that.\n\n"
                "Try one of these:\n"
                "- `add bank`\n"
                "- `banks`\n"
                "- `sell 0.25 BTC`\n"
                "- `status`\n"
                "- `help`"
            )

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

    async def _cancel_current(self, user_id: str, session: dict) -> str:
        if session.get("onboarded"):
            await self._clear_transient_state(user_id, session)
            return (
                "Current operation cancelled.\n\n"
                "You can now:\n"
                "- `add bank`\n"
                "- `banks`\n"
                "- `sell 0.25 BTC`\n"
                "- `help`"
            )

        await self._session.delete(user_id)
        return "Operation cancelled. Type *hi* whenever you want to start again."

    async def _clear_transient_state(self, user_id: str, session: dict) -> dict:
        session.pop("trade_data", None)
        session.pop("bank_data", None)
        session["flow"] = None
        session["step"] = None
        await self._session.set(user_id, session)
        return session

    def _main_menu(self, name: str) -> str:
        return (
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
            "Status\n"
            "- `status`\n\n"
            "Help\n"
            "- `help`"
        )

    def _help_menu(self) -> str:
        return (
            "*ConvertChain Help*\n\n"
            "Available commands:\n\n"
            "*Account Setup*\n"
            "`hi` or `start` - begin or refresh account setup\n\n"
            "*Banks*\n"
            "`add bank` - add a payout bank account\n"
            "`banks` - list your saved bank accounts\n"
            "`use bank 1` - choose the bank account to use for payout\n\n"
            "*Trading*\n"
            "`sell [amount] [coin]` - request a quote and start a trade\n"
            "Examples: `sell 0.25 BTC`, `sell 100 USDT`\n"
            "Supported coins: BTC, ETH, USDT, USDC, BNB\n\n"
            "*Trade Management*\n"
            "`status` - check your active trade\n"
            "`cancel` - cancel the current operation\n\n"

            "Need help? Email support@convertchain.com"
        )
