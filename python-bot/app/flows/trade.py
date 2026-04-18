from __future__ import annotations

import re
from decimal import Decimal, InvalidOperation

import structlog

from app.services.engine_client import EngineClient, EngineError
from app.services.session import SessionService

log = structlog.get_logger()

STEP_AWAITING_CONFIRMATION = "AWAITING_CONFIRMATION"
STEP_AWAITING_DEPOSIT = "AWAITING_DEPOSIT"
STEP_COMPLETED = "COMPLETED"

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
            "rate_kobo": int(quote.get("rate", 0)),
            "net_naira_kobo": int(quote.get("net_naira_kobo", 0)),
            "fee_kobo": int(quote.get("fee_kobo", 0)),
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

        rate_naira = self._kobo_to_naira_str(int(quote.get("rate", 0)))
        net_naira = self._kobo_to_naira_str(int(quote.get("net_naira_kobo", 0)))
        fee_naira = self._kobo_to_naira_str(int(quote.get("fee_kobo", 0)))

        bank_name = selected_account.get("bank_name") or "your bank"
        account_number = selected_account.get("account_number") or ""
        account_suffix = account_number[-4:] if len(account_number) >= 4 else account_number or "0000"

        return (
            "*Trade Quote*\n\n"
            f"You sell: *{amount} {coin}*\n"
            f"Rate: *{rate_naira}/{coin}*\n"
            f"Platform fee: {fee_naira}\n"
            "---------------------\n"
            f"You receive: *{net_naira}*\n"
            f"Bank: {bank_name} ****{account_suffix}\n\n"
            "Quote expires in about 30 seconds.\n\n"
            "Type *CONFIRM* to proceed.\n"
            "Type *CANCEL* to abort."
        )

    async def handle_step(self, user_id: str, session: dict, text: str) -> str:
        step = session.get("step")

        if step == STEP_AWAITING_CONFIRMATION:
            return await self._handle_confirmation(user_id, session, text)
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
        engine_user_id = session.get("engine_user_id")

        try:
            trade_result = await self._engine.create_trade(
                {
                    "user_id": engine_user_id,
                    "quote_id": trade_data.get("quote_id"),
                    "bank_account_id": trade_data.get("bank_account_id"),
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

            log.error("Trade creation failed", error=str(exc), user_id=user_id[:6])
            return "Trade creation failed. Please try again."

        trade_id = trade_result.get("trade_id")
        deposit_address = trade_result.get("deposit_address", "")
        deposit_amount = trade_result.get("deposit_amount")
        asset = trade_result.get("asset") or trade_data.get("asset")
        deposit_mode = "sandbox" if str(deposit_address).startswith("sandbox://") else "chain"

        session["step"] = STEP_AWAITING_DEPOSIT
        session["trade_data"]["trade_id"] = trade_id
        session["trade_data"]["deposit_address"] = deposit_address
        session["trade_data"]["deposit_mode"] = deposit_mode
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
            return "You do not have an active trade. Type `sell [amount] [coin]` to start one."

        try:
            status_result = await self._engine.get_trade_status(trade_id)
        except EngineError as exc:
            log.error("Failed to get trade status", error=str(exc), trade_id=trade_id)
            return f"Could not fetch status for trade `{trade_id}`. Please try again."

        trade_status = status_result.get("status") or "unknown"
        confirmations = int(status_result.get("confirmations", 0) or 0)
        required = int(status_result.get("required_confirmations", 2) or 2)
        asset = trade_data.get("asset", "crypto")
        net_naira = self._kobo_to_naira_str(int(trade_data.get("net_naira_kobo", 0)))
        deposit_mode = trade_data.get("deposit_mode") or "chain"

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

        if trade_status == "confirming":
            return (
                "Processing payout.\n\n"
                f"Trade ID: `{trade_id}`\n"
                f"Blockchain progress: {confirmations}/{required} confirmations\n\n"
                f"Your payout of *{net_naira}* is being finalized."
            )

        if trade_status == "settled":
            session.pop("trade_data", None)
            session["step"] = None
            session["flow"] = None
            await self._session.set(user_id, session)
            return (
                "Payment complete.\n\n"
                f"Trade ID: `{trade_id}`\n"
                f"Amount sent: *{net_naira}*\n\n"
                "Your payout has been completed successfully.\n"
                "Type `sell 0.25 BTC` to start another trade."
            )

        if trade_status == "failed":
            session.pop("trade_data", None)
            session["step"] = None
            session["flow"] = None
            await self._session.set(user_id, session)
            return (
                "Trade failed.\n\n"
                f"Trade ID: `{trade_id}`\n\n"
                "This trade could not be completed. Type `sell [amount] [coin]` to start a new one."
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
        return f"N{naira:,.2f}"
