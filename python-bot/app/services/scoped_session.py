from __future__ import annotations

from typing import Any

from app.services.session import SessionService


class ScopedSessionService:
    def __init__(self, base: SessionService, provider: str, channel: str):
        self._base = base
        self._provider = provider
        self._channel = channel

    def _scoped_user_id(self, user_id: str) -> str:
        return f"{self._provider}:{self._channel}:{user_id}"

    async def get(self, user_id: str) -> dict[str, Any]:
        return await self._base.get(self._scoped_user_id(user_id))

    async def set(self, user_id: str, data: dict[str, Any]) -> None:
        payload = data.copy()
        payload.setdefault("provider", self._provider)
        payload.setdefault("channel", self._channel)
        await self._base.set(self._scoped_user_id(user_id), payload)

    async def delete(self, user_id: str) -> None:
        await self._base.delete(self._scoped_user_id(user_id))

    async def get_ttl(self, user_id: str) -> int:
        return await self._base.get_ttl(self._scoped_user_id(user_id))

    async def extend(self, user_id: str) -> None:
        await self._base.extend(self._scoped_user_id(user_id))
