from __future__ import annotations

from decimal import Decimal, InvalidOperation
from typing import Any


class ConvertChainBot:
    """Compatibility facade for legacy hardening tests.

    The production Telegram runtime uses app.flows.trade.TradeFlow. This thin
    adapter keeps old tests pointed at the same safety invariant: user flows
    request quotes and authorize through confirm_trade, never create_trade.
    """

    engine: Any

    async def _handle_sell_command(self, *, user_id: str, asset: str, amount: str) -> str:
        try:
            parsed_amount = Decimal(str(amount))
        except InvalidOperation:
            return "Invalid amount. Please enter a valid number."

        if parsed_amount <= Decimal("0"):
            return "Amount must be greater than zero."

        quote = await self.engine.create_quote(
            {
                "user_id": user_id,
                "asset": asset.upper(),
                "amount": str(parsed_amount),
                "direction": "sell",
            }
        )

        if isinstance(quote, dict) and quote.get("ok") is False:
            error = quote.get("error") or {}
            code = str(error.get("code") or "QUOTE_ERROR")
            message = str(error.get("message") or "Could not create quote.")
            return f"{code}: {message}"

        return "Quote created. Type CONFIRM to continue."

    async def _confirm_trade(self, payload: dict[str, Any]) -> Any:
        return await self.engine.confirm_trade(payload)
