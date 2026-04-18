from __future__ import annotations

import redis.asyncio as aioredis


class ReplayGuard:
    def __init__(self, redis_client: aioredis.Redis, ttl_seconds: int = 3600):
        self._redis = redis_client
        self._ttl_seconds = ttl_seconds

    async def claim(self, provider: str, channel: str, message_id: str) -> bool:
        if not message_id:
            return True

        key = f"replay:{provider}:{channel}:{message_id}"
        result = await self._redis.set(key, "1", ex=self._ttl_seconds, nx=True)
        return bool(result)
