from __future__ import annotations

import structlog

from app.flows.router import FlowRouter
from app.providers.types import InboundEnvelope, OutboundMessage
from app.services.engine_client import EngineClient
from app.services.scoped_session import ScopedSessionService
from app.services.session import SessionService

log = structlog.get_logger()

SUPPORTED_MEDIA_TYPES = {None, "", "image"}


class MessageRuntime:
    def __init__(self, session_service: SessionService, engine_client: EngineClient):
        self._session = session_service
        self._engine = engine_client

    async def handle_inbound(self, envelope: InboundEnvelope) -> OutboundMessage | None:
        if envelope.media_type not in SUPPORTED_MEDIA_TYPES:
            return OutboundMessage(
                channel=envelope.channel,
                recipient_id=envelope.user_id,
                text="Sorry, I can only process text and images right now.",
                metadata={"provider": envelope.provider, "message_id": envelope.message_id},
            )

        router = FlowRouter(
            session_service=ScopedSessionService(self._session, envelope.provider, envelope.channel),
            engine_client=self._engine,
        )

        response_text = await router.route(
            channel=envelope.channel,
            user_id=envelope.user_id,
            message_text=envelope.text or "",
            image_id=envelope.media_id if envelope.media_type == "image" else None,
            sender_name=envelope.sender_name or "there",
        )

        if not response_text:
            log.debug("no response generated for inbound envelope", provider=envelope.provider, channel=envelope.channel)
            return None

        return OutboundMessage(
            channel=envelope.channel,
            recipient_id=envelope.user_id,
            text=response_text,
            metadata={"provider": envelope.provider, "message_id": envelope.message_id},
        )
