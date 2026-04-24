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
                {"bank_code": "000000", "bank_name": "Sandbox Test Bank"},
                {"bank_code": "011", "bank_name": "First Bank of Nigeria"},
                {"bank_code": "033", "bank_name": "United Bank For Africa"},
                {"bank_code": "044", "bank_name": "Access Bank"},
                {"bank_code": "057", "bank_name": "Zenith Bank"},
                {"bank_code": "058", "bank_name": "Guaranty Trust Bank"},
                {"bank_code": "063", "bank_name": "Access Bank (Diamond)"},
                {"bank_code": "039", "bank_name": "Stanbic IBTC Bank"},
                {"bank_code": "221", "bank_name": "Stanbic IBTC Bank"},
                {"bank_code": "305", "bank_name": "OPay Digital Services Limited (OPay)"},
                {"bank_code": "796", "bank_name": "Moniepoint MFB"},
                {"bank_code": "999991", "bank_name": "PalmPay"},
            ]
        }
    )
    engine.resolve_bank_account = AsyncMock(
        return_value={
            "bank_code": "058",
            "bank_name": "Guaranty Trust Bank",
            "account_number": "1234567890",
            "account_name": "Test User",
        }
    )
    engine.list_bank_accounts = AsyncMock(
        return_value={
            "accounts": [
                {
                    "bank_account_id": "bnk_1",
                    "bank_name": "Guaranty Trust Bank",
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
            "bank_name": "Guaranty Trust Bank",
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
    assert "bank code or bank name" in result.lower()
    assert "058" in result


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
    assert saved["bank_data"]["bank_code"] == "058"
    assert saved["bank_data"]["bank_name"] == "Guaranty Trust Bank"
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
    assert saved["bank_data"]["bank_code"] == "058"
    assert saved["bank_data"]["bank_name"] == "Guaranty Trust Bank"
    assert "guaranty trust bank" in result.lower()


@pytest.mark.asyncio
async def test_collect_bank_name_returns_suggestions_for_ambiguous_match(bank_flow, mock_session_service):
    session = {
        "onboarded": True,
        "engine_user_id": "usr_123",
        "flow": "bank",
        "step": STEP_COLLECT_BANK_CODE,
        "bank_data": {},
    }

    result = await bank_flow.handle_step("user-1", session, "Stanbic")

    saved = await mock_session_service.get("user-1")
    assert saved == {}
    assert "close matches" in result.lower()
    assert "039" in result
    assert "221" in result


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
    assert saved["bank_data"]["bank_code"] == "058"
    assert "account number" in result.lower()


@pytest.mark.asyncio
async def test_account_number_step_resolves_account_and_moves_to_confirmation(bank_flow, mock_session_service, mock_engine_client):
    session = {
        "onboarded": True,
        "engine_user_id": "usr_123",
        "flow": "bank",
        "step": STEP_COLLECT_ACCOUNT_NUMBER,
        "bank_data": {"bank_code": "058", "bank_name": "Guaranty Trust Bank"},
    }

    result = await bank_flow.handle_step("user-1", session, "1234567890")

    mock_engine_client.resolve_bank_account.assert_awaited_once()
    payload = mock_engine_client.resolve_bank_account.await_args.args[0]
    assert payload["user_id"] == "usr_123"
    assert payload["bank_code"] == "058"
    assert payload["account_number"] == "1234567890"

    saved = await mock_session_service.get("user-1")
    assert saved["step"] == STEP_CONFIRM_BANK_ACCOUNT
    assert saved["bank_data"]["account_name"] == "Test User"
    assert "bank account verified" in result.lower()


@pytest.mark.asyncio
async def test_account_number_step_accepts_multiline_input(bank_flow, mock_session_service, mock_engine_client):
    mock_engine_client.resolve_bank_account = AsyncMock(
        return_value={
            "bank_code": "058",
            "bank_name": "Guaranty Trust Bank",
            "account_number": "2274091001",
            "account_name": "Test User",
        }
    )
    session = {
        "onboarded": True,
        "engine_user_id": "usr_123",
        "flow": "bank",
        "step": STEP_COLLECT_ACCOUNT_NUMBER,
        "bank_data": {"bank_code": "058", "bank_name": "Guaranty Trust Bank"},
    }

    result = await bank_flow.handle_step("user-1", session, "Account number: 2274091001")

    payload = mock_engine_client.resolve_bank_account.await_args.args[0]
    assert payload["account_number"] == "2274091001"
    assert "test user" in result.lower()


@pytest.mark.asyncio
async def test_confirm_bank_account_selects_new_account(bank_flow, mock_session_service, mock_engine_client):
    session = {
        "onboarded": True,
        "engine_user_id": "usr_123",
        "flow": "bank",
        "step": STEP_CONFIRM_BANK_ACCOUNT,
        "bank_data": {
            "bank_code": "058",
            "bank_name": "Guaranty Trust Bank",
            "account_number": "1234567890",
            "account_name": "Test User",
        },
    }

    result = await bank_flow.handle_step("user-1", session, "YES")

    mock_engine_client.add_bank_account.assert_awaited_once()
    payload = mock_engine_client.add_bank_account.await_args.args[0]
    assert payload["user_id"] == "usr_123"
    assert payload["bank_code"] == "058"
    assert payload["account_number"] == "1234567890"
    assert payload["account_name"] == "Test User"

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
            "bank_code": "058",
            "bank_name": "Guaranty Trust Bank",
            "account_number": "1234567890",
            "account_name": "Test User",
        },
    }

    result = await bank_flow.handle_step("user-1", session, "YES")

    assert "Could not save" in result
    assert "bank validation failed" in result
