from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock

import pytest

from app.flows.router import FlowRouter


@pytest.fixture
def mock_session_service():
    storage: dict = {}
    service = MagicMock()

    async def get(user_id):
        return storage.get(user_id, {})

    async def set(user_id, data):
        storage[user_id] = data.copy()

    async def delete(user_id):
        storage.pop(user_id, None)

    service.get = get
    service.set = set
    service.delete = delete
    service._storage = storage
    return service


@pytest.mark.asyncio
async def test_greeting_resets_stale_onboarding_session_for_verified_user(mock_session_service):
    engine = MagicMock()
    engine.create_user = AsyncMock(return_value={"user_id": "usr_test123"})
    engine.get_kyc_status = AsyncMock(return_value={"kyc_status": "approved", "tier": 1})

    await mock_session_service.set(
        "5827695262",
        {
            "flow": "onboarding",
            "step": "COLLECT_BVN",
            "engine_user_id": "usr_test123",
            "channel": "telegram",
            "data": {
                "phone": "+2348012345678",
                "first_name": "John",
                "last_name": "Doe",
                "date_of_birth": "1990-01-15",
                "nin": "12345678901",
            },
        },
    )

    router = FlowRouter(session_service=mock_session_service, engine_client=engine)
    result = await router.route(
        channel="telegram",
        user_id="5827695262",
        message_text="hi",
        image_id=None,
        sender_name="John",
    )

    session = await mock_session_service.get("5827695262")
    assert session["onboarded"] is True
    assert session["step"] is None
    assert session["flow"] is None
    assert "already verified" in result.lower()


@pytest.mark.asyncio
async def test_completed_onboarding_session_routes_help_for_verified_user(mock_session_service):
    engine = MagicMock()

    await mock_session_service.set(
        "5827695262",
        {
            "flow": "onboarding",
            "step": "COMPLETED",
            "engine_user_id": "usr_test123",
            "channel": "telegram",
            "onboarded": True,
            "data": {"first_name": "John"},
        },
    )

    router = FlowRouter(session_service=mock_session_service, engine_client=engine)
    result = await router.route(
        channel="telegram",
        user_id="5827695262",
        message_text="help",
        image_id=None,
        sender_name="John",
    )

    session = await mock_session_service.get("5827695262")
    assert session["step"] is None
    assert session["flow"] is None
    assert "ConvertChain Help" in result
    assert "add bank" in result.lower()


@pytest.mark.asyncio
async def test_completed_onboarding_session_routes_sell_for_verified_user(mock_session_service):
    engine = MagicMock()
    engine.list_bank_accounts = AsyncMock(
        return_value={
            "accounts": [
                {
                    "bank_account_id": "bnk_test789",
                    "bank_name": "Access Bank",
                    "account_number": "0123456789",
                    "account_name": "JOHN DOE",
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
            "expires_at": "2026-04-18T22:00:00Z",
        }
    )

    await mock_session_service.set(
        "5827695262",
        {
            "flow": "onboarding",
            "step": "COMPLETED",
            "engine_user_id": "usr_test123",
            "channel": "telegram",
            "onboarded": True,
            "data": {"first_name": "John"},
        },
    )

    router = FlowRouter(session_service=mock_session_service, engine_client=engine)
    result = await router.route(
        channel="telegram",
        user_id="5827695262",
        message_text="sell 0.25 BTC",
        image_id=None,
        sender_name="John",
    )

    session = await mock_session_service.get("5827695262")
    assert session["flow"] == "trade"
    assert session["step"] == "AWAITING_CONFIRMATION"
    assert "Trade Quote" in result


@pytest.mark.asyncio
async def test_verified_user_can_start_add_bank_flow(mock_session_service):
    engine = MagicMock()

    await mock_session_service.set(
        "5827695262",
        {
            "flow": None,
            "step": None,
            "engine_user_id": "usr_test123",
            "channel": "telegram",
            "onboarded": True,
            "data": {"first_name": "John"},
        },
    )

    router = FlowRouter(session_service=mock_session_service, engine_client=engine)
    result = await router.route(
        channel="telegram",
        user_id="5827695262",
        message_text="add bank",
        image_id=None,
        sender_name="John",
    )

    session = await mock_session_service.get("5827695262")
    assert session["flow"] == "bank"
    assert session["step"] == "COLLECT_BANK_CODE"
    assert "058" in result