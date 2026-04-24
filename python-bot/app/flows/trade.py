from __future__ import annotations

import inspect
import re
from decimal import Decimal, InvalidOperation
from datetime import datetime, timezone

import structlog

from app.services.engine_client import EngineClient, EngineError
from app.services.session import SessionService

log = structlog.get_logger()

STEP_AWAITING_CONFIRMATION = "AWAITING_CONFIRMATION"
STEP_AWAITING_TRANSACTION_PASSWORD = "AWAITING_TRANSACTION_PASSWORD"
STEP_AWAITING_DEPOSIT = "AWAITING_DEPOSIT"
STEP_COMPLETED = "COMPLETED"

TX_PASSWORD_PROMPT_TTL_SECONDS = 300

SELL_PATTERN = re.compile(r"^sell\s+(\d+\.?\d*)\s*(btc|eth|usdt|usdc|bnb)$", re.IGNORECASE)

SUPPORTED_COINS = {
    "BTC": "Bitcoin",
    "ETH": "Ethereum",
    "USDT": "Tether (USDT)",
    "USDC": "USD Coin (USDC)",
    "BNB": "BNB",
}

MINIMUM_AMOUNTS = {
    "BTC": Decimal("0.0001"),
    "ETH": Decimal("0.001"),
    "USDT": Decimal("1"),
    "USDC": Decimal("1"),
    "BNB": Decimal("0.01"),
}


class TradeFlow:
    def __init__(
        self,
        session_service: SessionService,
        engine_client: EngineClient,
        channel: str,
    ):
        self._session = session_service
        self._engine = engine_client
        self._channel = channel

    async def handle_sell_intent(self, user_id: str, session: dict, text: str) -> str:
        match = SELL_PATTERN.match(text.strip())
        if not match:
            return (
                "Invalid sell command.\n\n"
                "Format: `sell [amount] [coin]`\n\n"
                "Examples:\n"
                "- `sell 0.25 BTC`\n"
                "- `sell 1 ETH`\n"
                "- `sell 100 USDT`\n\n"
                f"Supported coins: {', '.join(SUPPORTED_COINS.keys())}"
            )

        amount_str = match.group(1)
        coin = match.group(2).upper()

        try:
            amount = Decimal(amount_str)
        except InvalidOperation:
            return f"Invalid amount: `{amount_str}`. Please enter a number like `0.25`."

        if amount <= Decimal("0"):
            return "Amount must be greater than zero."

        min_amount = MINIMUM_AMOUNTS.get(coin, Decimal("0"))
        if amount < min_amount:
            return (
                f"Minimum trade amount for {coin} is *{min_amount} {coin}*.\n\n"
                f"You entered: {amount} {coin}"
            )

        engine_user_id = session.get("engine_user_id")
        if not engine_user_id:
            return "Account error. Please type *hi* to refresh your account session."

        try:
            bank_accounts = await self._engine.list_bank_accounts(engine_user_id)
        except EngineError as exc:
            log.error("Failed to fetch bank accounts", error=str(exc), user_id=user_id[:6])
            return "Could not load your bank accounts. Please try again."

        accounts = bank_accounts.get("accounts", [])
        if not accounts:
            return (
                "No bank account on file.\n\n"
                "Add one first with `add bank`, then try your trade again.\n\n"
                "Sandbox tip for local testing:\n"
                "- Bank code: `000000`\n"
                "- Account number: any 10 digits"
            )

        selected_account = await self._ensure_selected_bank_account(user_id, session, accounts)
        bank_account_id = selected_account.get("bank_account_id")

        if not bank_account_id:
            return "Could not determine which bank account to use. Type `banks` and choose one with `use bank 1`."

        log.info("Fetching quote", user_id=user_id[:6], coin=coin, amount=str(amount), bank_account_id=bank_account_id)

        try:
            quote = await self._engine.get_quote(
                {
                    "user_id": engine_user_id,
                    "asset": coin,
                    "amount": str(amount),
                    "direction": "sell",
                }
            )
        except EngineError as exc:
            log.error("Quote fetch failed", error=str(exc), coin=coin, amount=str(amount))
            return (
                "Could not get a quote right now.\n\n"
                "The market may be unavailable. Please try again in a moment."
            )

        session["flow"] = "trade"
        session["step"] = STEP_AWAITING_CONFIRMATION
        session["trade_data"] = {
            "quote_id": quote.get("quote_id"),
            "asset": coin,
            "amount": str(amount),
            "market_rate_per_unit_kobo": int(quote.get("market_rate_per_unit_kobo", 0) or 0),
            "user_rate_per_unit_kobo": int(quote.get("user_rate_per_unit_kobo", 0) or 0),
            "gross_naira_kobo": int(quote.get("gross_naira_kobo", 0) or 0),
            "net_naira_kobo": int(quote.get("net_naira_kobo", 0)),
            "platform_fee_kobo": int(quote.get("platform_fee_kobo", quote.get("fee_kobo", 0)) or 0),
            "platform_fee_bps": int(quote.get("platform_fee_bps", 0) or 0),
            "pricing_mode": str(quote.get("pricing_mode") or "sandbox_fallback"),
            "price_source": str(quote.get("price_source") or "fallback"),
            "fiat_rate_source": str(quote.get("fiat_rate_source") or "fallback"),
            "expires_at": quote.get("expires_at", ""),
            "bank_account_id": bank_account_id,
            "bank_name": selected_account.get("bank_name") or "Bank",
            "account_number": selected_account.get("account_number") or "",
            "account_name": selected_account.get("account_name") or "",
            "trade_id": None,
            "deposit_address": None,
            "deposit_mode": None,
        }
        await self._session.set(user_id, session)

        market_rate = self._kobo_to_naira_str(int(quote.get("market_rate_per_unit_kobo", 0) or 0))
        user_rate = self._kobo_to_naira_str(int(quote.get("user_rate_per_unit_kobo", 0) or 0))
        gross_naira = self._kobo_to_naira_str(int(quote.get("gross_naira_kobo", 0) or 0))
        net_naira = self._kobo_to_naira_str(int(quote.get("net_naira_kobo", 0)))
        fee_naira = self._kobo_to_naira_str(int(quote.get("platform_fee_kobo", quote.get("fee_kobo", 0)) or 0))
        fee_bps = int(quote.get("platform_fee_bps", 0) or 0)

        bank_name = selected_account.get("bank_name") or "your bank"
        account_number = selected_account.get("account_number") or ""
        account_suffix = account_number[-4:] if len(account_number) >= 4 else account_number or "0000"
        pricing_mode = str(quote.get("pricing_mode") or "sandbox_fallback")
        if pricing_mode == "live":
            pricing_disclosure = "Pricing mode: live provider rates."
        elif pricing_mode == "sandbox_live_rates":
            pricing_disclosure = "Pricing mode: sandbox settlement with live market/rate inputs."
        else:
            pricing_disclosure = "Pricing mode: sandbox fallback pricing."

        return (
            "*Trade Quote*\n\n"
            f"You are selling: *{amount} {coin}*\n\n"
            f"Market Rate: *{market_rate} / {coin}*\n"
            f"Your Rate (after fees): *{user_rate} / {coin}*\n\n"
            "*You Receive*\n"
            f"- Gross payout: *{gross_naira}*\n"
            f"- Platform fee ({fee_bps / 100:.1f}%): {fee_naira}\n"
            f"- Total net: *{net_naira}*\n\n"
            f"Payout Bank: {bank_name} ****{account_suffix}\n"
            f"{pricing_disclosure}\n\n"
            "Type *CONFIRM* to continue.\n"
            "Type *CANCEL* to abort."
        )

    async def handle_step(self, user_id: str, session: dict, text: str) -> str:
        step = session.get("step")

        if step == STEP_AWAITING_CONFIRMATION:
            return await self._handle_confirmation(user_id, session, text)
        if step == STEP_AWAITING_TRANSACTION_PASSWORD:
            return await self._handle_transaction_password(user_id, session, text)
        if step == STEP_AWAITING_DEPOSIT:
            return await self.handle_status(user_id, session)

        return "Unexpected state. Type *cancel* to reset."

    async def _handle_confirmation(self, user_id: str, session: dict, text: str) -> str:
        text_upper = text.strip().upper()

        if text_upper in {"CANCEL", "NO"}:
            return await self.handle_cancel(user_id, session)

        if text_upper not in {"CONFIRM", "YES", "CONFIRM TRADE"}:
            return (
                "Please type *CONFIRM* to proceed with the trade, or *CANCEL* to abort.\n\n"
                "Quotes expire quickly, so confirm now if you want to keep this price."
            )

        trade_data = session.get("trade_data", {})
        trade_data["tx_password_prompted_at"] = datetime.now(timezone.utc).isoformat()
        session["step"] = STEP_AWAITING_TRANSACTION_PASSWORD
        session["trade_data"] = trade_data
        await self._session.set(user_id, session)

        return (
            "To authorize this trade, enter your transaction password.\n\n"
            "You use this password to approve payouts to your selected bank account.\n"
            "This prompt expires after 5 minutes of inactivity."
        )

    async def _handle_transaction_password(self, user_id: str, session: dict, text: str) -> str:
        trade_data = session.get("trade_data", {})
        if self._transaction_password_prompt_expired(trade_data):
            trade_data.pop("tx_password_prompted_at", None)
            session["step"] = STEP_AWAITING_CONFIRMATION
            session["trade_data"] = trade_data
            await self._session.set(user_id, session)
            return (
                "Your trade authorization session expired.\n\n"
                "Type *CONFIRM* again if you still want to continue with this quote."
            )

        engine_user_id = session.get("engine_user_id")

        try:
            trade_result = await self._engine.confirm_trade(
                {
                    "user_id": engine_user_id,
                    "quote_id": trade_data.get("quote_id"),
                    "bank_account_id": trade_data.get("bank_account_id"),
                    "transaction_password": text.strip(),
                }
            )
        except EngineError as exc:
            if exc.status_code in {409, 410}:
                session.pop("trade_data", None)
                session["step"] = None
                session["flow"] = None
                await self._session.set(user_id, session)
                return (
                    "Quote expired.\n\n"
                    f"Type `sell {trade_data.get('amount')} {trade_data.get('asset')}` to get a fresh quote."
                )
            if exc.code == "TRANSACTION_PASSWORD_INVALID":
                return (
                    "That transaction password is incorrect.\n\n"
                    "Please try again carefully. After too many failed attempts, your password will be locked temporarily."
                )
            if exc.code == "TRANSACTION_PASSWORD_LOCKED":
                session["step"] = None
                session["flow"] = None
                await self._session.set(user_id, session)
                return (
                    "Your transaction password is temporarily locked after too many failed attempts.\n\n"
                    "Please wait about 15 minutes, then start again with `sell 0.25 BTC`."
                )
            if exc.code == "TRANSACTION_PASSWORD_NOT_SET":
                session["step"] = None
                session["flow"] = None
                await self._session.set(user_id, session)
                return (
                    "You need to set a transaction password before you can confirm trades.\n\n"
                    "Type *hi* to refresh your account setup."
                )
            if exc.code == "TRADE_PREFLIGHT_FAILED":
                session.pop("trade_data", None)
                session["step"] = None
                session["flow"] = None
                await self._session.set(user_id, session)
                return self._format_trade_preflight_failure(trade_data, exc)

            log.error("Trade creation failed", error=str(exc), user_id=user_id[:6])
            return "Trade confirmation failed. Please try again."

        trade_id = trade_result.get("trade_id")
        deposit_address = trade_result.get("deposit_address", "")
        deposit_amount = trade_result.get("deposit_amount")
        asset = trade_result.get("asset") or trade_data.get("asset")
        deposit_mode = "sandbox" if str(deposit_address).startswith("sandbox://") else "chain"

        session["step"] = STEP_AWAITING_DEPOSIT
        session["trade_data"]["trade_id"] = trade_id
        session["trade_data"]["deposit_address"] = deposit_address
        session["trade_data"]["deposit_mode"] = deposit_mode
        session["trade_data"].pop("tx_password_prompted_at", None)
        await self._session.set(user_id, session)

        net_naira = self._kobo_to_naira_str(int(trade_data.get("net_naira_kobo", 0)))

        if deposit_mode == "sandbox":
            return (
                "Trade confirmed.\n\n"
                f"Trade ID: `{trade_id}`\n"
                f"Sandbox deposit reference: `{deposit_address}`\n"
                f"Expected amount: *{deposit_amount} {asset}*\n\n"
                "This local sandbox trade does not require a real blockchain transfer.\n"
                "The sandbox deposit watcher will move this trade forward automatically.\n\n"
                f"When processing completes, *{net_naira}* will be paid to your selected bank account.\n\n"
                "Type *status* to track the trade."
            )

        return (
            "Trade confirmed.\n\n"
            f"Trade ID: `{trade_id}`\n\n"
            "Send your crypto to:\n"
            f"`{deposit_address}`\n\n"
            f"Amount: *{deposit_amount} {asset}*\n\n"
            "Send exactly that amount to the deposit address above.\n"
            f"Once your deposit is confirmed, *{net_naira}* will be sent to your bank account.\n\n"
            "Type *status* to track the trade."
        )

    async def handle_status(self, user_id: str, session: dict) -> str:
        trade_data = session.get("trade_data", {})
        trade_id = trade_data.get("trade_id")
        if not trade_id:
            context_result = await self._get_trade_status_context(session)
            if not context_result or not context_result.get("trade"):
                return "You do not have an active or recent trade. Type `sell [amount] [coin]` to start one."
            if not context_result.get("has_active_trade"):
                return await self._render_recent_status_context(user_id, session, context_result)
            trade_data = await self._hydrate_trade_data_from_context(user_id, session, context_result.get("trade") or {})
            trade_id = (trade_data or {}).get("trade_id")
            if not trade_id:
                return "You do not have an active or recent trade. Type `sell [amount] [coin]` to start one."

        try:
            status_result = await self._engine.get_trade_status(trade_id)
        except EngineError as exc:
            if exc.status_code == 404:
                session.pop("trade_data", None)
                session["step"] = None
                session["flow"] = None
                await self._session.set(user_id, session)
                context_result = await self._get_trade_status_context(session)
                if context_result and context_result.get("trade"):
                    return await self._render_recent_status_context(user_id, session, context_result)
                return "You do not have an active or recent trade. Type `sell [amount] [coin]` to start one."
            log.error("Failed to get trade status", error=str(exc), trade_id=trade_id)
            return f"Could not fetch status for trade `{trade_id}`. Please try again."

        trade_status = status_result.get("status") or "unknown"
        raw_status = str(status_result.get("raw_status") or trade_status).upper()
        confirmations = int(status_result.get("confirmations", 0) or 0)
        required = int(status_result.get("required_confirmations", 2) or 2)
        asset = trade_data.get("asset", "crypto")
        net_naira = self._kobo_to_naira_str(int(trade_data.get("net_naira_kobo", 0)))
        deposit_mode = trade_data.get("deposit_mode") or "chain"
        bank_name = status_result.get("bank_name") or trade_data.get("bank_name") or "Bank"
        masked_account = status_result.get("masked_account_number") or trade_data.get("masked_account_number") or self._mask_account_number(
            trade_data.get("account_number", ""),
        )
        dispute_reason = str(status_result.get("dispute_reason") or "").strip()

        if trade_status == "awaiting_deposit":
            if deposit_mode == "sandbox":
                return (
                    "Waiting for sandbox deposit processing.\n\n"
                    f"Trade ID: `{trade_id}`\n"
                    f"Progress: {confirmations}/{required} confirmations\n\n"
                    "No real transfer is required in local sandbox mode. The watcher will progress this trade automatically.\n"
                    "Type *status* again in a few seconds."
                )
            return (
                "Waiting for your deposit.\n\n"
                f"Trade ID: `{trade_id}`\n"
                f"Progress: {confirmations}/{required} confirmations\n\n"
                "Please send your crypto to the deposit address provided earlier.\n"
                "Type *status* again to refresh."
            )

        if trade_status == "deposit_detected":
            return (
                "Deposit detected.\n\n"
                f"Trade ID: `{trade_id}`\n"
                f"Progress: {confirmations}/{required} confirmations\n\n"
                "Your trade is moving through confirmation now."
            )

        if trade_status == "deposit_confirmed":
            return (
                "Deposit confirmed.\n\n"
                f"Trade ID: `{trade_id}`\n"
                f"Blockchain progress: {confirmations}/{required} confirmations\n\n"
                "Your deposit is fully confirmed and conversion will start now."
            )

        if trade_status == "conversion_in_progress":
            return (
                "Conversion in progress.\n\n"
                f"Trade ID: `{trade_id}`\n\n"
                f"Your {asset} is currently being converted for payout."
            )

        if trade_status == "conversion_completed":
            return (
                "Conversion completed.\n\n"
                f"Trade ID: `{trade_id}`\n"
                f"Net payout ready: *{net_naira}*\n"
                f"Payout destination: {bank_name} {masked_account}\n\n"
                "We are preparing your bank payout."
            )

        if trade_status in {"payout_processing", "confirming"}:
            return (
                "Payout processing.\n\n"
                f"Trade ID: `{trade_id}`\n"
                f"Destination: {bank_name} {masked_account}\n\n"
                f"Your payout of *{net_naira}* is being finalized."
            )

        if trade_status == "settled":
            receipt_text = ""
            try:
                receipt = await self._engine.get_trade_receipt(trade_id)
            except EngineError as exc:
                log.warning("Failed to fetch trade receipt", error=str(exc), trade_id=trade_id)
            else:
                receipt_text = self._format_receipt(receipt)

            session.pop("trade_data", None)
            session["step"] = None
            session["flow"] = None
            await self._session.set(user_id, session)
            base = (
                "Payment complete.\n\n"
                f"Trade ID: `{trade_id}`\n"
                f"Amount sent: *{net_naira}*\n\n"
                "Your payout has been completed successfully.\n"
                "Type `sell 0.25 BTC` to start another trade."
            )
            if receipt_text:
                return f"{base}\n\n{receipt_text}"
            return base

        if trade_status == "failed":
            session.pop("trade_data", None)
            session["step"] = None
            session["flow"] = None
            await self._session.set(user_id, session)
            failure_reason = dispute_reason or str(status_result.get("reason") or "").strip()
            title = "Payout failed." if raw_status == "PAYOUT_FAILED" else "Trade failed."
            detail_text = ""
            if failure_reason:
                detail_text = f"Reason: {failure_reason}\n\n"
            return (
                f"{title}\n\n"
                f"Trade ID: `{trade_id}`\n\n"
                f"{detail_text}"
                "This trade could not be completed. Type `sell [amount] [coin]` to start a new one."
            )

        if trade_status == "needs_attention":
            context_result = await self._get_trade_status_context(session)
            dispute = (context_result or {}).get("dispute") or {}
            ticket_line = ""
            if dispute.get("ticket_ref"):
                ticket_line = f"Ticket: `{dispute.get('ticket_ref')}`\n"
            detail_text = ""
            if dispute_reason:
                detail_text = f"Reason: {dispute_reason}\n\n"
            return (
                "Trade requires manual review.\n\n"
                f"Trade ID: `{trade_id}`\n"
                f"Current engine state: *{raw_status}*\n\n"
                f"{ticket_line}"
                f"{detail_text}"
                "This trade is in dispute and is still blocking account deletion until it is resolved."
            )

        return f"Trade `{trade_id}` status: *{trade_status}*. Type *status* again to refresh."

    async def handle_cancel(self, user_id: str, session: dict) -> str:
        trade_data = session.get("trade_data", {})
        asset = trade_data.get("asset", "")
        amount = trade_data.get("amount", "")

        session.pop("trade_data", None)
        session["step"] = None
        session["flow"] = None
        await self._session.set(user_id, session)

        if amount and asset:
            return f"Trade cancelled.\n\nType `sell {amount} {asset}` to start a new quote."
        return "Trade cancelled.\n\nType `sell [amount] [coin]` to start a new quote."

    async def _ensure_selected_bank_account(self, user_id: str, session: dict, accounts: list[dict]) -> dict:
        selected_id = session.get("selected_bank_account_id")
        if selected_id:
            for account in accounts:
                if account.get("bank_account_id") == selected_id:
                    return account

        selected = accounts[0]
        session["selected_bank_account_id"] = selected.get("bank_account_id")
        await self._session.set(user_id, session)
        return selected

    @staticmethod
    def _kobo_to_naira_str(kobo: int) -> str:
        naira = Decimal(kobo) / Decimal("100")
        return f"\u20A6{naira:,.2f}"

    @staticmethod
    def _transaction_password_prompt_expired(trade_data: dict) -> bool:
        started_at = trade_data.get("tx_password_prompted_at")
        if not started_at:
            return False

        try:
            started = datetime.fromisoformat(started_at)
        except ValueError:
            return True

        return (datetime.now(timezone.utc) - started).total_seconds() > TX_PASSWORD_PROMPT_TTL_SECONDS

    def _format_receipt(self, receipt: dict) -> str:
        payout_amount = self._kobo_to_naira_str(int(receipt.get("payout_amount_kobo", 0) or 0))
        fee_amount = self._kobo_to_naira_str(int(receipt.get("fee_amount_kobo", 0) or 0))
        bank_name = receipt.get("bank_name") or "Bank"
        masked_account = receipt.get("masked_account_number") or "******0000"
        trade_ref = receipt.get("trade_ref") or receipt.get("trade_id") or "-"
        payout_ref = receipt.get("payout_ref") or "-"
        completed_at = receipt.get("payout_completed_at") or receipt.get("created_at") or "-"
        status = receipt.get("status") or "PAYOUT_COMPLETED"

        return (
            "*Payout Receipt*\n"
            f"- Trade Ref: `{trade_ref}`\n"
            f"- Status: *{status}*\n"
            f"- Amount Sent: *{payout_amount}*\n"
            f"- Fee: {fee_amount}\n"
            f"- Bank: {bank_name} {masked_account}\n"
            f"- Payout Ref: `{payout_ref}`\n"
            f"- Completed At: {completed_at}"
        )

    @staticmethod
    def _mask_account_number(account_number: str) -> str:
        if len(account_number) <= 4:
            return account_number or "******0000"
        return f"******{account_number[-4:]}"

    async def _hydrate_latest_active_trade(self, user_id: str, session: dict) -> dict | None:
        engine_user_id = session.get("engine_user_id")
        if not engine_user_id:
            return None

        try:
            active_trade = await self._engine.get_latest_active_trade(engine_user_id)
        except EngineError as exc:
            if exc.status_code == 404:
                return None
            log.error("Failed to recover latest active trade", error=str(exc), user_id=user_id[:6], engine_user_id=engine_user_id)
            return None

        deposit_address = str(active_trade.get("deposit_address") or "")
        trade_data = dict(session.get("trade_data") or {})
        trade_data.update(
            {
                "trade_id": active_trade.get("trade_id"),
                "trade_ref": active_trade.get("trade_ref"),
                "asset": active_trade.get("asset") or trade_data.get("asset"),
                "net_naira_kobo": int(active_trade.get("net_amount_kobo", trade_data.get("net_naira_kobo", 0)) or 0),
                "bank_name": active_trade.get("bank_name") or trade_data.get("bank_name"),
                "masked_account_number": active_trade.get("masked_account_number") or trade_data.get("masked_account_number"),
                "deposit_address": deposit_address or trade_data.get("deposit_address"),
                "deposit_mode": "sandbox" if deposit_address.startswith("sandbox://") else (trade_data.get("deposit_mode") or "chain"),
            }
        )
        session["trade_data"] = trade_data
        await self._session.set(user_id, session)
        return trade_data

    async def _get_trade_status_context(self, session: dict) -> dict | None:
        engine_user_id = session.get("engine_user_id")
        if not engine_user_id:
            return None

        try:
            status_context_method = getattr(self._engine, "get_trade_status_context", None)
            if status_context_method is not None:
                context_result = status_context_method(engine_user_id)
                if inspect.isawaitable(context_result):
                    return await context_result
        except EngineError as exc:
            if exc.status_code == 404:
                return None
            log.error("Failed to fetch trade status context", engine_user_id=engine_user_id, error=str(exc))
            return None

        try:
            latest_trade_method = getattr(self._engine, "get_latest_active_trade", None)
            if latest_trade_method is None:
                return None
            active_trade = latest_trade_method(engine_user_id)
            if not inspect.isawaitable(active_trade):
                return None
            trade = await active_trade
            if not trade:
                return None
            return {
                "context_type": "active",
                "has_active_trade": True,
                "trade": trade,
            }
        except EngineError as exc:
            if exc.status_code == 404:
                return None
            log.error("Failed to recover latest active trade", engine_user_id=engine_user_id, error=str(exc))
            return None

    async def _hydrate_trade_data_from_context(self, user_id: str, session: dict, trade: dict) -> dict:
        trade_data = dict(session.get("trade_data") or {})
        deposit_address = str(trade.get("deposit_address") or "")
        trade_data.update(
            {
                "trade_id": trade.get("trade_id"),
                "trade_ref": trade.get("trade_ref"),
                "asset": trade.get("asset") or trade_data.get("asset"),
                "net_naira_kobo": int(trade.get("net_amount_kobo", trade_data.get("net_naira_kobo", 0)) or 0),
                "bank_name": trade.get("bank_name") or trade_data.get("bank_name"),
                "masked_account_number": trade.get("masked_account_number") or trade_data.get("masked_account_number"),
                "deposit_address": deposit_address or trade_data.get("deposit_address"),
                "deposit_mode": "sandbox" if deposit_address.startswith("sandbox://") else (trade_data.get("deposit_mode") or "chain"),
            }
        )
        session["trade_data"] = trade_data
        await self._session.set(user_id, session)
        return trade_data

    async def _render_recent_status_context(self, user_id: str, session: dict, context_result: dict) -> str:
        trade = context_result.get("trade") or {}
        if not trade:
            return "You do not have an active or recent trade. Type `sell [amount] [coin]` to start one."

        receipt = context_result.get("receipt") or {}
        dispute = context_result.get("dispute") or {}
        raw_status = str(trade.get("raw_status") or trade.get("status") or "").upper()
        trade_id = trade.get("trade_id") or "-"
        trade_ref = trade.get("trade_ref") or trade_id

        session.pop("trade_data", None)
        session["step"] = None
        session["flow"] = None
        await self._session.set(user_id, session)

        if raw_status == "PAYOUT_COMPLETED":
            amount_sent = self._kobo_to_naira_str(int((receipt or trade).get("payout_amount_kobo") or trade.get("net_amount_kobo") or 0))
            base = (
                "Most recent trade completed successfully.\n\n"
                f"Trade ID: `{trade_id}`\n"
                f"Trade Ref: `{trade_ref}`\n"
                f"Amount sent: *{amount_sent}*"
            )
            receipt_text = self._format_receipt(receipt) if receipt else ""
            return f"{base}\n\n{receipt_text}" if receipt_text else base

        if raw_status == "DISPUTE":
            return (
                "Most recent trade requires manual review.\n\n"
                f"Trade ID: `{trade_id}`\n"
                f"Trade Ref: `{trade_ref}`\n"
                f"Ticket: `{dispute.get('ticket_ref') or '-'}`\n"
                f"Reason: {dispute.get('reason') or trade.get('dispute_reason') or 'Manual review required.'}\n\n"
                "This open dispute is still blocking account deletion."
            )

        if raw_status == "DISPUTE_CLOSED":
            note_text = str(dispute.get("resolution_note") or "").strip()
            detail_text = f"Reason: {dispute.get('reason') or trade.get('dispute_reason') or 'Closed after review.'}"
            if note_text:
                detail_text += f"\nResolution note: {note_text}"
            return (
                "Most recent trade was closed without payout.\n\n"
                f"Trade ID: `{trade_id}`\n"
                f"Trade Ref: `{trade_ref}`\n"
                f"Ticket: `{dispute.get('ticket_ref') or '-'}`\n"
                f"{detail_text}\n\n"
                "This closed dispute no longer blocks account deletion."
            )

        if raw_status == "PAYOUT_FAILED":
            return (
                "Most recent payout failed.\n\n"
                f"Trade ID: `{trade_id}`\n"
                f"Trade Ref: `{trade_ref}`\n"
                f"Reason: {trade.get('dispute_reason') or 'Provider payout failed.'}\n\n"
                "No active trade is running right now. You can retry with a new trade after fixing the provider issue."
            )

        if raw_status == "CANCELLED":
            return (
                "Most recent trade was cancelled.\n\n"
                f"Trade ID: `{trade_id}`\n"
                f"Trade Ref: `{trade_ref}`\n\n"
                "There is no active trade right now."
            )

        return (
            "Most recent trade summary.\n\n"
            f"Trade ID: `{trade_id}`\n"
            f"Trade Ref: `{trade_ref}`\n"
            f"Status: *{raw_status or trade.get('status') or 'UNKNOWN'}*"
        )

    def _format_trade_preflight_failure(self, trade_data: dict, exc: EngineError) -> str:
        details = exc.details if isinstance(exc.details, dict) else {}
        provider = str(details.get("provider") or "provider").strip()
        check = str(details.get("check") or "readiness_check").replace("_", " ").strip()
        reason = str(details.get("reason") or exc).strip()
        amount = trade_data.get("amount") or "?"
        asset = trade_data.get("asset") or "asset"

        return (
            "Trade cannot start right now.\n\n"
            f"Requested trade: *{amount} {asset}*\n"
            f"Provider: `{provider}`\n"
            f"Check: `{check}`\n"
            f"Reason: {reason}\n\n"
            "No deposit was started, so this request did not create a dispute.\n"
            "Please reduce the trade size or fix the provider sandbox/test balances first, then request a new quote."
        )
