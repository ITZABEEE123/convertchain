# ── NEW TESTS: API hardening additions ──────────────────────────────────────
# Append these classes to test_trade.py (or run as standalone test_trade_hardening.py)

import unittest
from unittest.mock import AsyncMock, MagicMock

def make_engine_error(code: str, message: str, status_code: int = 422) -> dict:
    return {
        "ok": False,
        "status_code": status_code,
        "error": {"code": code, "message": message, "details": None},
    }

def make_engine_success(data: dict) -> dict:
    return {"ok": True, "data": data}


class TestSellBelowMinimumAmount(unittest.IsolatedAsyncioTestCase):
    """engine ERR_AMOUNT_TOO_SMALL → bot surfaces message, never calls confirm_trade."""

    async def test_sell_below_minimum_usdt(self):
        from trade import ConvertChainBot
        bot = ConvertChainBot.__new__(ConvertChainBot)
        bot.engine = MagicMock()
        bot.engine.create_quote = AsyncMock(return_value=make_engine_error(
            code="ERR_AMOUNT_TOO_SMALL",
            message="Amount is below the minimum for USDT. Minimum: 1.000000 USDT.",
            status_code=422,
        ))
        bot.engine.confirm_trade = AsyncMock()

        result = await bot._handle_sell_command(
            user_id="user-123", asset="USDT", amount="0.5",
        )

        bot.engine.create_quote.assert_called_once()
        bot.engine.confirm_trade.assert_not_called()
        assert result is not None
        assert (
            "minimum" in result.lower()
            or "below" in result.lower()
            or "ERR_AMOUNT_TOO_SMALL" in str(result)
        ), f"Expected minimum-amount message, got: {result}"

    async def test_sell_zero_amount_bot_guard(self):
        """Bot must reject zero amounts before reaching engine."""
        from trade import ConvertChainBot
        bot = ConvertChainBot.__new__(ConvertChainBot)
        bot.engine = MagicMock()
        bot.engine.create_quote = AsyncMock()
        bot.engine.confirm_trade = AsyncMock()

        result = await bot._handle_sell_command(
            user_id="user-123", asset="BTC", amount="0",
        )
        bot.engine.create_quote.assert_not_called()
        assert result is not None


class TestSellTierLimitExceeded(unittest.IsolatedAsyncioTestCase):
    """engine ERR_TIER_LIMIT_EXCEEDED → bot surfaces KYC upgrade message."""

    async def test_sell_over_tier1_usdt_limit(self):
        from trade import ConvertChainBot
        bot = ConvertChainBot.__new__(ConvertChainBot)
        bot.engine = MagicMock()
        bot.engine.create_quote = AsyncMock(return_value=make_engine_error(
            code="ERR_TIER_LIMIT_EXCEEDED",
            message=(
                "Amount exceeds your KYC tier (TIER_1) limit for USDT. "
                "Maximum: 500000.000000 USDT. Complete enhanced KYC to increase your limit."
            ),
            status_code=422,
        ))
        bot.engine.confirm_trade = AsyncMock()

        result = await bot._handle_sell_command(
            user_id="user-456", asset="USDT", amount="999999",
        )

        bot.engine.create_quote.assert_called_once()
        bot.engine.confirm_trade.assert_not_called()
        assert result is not None
        assert (
            "tier" in result.lower()
            or "limit" in result.lower()
            or "kyc" in result.lower()
            or "ERR_TIER_LIMIT_EXCEEDED" in str(result)
        ), f"Expected tier-limit message, got: {result}"


class TestBotNeverCallsCreateTrade(unittest.TestCase):
    """Structural invariant: bot never awaits engine.create_trade for user flows."""

    def test_no_awaited_create_trade_in_source(self):
        import re
        import trade as bot_module
        import inspect

        source = inspect.getsource(bot_module)
        awaited_create = re.findall(r"await\s+\S*create_trade", source)
        self.assertEqual(
            len(awaited_create), 0,
            f"Bot must not await create_trade; found: {awaited_create}"
        )

    def test_confirm_trade_awaited_in_source(self):
        import re
        import trade as bot_module
        import inspect

        source = inspect.getsource(bot_module)
        awaited_confirm = re.findall(r"await\s+\S*confirm_trade", source)
        self.assertGreater(
            len(awaited_confirm), 0,
            "Bot sell flow must await confirm_trade at least once."
        )


if __name__ == "__main__":
    unittest.main()
