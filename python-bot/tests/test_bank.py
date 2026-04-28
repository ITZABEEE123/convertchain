from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock

import pytest

from app.flows.bank import (
    BankFlow,
    STEP_COLLECT_ACCOUNT_NUMBER,
    STEP_COLLECT_BANK_CODE,
    STEP_CONFIRM_BANK_ACCOUNT,
)
from app.services.engine_client import EngineError


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


@pytest.fixture
def mock_engine_client():
    engine = MagicMock()
    engine.list_banks = AsyncMock(
        return_value={
            "banks": [
                {"bank_id": "bk-sandbox", "provider_bank_id": "bk-sandbox", "bank_code": "000000", "resolve_bank_code": "000000", "bank_name": "Sandbox Test Bank", "currency": "NGN"},
                {"bank_id": "bk-zenith", "provider_bank_id": "bk-zenith", "bank_code": "000015", "resolve_bank_code": "000015", "nip_code": "000015", "short_code": "057", "bank_name": "ZENITH BANK PLC", "slug": "zenith", "currency": "NGN"},
                {"bank_id": "bk-zenith-mobile", "provider_bank_id": "bk-zenith-mobile", "bank_code": "100018", "resolve_bank_code": "100018", "nip_code": "100018", "short_code": "322", "bank_name": "ZENITH MOBILE", "slug": "zenith-mobile", "currency": "NGN"},
                {"bank_id": "bk-gtb", "provider_bank_id": "bk-gtb", "bank_code": "000013", "resolve_bank_code": "000013", "nip_code": "000013", "short_code": "058", "bank_name": "GUARANTY TRUST BANK", "slug": "gtbank", "currency": "NGN"},
                {"bank_id": "bk-access", "provider_bank_id": "bk-access", "bank_code": "000014", "resolve_bank_code": "000014", "nip_code": "000014", "short_code": "044", "bank_name": "ACCESS BANK", "slug": "access", "currency": "NGN"},
                {"bank_id": "bk-uba", "provider_bank_id": "bk-uba", "bank_code": "000004", "resolve_bank_code": "000004", "nip_code": "000004", "short_code": "033", "bank_name": "UNITED BANK FOR AFRICA", "slug": "uba", "currency": "NGN"},
                {"bank_id": "bk-first", "provider_bank_id": "bk-first", "bank_code": "000016", "resolve_bank_code": "000016", "nip_code": "000016", "short_code": "011", "bank_name": "FIRST BANK OF NIGERIA", "slug": "first-bank", "currency": "NGN"},
                {"bank_id": "bk-stanbic", "provider_bank_id": "bk-stanbic", "bank_code": "000012", "resolve_bank_code": "000012", "nip_code": "000012", "short_code": "221", "bank_name": "STANBIC IBTC BANK", "slug": "stanbic", "currency": "NGN"},
                {"bank_id": "bk-opay-1", "provider_bank_id": "bk-opay-1", "bank_code": "100004", "resolve_bank_code": "100004", "nip_code": "100004", "short_code": "305", "bank_name": "OPAY DIGITAL SERVICES LIMITED (OPAY)", "slug": "opay", "currency": "NGN"},
                {"bank_id": "bk-opay-2", "provider_bank_id": "bk-opay-2", "bank_code": "999992", "resolve_bank_code": "999992", "nip_code": "999992", "short_code": "999992", "bank_name": "OPAY DIGITAL SERVICES LIMITED (OPAY)", "slug": "opay-wallet", "currency": "NGN"},
                {"bank_id": "bk-moniepoint", "provider_bank_id": "bk-moniepoint", "bank_code": "090405", "resolve_bank_code": "090405", "nip_code": "090405", "short_code": "796", "bank_name": "MONIEPOINT MICROFINANCE BANK", "slug": "moniepoint-mfb", "currency": "NGN"},
                {"bank_id": "bk-palmpay", "provider_bank_id": "bk-palmpay", "bank_code": "999991", "resolve_bank_code": "999991", "nip_code": "999991", "short_code": "999991", "bank_name": "PALMPAY", "slug": "palmpay", "currency": "NGN"},
                {"bank_id": "bk-kuda", "provider_bank_id": "bk-kuda", "bank_code": "090267", "resolve_bank_code": "090267", "nip_code": "090267", "short_code": "50211", "bank_name": "KUDA MICROFINANCE BANK", "slug": "kuda", "currency": "NGN"},
            ]
        }
    )
    engine.resolve_bank_account = AsyncMock(
        return_value={
            "bank_code": "000013",
            "bank_name": "GUARANTY TRUST BANK",
            "account_number": "1234567890",
            "account_name": "Test User",
        }
    )
    engine.list_bank_accounts = AsyncMock(
        return_value={
            "accounts": [
                {
                    "bank_account_id": "bnk_1",
                    "bank_name": "GUARANTY TRUST BANK",
                    "account_number": "******1234",
                    "account_name": "Test User",
                },
                {
                    "bank_account_id": "bnk_2",
                    "bank_name": "Access Bank",
                    "account_number": "******5678",
                    "account_name": "Second User",
                },
            ]
        }
    )
    engine.add_bank_account = AsyncMock(
        return_value={
            "bank_account_id": "bnk_new",
            "bank_name": "GUARANTY TRUST BANK",
            "account_number": "******7890",
            "account_name": "Test User",
        }
    )
    return engine


@pytest.fixture
def bank_flow(mock_session_service, mock_engine_client):
    return BankFlow(mock_session_service, mock_engine_client, "telegram")


@pytest.mark.asyncio
async def test_start_bank_flow_sets_bank_state(bank_flow, mock_session_service):
    session = {"onboarded": True, "engine_user_id": "usr_123", "flow": None, "step": None}

    result = await bank_flow.start("user-1", session)

    saved = await mock_session_service.get("user-1")
    assert saved["flow"] == "bank"
    assert saved["step"] == STEP_COLLECT_BANK_CODE
    assert "popular banks" in result.lower()
    assert "zenith bank" in result.lower()
    assert "058" not in result


@pytest.mark.asyncio
async def test_collect_bank_code_advances_to_account_number(bank_flow, mock_session_service):
    session = {
        "onboarded": True,
        "engine_user_id": "usr_123",
        "flow": "bank",
        "step": STEP_COLLECT_BANK_CODE,
        "bank_data": {},
    }

    result = await bank_flow.handle_step("user-1", session, "058")

    saved = await mock_session_service.get("user-1")
    assert saved["step"] == STEP_COLLECT_ACCOUNT_NUMBER
    assert saved["bank_data"]["bank_code"] == "000013"
    assert saved["bank_data"]["resolve_bank_code"] == "000013"
    assert saved["bank_data"]["short_code"] == "058"
    assert saved["bank_data"]["bank_name"] == "GUARANTY TRUST BANK"
    assert "account number" in result.lower()


@pytest.mark.asyncio
async def test_collect_sandbox_bank_code_advances_to_account_number(bank_flow, mock_session_service):
    session = {
        "onboarded": True,
        "engine_user_id": "usr_123",
        "flow": "bank",
        "step": STEP_COLLECT_BANK_CODE,
        "bank_data": {},
    }

    result = await bank_flow.handle_step("user-1", session, "000000")

    saved = await mock_session_service.get("user-1")
    assert saved["step"] == STEP_COLLECT_ACCOUNT_NUMBER
    assert saved["bank_data"]["bank_code"] == "000000"
    assert saved["bank_data"]["bank_name"] == "Sandbox Test Bank"
    assert "account number" in result.lower()


@pytest.mark.asyncio
async def test_popular_number_selects_zenith_nip_code(bank_flow, mock_session_service):
    session = {"onboarded": True, "engine_user_id": "usr_123", "flow": None, "step": None}
    await bank_flow.start("user-1", session)
    saved = await mock_session_service.get("user-1")

    result = await bank_flow.handle_step("user-1", saved, "1")

    saved = await mock_session_service.get("user-1")
    assert saved["step"] == STEP_COLLECT_ACCOUNT_NUMBER
    assert saved["bank_data"]["bank_code"] == "000015"
    assert saved["bank_data"]["short_code"] == "057"
    assert saved["bank_data"]["bank_name"] == "ZENITH BANK PLC"
    assert "zenith bank plc" in result.lower()


@pytest.mark.asyncio
async def test_collect_bank_name_advances_to_account_number(bank_flow, mock_session_service):
    session = {
        "onboarded": True,
        "engine_user_id": "usr_123",
        "flow": "bank",
        "step": STEP_COLLECT_BANK_CODE,
        "bank_data": {},
    }

    result = await bank_flow.handle_step("user-1", session, "GTBank")

    saved = await mock_session_service.get("user-1")
    assert saved["step"] == STEP_COLLECT_ACCOUNT_NUMBER
    assert saved["bank_data"]["bank_code"] == "000013"
    assert saved["bank_data"]["bank_name"] == "GUARANTY TRUST BANK"
    assert "i found" in result.lower()
    assert "guaranty trust bank" in result.lower()


@pytest.mark.asyncio
async def test_collect_zenith_prefers_real_bank_over_mobile(bank_flow, mock_session_service):
    session = {
        "onboarded": True,
        "engine_user_id": "usr_123",
        "flow": "bank",
        "step": STEP_COLLECT_BANK_CODE,
        "bank_data": {},
    }

    result = await bank_flow.handle_step("user-1", session, "Zenith")

    saved = await mock_session_service.get("user-1")
    assert saved["step"] == STEP_COLLECT_ACCOUNT_NUMBER
    assert saved["bank_data"]["bank_code"] == "000015"
    assert saved["bank_data"]["bank_name"] == "ZENITH BANK PLC"
    assert "zenith bank plc" in result.lower()


@pytest.mark.asyncio
async def test_typo_senith_suggests_zenith(bank_flow, mock_session_service):
    session = {
        "onboarded": True,
        "engine_user_id": "usr_123",
        "flow": "bank",
        "step": STEP_COLLECT_BANK_CODE,
        "bank_data": {},
    }

    result = await bank_flow.handle_step("user-1", session, "senith")

    saved = await mock_session_service.get("user-1")
    assert saved["bank_data"]["bank_suggestion"]["bank_name"] == "ZENITH BANK PLC"
    assert "did you mean" in result.lower()
    assert "zenith bank plc" in result.lower()


@pytest.mark.asyncio
async def test_collect_bank_name_returns_suggestions_for_ambiguous_match(bank_flow, mock_session_service):
    session = {
        "onboarded": True,
        "engine_user_id": "usr_123",
        "flow": "bank",
        "step": STEP_COLLECT_BANK_CODE,
        "bank_data": {},
    }

    result = await bank_flow.handle_step("user-1", session, "OPay")

    saved = await mock_session_service.get("user-1")
    assert saved["bank_data"]["bank_suggestions"]
    assert "close matches" in result.lower()
    assert "OPAY" in result
    assert "code ending" in result


@pytest.mark.asyncio
async def test_collect_bank_code_accepts_labeled_text(bank_flow, mock_session_service):
    session = {
        "onboarded": True,
        "engine_user_id": "usr_123",
        "flow": "bank",
        "step": STEP_COLLECT_BANK_CODE,
        "bank_data": {},
    }

    result = await bank_flow.handle_step("user-1", session, "Bank code: 058")

    saved = await mock_session_service.get("user-1")
    assert saved["step"] == STEP_COLLECT_ACCOUNT_NUMBER
    assert saved["bank_data"]["bank_code"] == "000013"
    assert "account number" in result.lower()


@pytest.mark.asyncio
async def test_account_number_step_resolves_account_and_moves_to_confirmation(bank_flow, mock_session_service, mock_engine_client):
    session = {
        "onboarded": True,
        "engine_user_id": "usr_123",
        "flow": "bank",
        "step": STEP_COLLECT_ACCOUNT_NUMBER,
        "bank_data": {"bank_code": "000013", "resolve_bank_code": "000013", "bank_name": "GUARANTY TRUST BANK", "provider_bank_id": "bk-gtb"},
    }

    result = await bank_flow.handle_step("user-1", session, "1234567890")

    mock_engine_client.resolve_bank_account.assert_awaited_once()
    payload = mock_engine_client.resolve_bank_account.await_args.args[0]
    assert payload["user_id"] == "usr_123"
    assert payload["bank_code"] == "000013"
    assert payload["provider_bank_id"] == "bk-gtb"
    assert payload["bank_name"] == "GUARANTY TRUST BANK"
    assert payload["account_number"] == "1234567890"
    assert payload["currency"] == "NGN"

    saved = await mock_session_service.get("user-1")
    assert saved["step"] == STEP_CONFIRM_BANK_ACCOUNT
    assert saved["bank_data"]["account_number"] == "1234567890"
    assert saved["bank_data"]["account_name"] == "Test User"
    assert "i found this account" in result.lower()
    assert "account name" in result.lower()


@pytest.mark.asyncio
async def test_account_number_step_accepts_multiline_input(bank_flow, mock_session_service, mock_engine_client):
    mock_engine_client.resolve_bank_account = AsyncMock(
        return_value={
            "bank_code": "000013",
            "bank_name": "GUARANTY TRUST BANK",
            "account_number": "2274091001",
            "account_name": "Test User",
        }
    )
    session = {
        "onboarded": True,
        "engine_user_id": "usr_123",
        "flow": "bank",
        "step": STEP_COLLECT_ACCOUNT_NUMBER,
        "bank_data": {"bank_code": "000013", "resolve_bank_code": "000013", "bank_name": "GUARANTY TRUST BANK"},
    }

    result = await bank_flow.handle_step("user-1", session, "Account number: 2274091001")

    payload = mock_engine_client.resolve_bank_account.await_args.args[0]
    assert payload["account_number"] == "2274091001"
    assert payload["currency"] == "NGN"
    assert "test user" in result.lower()


@pytest.mark.asyncio
async def test_confirm_bank_account_selects_new_account(bank_flow, mock_session_service, mock_engine_client):
    session = {
        "onboarded": True,
        "engine_user_id": "usr_123",
        "flow": "bank",
        "step": STEP_CONFIRM_BANK_ACCOUNT,
        "bank_data": {
            "bank_code": "000013",
            "bank_name": "GUARANTY TRUST BANK",
            "account_number": "1234567890",
            "account_name": "Test User",
        },
    }

    result = await bank_flow.handle_step("user-1", session, "YES")

    mock_engine_client.add_bank_account.assert_awaited_once()
    payload = mock_engine_client.add_bank_account.await_args.args[0]
    assert payload["user_id"] == "usr_123"
    assert payload["bank_code"] == "000013"
    assert payload["bank_name"] == "GUARANTY TRUST BANK"
    assert payload["account_number"] == "1234567890"
    assert payload["account_name"] == "Test User"
    assert payload["currency"] == "NGN"

    saved = await mock_session_service.get("user-1")
    assert saved["selected_bank_account_id"] == "bnk_new"
    assert saved["flow"] is None
    assert saved["step"] is None
    assert "trade immediately" in result.lower()


@pytest.mark.asyncio
async def test_show_accounts_marks_selected_account(bank_flow, mock_session_service):
    session = {
        "onboarded": True,
        "engine_user_id": "usr_123",
        "selected_bank_account_id": "bnk_2",
    }

    result = await bank_flow.show_accounts("user-1", session)

    assert "Your Bank Accounts" in result
    assert "selected" in result
    assert "use bank 1" in result.lower()


@pytest.mark.asyncio
async def test_select_account_updates_selected_bank(bank_flow, mock_session_service):
    session = {"onboarded": True, "engine_user_id": "usr_123"}

    result = await bank_flow.select_account("user-1", session, "use bank 2")

    saved = await mock_session_service.get("user-1")
    assert saved["selected_bank_account_id"] == "bnk_2"
    assert "selected" in result.lower()


@pytest.mark.asyncio
async def test_add_bank_error_returns_helpful_message(bank_flow, mock_session_service, mock_engine_client):
    mock_engine_client.add_bank_account = AsyncMock(side_effect=EngineError("bank validation failed", status_code=400))
    session = {
        "onboarded": True,
        "engine_user_id": "usr_123",
        "flow": "bank",
        "step": STEP_CONFIRM_BANK_ACCOUNT,
        "bank_data": {
            "bank_code": "000013",
            "bank_name": "GUARANTY TRUST BANK",
            "account_number": "1234567890",
            "account_name": "Test User",
        },
    }

    result = await bank_flow.handle_step("user-1", session, "YES")

    assert "Could not save" in result
    assert "bank validation failed" not in result


@pytest.mark.asyncio
async def test_account_number_resolve_failure_returns_retry_message(bank_flow, mock_session_service, mock_engine_client):
    mock_engine_client.resolve_bank_account = AsyncMock(
        side_effect=EngineError(
            "Engine returned 400: account not found",
            status_code=400,
            code="account_not_found",
        )
    )
    session = {
        "onboarded": True,
        "engine_user_id": "usr_123",
        "flow": "bank",
        "step": STEP_COLLECT_ACCOUNT_NUMBER,
        "bank_data": {"bank_code": "000015", "resolve_bank_code": "000015", "bank_name": "ZENITH BANK PLC"},
    }

    result = await bank_flow.handle_step("user-1", session, "2274091001")

    payload = mock_engine_client.resolve_bank_account.await_args.args[0]
    assert payload["bank_code"] == "000015"
    assert payload["bank_name"] == "ZENITH BANK PLC"
    assert payload["currency"] == "NGN"
    assert "could not verify this account" in result.lower()
