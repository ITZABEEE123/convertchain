from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock

import pytest

from app.flows.trade import STEP_AWAITING_CONFIRMATION, STEP_AWAITING_DEPOSIT, STEP_AWAITING_TRANSACTION_PASSWORD, TradeFlow
from app.services.engine_client import EngineError


@pytest.fixture
def mock_session_service():
    storage: dict = {}
    service = MagicMock()

    async def get(uid):
        return storage.get(uid, {})

    async def set(uid, data):
        storage[uid] = data.copy()

    async def delete(uid):
        storage.pop(uid, None)

    service.get = get
    service.set = set
    service.delete = delete
    service._storage = storage
    return service


@pytest.fixture
def mock_engine_client():
    engine = MagicMock()
    engine.list_bank_accounts = AsyncMock(
        return_value={
            "accounts": [
                {
                    "bank_account_id": "bnk_test789",
                    "bank_name": "Access Bank",
                    "account_number": "0123456789",
                    "account_name": "JOHN OLUWASEUN",
                }
            ]
        }
    )
    engine.get_quote = AsyncMock(
        return_value={
            "quote_id": "qte_test456",
            "asset": "BTC",
            "amount": "0.25",
            "rate": 6543210000,
            "net_naira_kobo": 1627623487,
            "fee_kobo": 8179013,
            "gross_naira_kobo": 1635802500,
            "platform_fee_kobo": 8179013,
            "platform_fee_bps": 50,
            "market_rate_per_unit_kobo": 6600000000,
            "user_rate_per_unit_kobo": 6543210000,
            "pricing_mode": "live",
            "expires_at": "2026-04-18T12:00:30Z",
        }
    )
    engine.confirm_trade = AsyncMock(
        return_value={
            "trade_id": "trd_test012",
            "status": "awaiting_deposit",
            "deposit_address": "sandbox://deposit/btc/trd_test012",
            "deposit_amount": "0.25",
            "asset": "BTC",
            "expires_at": "2026-04-18T13:00:00Z",
        }
    )
    engine.get_trade_receipt = AsyncMock(
        return_value={
            "trade_id": "trd_test012",
            "trade_ref": "TRD-TEST012",
            "status": "PAYOUT_COMPLETED",
            "pricing_mode": "sandbox_live_rates",
            "payout_amount_kobo": 1627623487,
            "fee_amount_kobo": 8179013,
            "bank_name": "Access Bank",
            "masked_account_number": "******6789",
            "payout_ref": "pay_test_123",
            "created_at": "2026-04-18T12:00:00Z",
            "payout_completed_at": "2026-04-18T12:05:00Z",
        }
    )
    engine.get_trade_status = AsyncMock(
        return_value={
            "trade_id": "trd_test012",
            "status": "awaiting_deposit",
            "confirmations": 0,
            "required_confirmations": 2,
        }
    )
    return engine


@pytest.fixture
def trade_flow(mock_session_service, mock_engine_client):
    return TradeFlow(mock_session_service, mock_engine_client, "whatsapp")


@pytest.fixture
def onboarded_session():
    return {
        "flow": None,
        "step": None,
        "onboarded": True,
        "transaction_password_set": True,
        "engine_user_id": "usr_test123",
        "data": {"first_name": "John", "last_name": "Oluwaseun", "phone": "+2348012345678"},
    }


@pytest.mark.parametrize(
    "command, expected_coin",
    [
        ("sell 0.25 BTC", "BTC"),
        ("sell 1 ETH", "ETH"),
        ("SELL 100 USDT", "USDT"),
        ("sell 0.001 btc", "BTC"),
    ],
)
@pytest.mark.asyncio
async def test_sell_command_valid(trade_flow, mock_session_service, onboarded_session, command, expected_coin):
    await mock_session_service.set("+2348012345678", onboarded_session)

    result = await trade_flow.handle_sell_intent(
        user_id="+2348012345678",
        session=onboarded_session,
        text=command,
    )

    assert "Trade Quote" in result
    assert expected_coin in result
    assert "CONFIRM" in result


@pytest.mark.parametrize(
    "command",
    [
        "sell BTC",
        "buy 0.25 BTC",
        "sell -1 BTC",
        "sell 0 ETH",
        "sell 0.25 DOGE",
        "sell abc BTC",
    ],
)
@pytest.mark.asyncio
async def test_sell_command_invalid(trade_flow, onboarded_session, command):
    result = await trade_flow.handle_sell_intent(
        user_id="+2348012345678",
        session=onboarded_session,
        text=command,
    )

    assert "Invalid" in result or "greater than zero" in result or "Minimum trade amount" in result


@pytest.mark.asyncio
async def test_sell_requires_bank_self_service_hint(trade_flow, onboarded_session, mock_engine_client):
    mock_engine_client.list_bank_accounts = AsyncMock(return_value={"accounts": []})

    result = await trade_flow.handle_sell_intent(
        user_id="+2348012345678",
        session=onboarded_session,
        text="sell 0.25 BTC",
    )

    assert "add bank" in result.lower()
    assert "000000" in result


@pytest.mark.asyncio
async def test_sell_uses_selected_bank_account(trade_flow, mock_session_service, onboarded_session, mock_engine_client):
    session = {**onboarded_session, "selected_bank_account_id": "bnk_selected"}
    mock_engine_client.list_bank_accounts = AsyncMock(
        return_value={
            "accounts": [
                {
                    "bank_account_id": "bnk_first",
                    "bank_name": "First Bank",
                    "account_number": "1111111111",
                    "account_name": "First User",
                },
                {
                    "bank_account_id": "bnk_selected",
                    "bank_name": "Access Bank",
                    "account_number": "2222222222",
                    "account_name": "Chosen User",
                },
            ]
        }
    )

    await trade_flow.handle_sell_intent("+2348012345678", session, "sell 0.25 BTC")

    saved = await mock_session_service.get("+2348012345678")
    assert saved["trade_data"]["bank_account_id"] == "bnk_selected"
    assert saved["trade_data"]["bank_name"] == "Access Bank"


@pytest.mark.asyncio
async def test_confirm_creates_sandbox_trade(trade_flow, mock_session_service, onboarded_session, mock_engine_client):
    session_with_quote = {
        **onboarded_session,
        "flow": "trade",
        "step": STEP_AWAITING_CONFIRMATION,
        "trade_data": {
            "quote_id": "qte_test456",
            "asset": "BTC",
            "amount": "0.25",
            "market_rate_per_unit_kobo": 6600000000,
            "user_rate_per_unit_kobo": 6543210000,
            "net_naira_kobo": 1627623487,
            "platform_fee_kobo": 8179013,
            "platform_fee_bps": 50,
            "expires_at": "2099-01-01T12:00:30Z",
            "bank_account_id": "bnk_test789",
            "trade_id": None,
        },
    }
    await mock_session_service.set("+2348012345678", session_with_quote)

    session = await mock_session_service.get("+2348012345678")
    result = await trade_flow.handle_step("+2348012345678", session, "CONFIRM")
    assert "transaction password" in result.lower()

    updated = await mock_session_service.get("+2348012345678")
    assert updated["step"] == STEP_AWAITING_TRANSACTION_PASSWORD

    result = await trade_flow.handle_step("+2348012345678", updated, "secret123")

    mock_engine_client.confirm_trade.assert_called_once()
    updated = await mock_session_service.get("+2348012345678")
    assert updated["trade_data"]["trade_id"] == "trd_test012"
    assert updated["trade_data"]["deposit_mode"] == "sandbox"
    assert updated["step"] == STEP_AWAITING_DEPOSIT
    assert "sandbox" in result.lower()
    assert "status" in result.lower()


@pytest.mark.asyncio
async def test_confirm_expired_quote(trade_flow, mock_session_service, mock_engine_client, onboarded_session):
    mock_engine_client.confirm_trade = AsyncMock(side_effect=EngineError("Quote expired", status_code=409))
    session_with_quote = {
        **onboarded_session,
        "flow": "trade",
        "step": STEP_AWAITING_CONFIRMATION,
        "trade_data": {
            "quote_id": "qte_expired",
            "asset": "BTC",
            "amount": "0.25",
            "market_rate_per_unit_kobo": 6600000000,
            "user_rate_per_unit_kobo": 6543210000,
            "net_naira_kobo": 1627623487,
            "platform_fee_kobo": 8179013,
            "expires_at": "2020-01-01T00:00:00Z",
            "bank_account_id": "bnk_test789",
            "trade_id": None,
        },
    }
    await mock_session_service.set("+2348012345678", session_with_quote)

    session = await mock_session_service.get("+2348012345678")
    await trade_flow.handle_step("+2348012345678", session, "CONFIRM")
    updated = await mock_session_service.get("+2348012345678")
    result = await trade_flow.handle_step("+2348012345678", updated, "secret123")

    assert "expired" in result.lower()


@pytest.mark.asyncio
async def test_status_settled_clears_session(trade_flow, mock_session_service, mock_engine_client):
    mock_engine_client.get_trade_status = AsyncMock(
        return_value={
            "trade_id": "trd_test012",
            "status": "settled",
            "confirmations": 2,
            "required_confirmations": 2,
        }
    )
    session = {
        "onboarded": True,
        "transaction_password_set": True,
        "engine_user_id": "usr_test123",
        "flow": "trade",
        "step": STEP_AWAITING_DEPOSIT,
        "trade_data": {
            "trade_id": "trd_test012",
            "asset": "BTC",
            "amount": "0.25",
            "net_naira_kobo": 1627623487,
            "deposit_mode": "sandbox",
        },
    }
    await mock_session_service.set("+2348012345678", session)

    result = await trade_flow.handle_status("+2348012345678", session)

    assert "Payment complete" in result
    updated = await mock_session_service.get("+2348012345678")
    assert "trade_data" not in updated or updated.get("trade_data") is None


@pytest.mark.asyncio
async def test_status_recovers_latest_active_trade_from_engine(trade_flow, mock_session_service, mock_engine_client):
    mock_engine_client.get_latest_active_trade = AsyncMock(
        return_value={
            "trade_id": "trd_dispute999",
            "trade_ref": "TRD-DISPUTE999",
            "status": "needs_attention",
            "raw_status": "DISPUTE",
            "asset": "BTC",
            "net_amount_kobo": 20863484868,
            "deposit_address": "sandbox://deposit/btc/trd_dispute999",
            "bank_name": "Oval Banc",
            "masked_account_number": "******1001",
            "dispute_reason": "binance: insufficient BTC balance: have 0.75000000, need 2",
        }
    )
    mock_engine_client.get_trade_status = AsyncMock(
        return_value={
            "trade_id": "trd_dispute999",
            "status": "needs_attention",
            "raw_status": "DISPUTE",
            "confirmations": 2,
            "required_confirmations": 2,
            "bank_name": "Oval Banc",
            "masked_account_number": "******1001",
            "dispute_reason": "binance: insufficient BTC balance: have 0.75000000, need 2",
        }
    )

    session = {
        "onboarded": True,
        "transaction_password_set": True,
        "engine_user_id": "usr_test123",
        "flow": None,
        "step": None,
    }

    result = await trade_flow.handle_status("+2348012345678", session)

    assert "requires manual review" in result.lower()
    assert "blocking account deletion" in result.lower()
    updated = await mock_session_service.get("+2348012345678")
    assert updated["trade_data"]["trade_id"] == "trd_dispute999"


def test_kobo_to_naira_conversion():
    assert TradeFlow._kobo_to_naira_str(100) == "₦1.00"
    assert TradeFlow._kobo_to_naira_str(163762348750) == "₦1,637,623,487.50"
    assert TradeFlow._kobo_to_naira_str(50) == "₦0.50"
    assert TradeFlow._kobo_to_naira_str(0) == "₦0.00"
