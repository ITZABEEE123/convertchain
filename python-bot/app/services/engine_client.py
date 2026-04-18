from __future__ import annotations

import asyncio
from datetime import datetime, timezone
from typing import Any

import httpx
import structlog

log = structlog.get_logger()

DEFAULT_TIMEOUT_SECONDS = 10.0
MAX_RETRIES = 3
RETRY_BASE_DELAY_SECONDS = 0.5


class EngineError(Exception):
    def __init__(self, message: str, status_code: int | None = None):
        super().__init__(message)
        self.status_code = status_code


class EngineClient:
    def __init__(self, base_url: str, service_token: str):
        self._base_url = base_url.rstrip("/")
        self._client = httpx.AsyncClient(
            base_url=self._base_url,
            headers={
                "X-Service-Token": service_token,
                "Content-Type": "application/json",
                "Accept": "application/json",
            },
            timeout=httpx.Timeout(
                connect=5.0,
                read=DEFAULT_TIMEOUT_SECONDS,
                write=5.0,
                pool=5.0,
            ),
        )
        log.info("EngineClient initialized", base_url=self._base_url)

    async def close(self) -> None:
        await self._client.aclose()
        log.info("EngineClient closed")

    async def _request(
        self,
        method: str,
        path: str,
        *,
        json: dict | None = None,
        params: dict | None = None,
    ) -> dict[str, Any]:
        last_exception: Exception | None = None

        for attempt in range(1, MAX_RETRIES + 1):
            try:
                response = await self._client.request(method=method, url=path, json=json, params=params)

                try:
                    response_body = response.json()
                except Exception:
                    response_body = {"raw": response.text}

                if response.is_success:
                    return response_body

                if response.is_client_error:
                    error_message = self._extract_error_message(response_body)
                    raise EngineError(
                        f"Engine returned {response.status_code}: {error_message}",
                        status_code=response.status_code,
                    )

                last_exception = EngineError(
                    f"Engine server error {response.status_code}",
                    status_code=response.status_code,
                )
            except httpx.TimeoutException as exc:
                last_exception = EngineError(f"Request timed out: {exc}")
            except httpx.ConnectError as exc:
                last_exception = EngineError(f"Connection failed: {exc}")
            except EngineError:
                raise

            if attempt < MAX_RETRIES:
                await asyncio.sleep(RETRY_BASE_DELAY_SECONDS * (2 ** (attempt - 1)))

        raise last_exception or EngineError("All retries exhausted")

    @staticmethod
    def _extract_error_message(payload: dict[str, Any]) -> str:
        error_block = payload.get("error")
        if isinstance(error_block, dict):
            return str(error_block.get("message") or error_block.get("details") or "Client error")
        if isinstance(error_block, str):
            return error_block
        return str(payload.get("message") or payload.get("detail") or "Client error")

    async def create_user(self, channel_type: str, channel_user_id: str) -> dict[str, Any]:
        return await self._request(
            "POST",
            "/api/v1/users",
            json={
                "channel_type": channel_type,
                "channel_user_id": channel_user_id,
            },
        )

    async def record_consent(self, user_id: str) -> dict[str, Any]:
        consented_at = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
        return await self._request(
            "POST",
            "/api/v1/consent",
            json={
                "user_id": user_id,
                "consent_version": "v1",
                "consented_at": consented_at,
            },
        )

    async def submit_kyc(self, data: dict[str, Any]) -> dict[str, Any]:
        result = await self._request("POST", "/api/v1/kyc/submit", json=data)
        result.setdefault("kyc_id", data.get("user_id"))
        result.setdefault("status", "pending")
        return result

    async def get_kyc_status(self, user_id: str) -> dict[str, Any]:
        result = await self._request("GET", f"/api/v1/kyc/status/{user_id}")
        raw_status = str(result.get("kyc_status") or result.get("status") or "NOT_STARTED").upper()
        result["kyc_status"] = {
            "APPROVED": "approved",
            "PENDING": "pending",
            "REJECTED": "rejected",
            "IN_PROGRESS": "pending",
            "NOT_STARTED": "not_started",
        }.get(raw_status, raw_status.lower())
        return result

    async def get_quote(self, data: dict[str, Any]) -> dict[str, Any]:
        return await self._request("POST", "/api/v1/quotes", json=data)

    async def create_trade(self, data: dict[str, Any]) -> dict[str, Any]:
        result = await self._request("POST", "/api/v1/trades", json=data)
        result["status"] = self._normalize_trade_status(result.get("status"))
        return result

    async def get_trade_status(self, trade_id: str) -> dict[str, Any]:
        result = await self._request("GET", f"/api/v1/trades/{trade_id}")
        normalized_status = self._normalize_trade_status(result.get("status"))
        result["status"] = normalized_status

        if not result.get("required_confirmations"):
            result["required_confirmations"] = 2
        if result.get("confirmations") in (None, 0):
            result["confirmations"] = self._default_confirmations_for_status(normalized_status)

        return result

    async def list_banks(self) -> dict[str, Any]:
        return await self._request("GET", "/api/v1/banks")

    async def resolve_bank_account(self, data: dict[str, Any]) -> dict[str, Any]:
        return await self._request("POST", "/api/v1/bank-accounts/resolve", json=data)

    async def add_bank_account(self, data: dict[str, Any]) -> dict[str, Any]:
        return await self._request("POST", "/api/v1/bank-accounts", json=data)

    async def list_bank_accounts(self, user_id: str) -> dict[str, Any]:
        return await self._request("GET", f"/api/v1/bank-accounts/{user_id}")

    async def raise_dispute(self, data: dict[str, Any]) -> dict[str, Any]:
        return await self._request("POST", "/api/v1/disputes", json=data)

    @staticmethod
    def _normalize_trade_status(status: str | None) -> str:
        normalized = str(status or "").upper()
        return {
            "PENDING_DEPOSIT": "awaiting_deposit",
            "DEPOSIT_RECEIVED": "deposit_detected",
            "DEPOSIT_DETECTED": "deposit_detected",
            "DEPOSIT_CONFIRMED": "confirming",
            "CONVERSION_IN_PROGRESS": "confirming",
            "CONVERSION_COMPLETED": "confirming",
            "PAYOUT_PENDING": "confirming",
            "PAYOUT_COMPLETED": "settled",
            "COMPLETED": "settled",
            "CANCELLED": "failed",
            "QUOTE_EXPIRED": "failed",
            "FAILED": "failed",
            "DISPUTE": "failed",
        }.get(normalized, normalized.lower())

    @staticmethod
    def _default_confirmations_for_status(status: str) -> int:
        return {
            "awaiting_deposit": 0,
            "deposit_detected": 1,
            "confirming": 2,
            "settled": 2,
            "failed": 0,
        }.get(status, 0)