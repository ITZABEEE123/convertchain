from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


@dataclass(slots=True)
class InboundEnvelope:
    provider: str
    channel: str
    user_id: str
    sender_name: str
    message_id: str
    text: str = ""
    media_type: str | None = None
    media_id: str | None = None
    timestamp: str | None = None
    raw_payload: dict[str, Any] = field(default_factory=dict)
    signature_valid: bool = False


@dataclass(slots=True)
class OutboundMessage:
    channel: str
    recipient_id: str
    message_type: str = "text"
    text: str = ""
    buttons: list[dict[str, str]] = field(default_factory=list)
    metadata: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True)
class SendResult:
    ok: bool
    provider_message_id: str | None = None
    error: str | None = None
