from __future__ import annotations

import pytest

from app.providers.meta_whatsapp import MetaWhatsAppProvider
from app.providers.openclaw_relay import OpenClawRelayProvider
from app.providers.telegram_direct import TelegramDirectProvider


@pytest.mark.asyncio
async def test_openclaw_relay_accepts_bearer_secret():
    provider = OpenClawRelayProvider(channel="telegram", inbound_secret="relay-secret")
    try:
        is_valid = await provider.verify_request(
            {"authorization": "Bearer relay-secret"},
            b'{"message_id":"msg-1"}',
        )
        assert is_valid is True
    finally:
        await provider.close()


@pytest.mark.asyncio
async def test_openclaw_relay_rejects_invalid_secret():
    provider = OpenClawRelayProvider(channel="whatsapp", inbound_secret="relay-secret")
    try:
        is_valid = await provider.verify_request(
            {"authorization": "Bearer wrong-secret"},
            b'{"message_id":"msg-1"}',
        )
        assert is_valid is False
    finally:
        await provider.close()


@pytest.mark.asyncio
async def test_meta_whatsapp_parse_inbound_text_message():
    provider = MetaWhatsAppProvider(
        access_token="test-token",
        phone_number_id="1234567890",
        app_secret="secret",
    )
    try:
        payload = {
            "entry": [
                {
                    "changes": [
                        {
                            "field": "messages",
                            "value": {
                                "contacts": [
                                    {"wa_id": "2348012345678", "profile": {"name": "Ada"}}
                                ],
                                "messages": [
                                    {
                                        "id": "wamid-1",
                                        "from": "2348012345678",
                                        "timestamp": "1710000000",
                                        "type": "text",
                                        "text": {"body": "hi"},
                                    }
                                ],
                            },
                        }
                    ]
                }
            ]
        }

        envelopes = provider.parse_inbound(payload, signature_valid=True)
        assert len(envelopes) == 1
        assert envelopes[0].provider == "meta"
        assert envelopes[0].channel == "whatsapp"
        assert envelopes[0].user_id == "+2348012345678"
        assert envelopes[0].sender_name == "Ada"
        assert envelopes[0].text == "hi"
    finally:
        await provider.close()


@pytest.mark.asyncio
async def test_telegram_direct_parse_callback_query():
    provider = TelegramDirectProvider(bot_token="test-token")
    try:
        payload = {
            "update_id": 1,
            "callback_query": {
                "id": "cb-1",
                "data": "CONFIRM",
                "from": {"first_name": "Tobi"},
                "message": {
                    "date": 1710000000,
                    "chat": {"id": 99887766},
                },
            },
        }

        envelopes = provider.parse_inbound(payload, signature_valid=True)
        assert len(envelopes) == 1
        assert envelopes[0].provider == "telegram_direct"
        assert envelopes[0].channel == "telegram"
        assert envelopes[0].user_id == "99887766"
        assert envelopes[0].text == "CONFIRM"
    finally:
        await provider.close()
