from __future__ import annotations

from unittest.mock import MagicMock

import pytest

from app.services.message_runtime import MessageRuntime


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
async def test_kyc_verified_notification_sends_step_8_and_sets_session(mock_session_service):
    runtime = MessageRuntime(mock_session_service, MagicMock())

    outbound = await runtime._build_notification_message(
        {
            "id": "notif_1",
            "event_type": "kyc.verified",
            "recipient_id": "582769000000",
            "payload": {"user_id": "engine-user-1", "first_name": "Ada"},
        },
        provider_name="telegram_direct",
        outbound_channel="telegram",
    )

    assert outbound is not None
    assert "Identity Verified" in outbound.text
    assert "Secure Your Account" in outbound.text
    assert "sandbox" not in outbound.text.lower()

    saved = mock_session_service._storage["telegram_direct:telegram:582769000000"]
    assert saved["flow"] == "onboarding"
    assert saved["step"] == "SET_TX_PASSWORD"
    assert saved["engine_user_id"] == "engine-user-1"
