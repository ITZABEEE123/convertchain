# app/services/session.py
# ============================================================
# Session management using Redis.
# Sessions store per-user conversation state between messages.
# Each session is a JSON blob with a 24-hour expiry (TTL).
# ============================================================

from __future__ import annotations

import json
from typing import Any

import redis.asyncio as aioredis
import structlog

log = structlog.get_logger()

# How long a session lives in Redis without being touched.
# 24 hours = 86,400 seconds.
# After 24 hours of inactivity, the session is deleted automatically.
# When a user messages again, they start fresh.
SESSION_TTL_SECONDS = 86_400  # 24 hours

# Key prefix for all session keys in Redis.
# Using a prefix prevents collisions with other data stored in Redis
# (e.g., if the Go engine also uses the same Redis instance).
KEY_PREFIX = "session"


class SessionService:
    """
    Manages user conversation sessions in Redis.

    Each user (identified by channel + user_id, e.g., WhatsApp phone number)
    has one session storing their current flow, step, and collected data.

    THREAD SAFETY:
    This class uses async Redis — all methods are coroutines (async def).
    The redis.asyncio client is designed for concurrent async usage.
    You can call session methods from multiple concurrent requests safely.

    ISOLATION:
    Sessions are isolated by user_id. User A's session never affects User B's.
    This is guaranteed by the key prefix: "session:{user_id}".
    """

    def __init__(self, redis_client: aioredis.Redis):
        """
        Args:
            redis_client: An async Redis client (created in main.py lifespan).
                         We don't create the client here — it's injected.
                         This is "dependency injection" at the service level.
        """
        self._redis = redis_client

    def _make_key(self, user_id: str) -> str:
        """
        Build the Redis key for a user's session.

        Args:
            user_id: A unique identifier for the user.
                    For WhatsApp: the phone number (e.g., "+2348012345678")
                    For Telegram: the chat ID as string (e.g., "123456789")

        Returns:
            Redis key string, e.g., "session:+2348012345678"

        WHY A PREFIX?
        Namespacing keys prevents accidental collisions.
        If you later store user preferences in Redis as "user:{id}",
        there's no confusion with "session:{id}".
        """
        return f"{KEY_PREFIX}:{user_id}"

    async def get(self, user_id: str) -> dict[str, Any]:
        """
        Retrieve a user's session from Redis.

        Args:
            user_id: The user's unique identifier.

        Returns:
            The session data dict, or an empty dict {} if no session exists.

        WHY RETURN EMPTY DICT INSTEAD OF NONE?
        An empty dict is safe to access without None checks:
            session = await session_service.get(user_id)
            current_step = session.get("step", "GREETING")  # Works even if empty
        Returning None would require:
            if session is not None:
                current_step = session.get("step", "GREETING")
        The empty dict is simpler and less error-prone.

        HOW IT WORKS:
        1. Build the Redis key: "session:{user_id}"
        2. Call Redis GET command → returns JSON string or None
        3. If None (no session) → return {}
        4. Otherwise → parse JSON string to dict and return
        """
        key = self._make_key(user_id)

        try:
            # redis.asyncio returns strings (because decode_responses=True in main.py)
            # or None if the key doesn't exist.
            raw_data = await self._redis.get(key)

            if raw_data is None:
                # No session found — this is a new user or expired session.
                log.debug("Session not found (new or expired)", user_id=user_id)
                return {}

            # Parse the JSON string back into a Python dict.
            # json.loads converts: '{"flow": "onboarding", "step": "GREETING"}'
            # into: {"flow": "onboarding", "step": "GREETING"}
            session_data = json.loads(raw_data)
            log.debug(
                "Session retrieved",
                user_id=user_id,
                flow=session_data.get("flow"),
                step=session_data.get("step"),
            )
            return session_data

        except json.JSONDecodeError as e:
            # The data in Redis is not valid JSON. This shouldn't happen normally,
            # but could if data was manually modified in Redis (e.g., via Redis CLI).
            # Return empty dict to give the user a fresh start.
            log.error(
                "Session data is not valid JSON — clearing corrupted session",
                user_id=user_id,
                error=str(e),
            )
            # Delete the corrupted session to prevent this error on every message.
            await self.delete(user_id)
            return {}

        except Exception as e:
            # Redis connection error or other unexpected issue.
            # Log and return empty dict — don't crash the request.
            log.error(
                "Failed to get session from Redis",
                user_id=user_id,
                error=str(e),
                exc_info=True,
            )
            return {}

    async def set(self, user_id: str, data: dict[str, Any]) -> None:
        """
        Store or update a user's session in Redis.

        Args:
            user_id: The user's unique identifier.
            data: The session data to store. Must be JSON-serializable.
                 Convention: always include "flow" and "step" keys.

        TTL BEHAVIOR:
        Every call to set() resets the 24-hour TTL.
        This means the session expires 24 hours after the LAST activity,
        not 24 hours after session creation. Active users never time out mid-flow.

        EXAMPLE SESSION DATA:
        {
            "flow": "onboarding",
            "step": "COLLECT_NAME",
            "channel": "whatsapp",
            "data": {
                "phone": "+2348012345678",
                "consent_given": True,
                "engine_user_id": "usr_abc123def456"
            }
        }
        """
        key = self._make_key(user_id)

        try:
            # Convert Python dict to JSON string.
            # ensure_ascii=False allows Nigerian names with Unicode characters
            # (e.g., Yoruba characters with diacritics).
            json_data = json.dumps(data, ensure_ascii=False)

            # Store in Redis with TTL.
            # SETEX = SET with EXpiry. Atomic operation.
            # If we used SET then EXPIRE separately, there's a race condition
            # where the key exists without expiry for a brief moment.
            # SETEX avoids this.
            await self._redis.setex(
                name=key,
                time=SESSION_TTL_SECONDS,  # TTL in seconds
                value=json_data,
            )

            log.debug(
                "Session saved",
                user_id=user_id,
                flow=data.get("flow"),
                step=data.get("step"),
                ttl_hours=SESSION_TTL_SECONDS // 3600,
            )

        except Exception as e:
            log.error(
                "Failed to save session to Redis",
                user_id=user_id,
                error=str(e),
                exc_info=True,
            )
            # Re-raise here because a failed session save is serious —
            # the user's progress would be lost, leading to confusing UX.
            raise

    async def delete(self, user_id: str) -> None:
        """
        Delete a user's session from Redis.

        Called when:
        - User completes onboarding (session no longer needed)
        - User says "cancel" (abort current flow)
        - User's session is corrupted (reset to fresh state)
        - User is banned or suspended

        After deletion, the next message from this user returns an empty
        session dict from get(), effectively starting them fresh.
        """
        key = self._make_key(user_id)

        try:
            deleted_count = await self._redis.delete(key)

            if deleted_count > 0:
                log.info("Session deleted", user_id=user_id)
            else:
                # Key didn't exist — that's fine. delete() is idempotent.
                log.debug("Session delete called but key not found", user_id=user_id)

        except Exception as e:
            log.error(
                "Failed to delete session from Redis",
                user_id=user_id,
                error=str(e),
                exc_info=True,
            )
            # Don't re-raise. A failed delete is less critical —
            # the session will expire naturally via TTL.

    async def get_ttl(self, user_id: str) -> int:
        """
        Get remaining TTL (seconds) for a user's session.
        Returns -2 if key doesn't exist, -1 if no TTL set.
        Useful for showing users how long their session remains active.
        """
        key = self._make_key(user_id)
        return await self._redis.ttl(key)

    async def extend(self, user_id: str) -> None:
        """
        Reset the TTL on an existing session without modifying its data.
        Useful for "keep-alive" pings (e.g., in long identity verification waits).
        """
        key = self._make_key(user_id)
        await self._redis.expire(key, SESSION_TTL_SECONDS)
        log.debug("Session TTL extended", user_id=user_id, ttl_hours=SESSION_TTL_SECONDS // 3600)