from __future__ import annotations

from typing import Any, Mapping, Protocol

from app.providers.types import InboundEnvelope, OutboundMessage, SendResult


class ProviderAdapter(Protocol):
    provider_name: str
    channel: str

    async def verify_request(self, headers: Mapping[str, str], body: bytes) -> bool: ...

    def parse_inbound(
        self,
        payload: dict[str, Any],
        *,
        signature_valid: bool,
    ) -> list[InboundEnvelope]: ...

    async def send_text(self, message: OutboundMessage) -> SendResult: ...

    async def send_interactive(self, message: OutboundMessage) -> SendResult: ...

    def supports(self, channel: str, capability: str) -> bool: ...

    async def close(self) -> None: ...
