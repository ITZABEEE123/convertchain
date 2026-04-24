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
    def __init__(
        self,
        message: str,
        status_code: int | None = None,
        code: str | None = None,
        details: Any | None = None,
    ):
        super().__init__(message)
        self.status_code = status_code
        self.code = code
        self.details = details


class EngineClient:
    def __init__(self, base_url: str, service_token: str, admin_token: str | None = None):
        self._base_url = base_url.rstrip("/")
        self._admin_token = (admin_token or "").strip()
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
        headers: dict[str, str] | None = None,
    ) -> dict[str, Any]:
        last_exception: Exception | None = None

        for attempt in range(1, MAX_RETRIES + 1):
            try:
                response = await self._client.request(
                    method=method,
                    url=path,
                    json=json,
                    params=params,
                    headers=headers,
                )

                try:
                    response_body = response.json()
                except Exception:
                    response_body = {"raw": response.text}

                if response.is_success:
                    return response_body

                if response.is_client_error:
                    error_message = self._extract_error_message(response_body)
                    error_code = self._extract_error_code(response_body)
                    error_details = self._extract_error_details(response_body)
                    raise EngineError(
                        f"Engine returned {response.status_code}: {error_message}",
                        status_code=response.status_code,
                        code=error_code,
                        details=error_details,
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

    def _admin_headers(self) -> dict[str, str]:
        if not self._admin_token:
            raise EngineError("Admin API token is not configured for this bot instance.")
        return {"X-Admin-Token": self._admin_token}

    @staticmethod
    def _extract_error_message(payload: dict[str, Any]) -> str:
        error_block = payload.get("error")
        if isinstance(error_block, dict):
            return str(error_block.get("message") or error_block.get("details") or "Client error")
        if isinstance(error_block, str):
            return error_block
        return str(payload.get("message") or payload.get("detail") or "Client error")

    @staticmethod
    def _extract_error_code(payload: dict[str, Any]) -> str | None:
        error_block = payload.get("error")
        if isinstance(error_block, dict):
            raw = error_block.get("code")
            return str(raw).strip() if raw is not None else None
        raw = payload.get("code")
        if raw is None:
            return None
        return str(raw).strip() or None

    @staticmethod
    def _extract_error_details(payload: dict[str, Any]) -> Any | None:
        error_block = payload.get("error")
        if isinstance(error_block, dict):
            return error_block.get("details")
        return payload.get("details")

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
        raw_status = str(result.get("status") or "").upper()
        result["raw_status"] = raw_status
        result["status"] = self._normalize_trade_status(raw_status)
        return result

    async def confirm_trade(self, data: dict[str, Any]) -> dict[str, Any]:
        result = await self._request("POST", "/api/v1/trades/confirm", json=data)
        raw_status = str(result.get("status") or "").upper()
        result["raw_status"] = raw_status
        result["status"] = self._normalize_trade_status(raw_status)
        return result

    async def get_trade_status(self, trade_id: str) -> dict[str, Any]:
        result = await self._request("GET", f"/api/v1/trades/{trade_id}")
        raw_status = str(result.get("status") or "").upper()
        normalized_status = self._normalize_trade_status(raw_status)
        result["raw_status"] = raw_status
        result["status"] = normalized_status

        if not result.get("required_confirmations"):
            result["required_confirmations"] = 2
        if result.get("confirmations") in (None, 0):
            result["confirmations"] = self._default_confirmations_for_status(normalized_status)

        return result

    async def get_trade_status_context(self, user_id: str) -> dict[str, Any]:
        result = await self._request("GET", f"/api/v1/users/{user_id}/trades/status-context")
        trade = result.get("trade")
        if isinstance(trade, dict):
            raw_status = str(trade.get("status") or "").upper()
            trade["raw_status"] = raw_status
            trade["status"] = self._normalize_trade_status(raw_status)
        return result

    async def get_latest_active_trade(self, user_id: str) -> dict[str, Any]:
        result = await self._request("GET", f"/api/v1/users/{user_id}/trades/active")
        raw_status = str(result.get("status") or "").upper()
        result["raw_status"] = raw_status
        result["status"] = self._normalize_trade_status(raw_status)
        return result

    async def get_trade_receipt(self, trade_id: str) -> dict[str, Any]:
        return await self._request("GET", f"/api/v1/trades/{trade_id}/receipt")

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

    async def setup_transaction_password(self, data: dict[str, Any]) -> dict[str, Any]:
        return await self._request("POST", "/api/v1/security/transaction-password/setup", json=data)

    async def get_deletion_quota(self, user_id: str) -> dict[str, Any]:
        return await self._request("GET", f"/api/v1/account/delete/quota/{user_id}")

    async def delete_account(self, data: dict[str, Any]) -> dict[str, Any]:
        return await self._request("POST", "/api/v1/account/delete", json=data)

    async def get_pending_notifications(self, channel: str, limit: int = 50) -> dict[str, Any]:
        return await self._request("GET", "/api/v1/notifications/pending", params={"channel": channel, "limit": limit})

    async def ack_notification(
        self,
        notification_id: str,
        *,
        delivered: bool,
        delivery_error: str = "",
        claim_token: str = "",
    ) -> dict[str, Any]:
        return await self._request(
            "POST",
            f"/api/v1/notifications/{notification_id}/ack",
            json={
                "delivered": delivered,
                "delivery_error": delivery_error,
                "claim_token": claim_token,
            },
        )

    async def list_admin_disputes(self, *, status: str | None = None, limit: int = 20) -> dict[str, Any]:
        params = {"limit": limit}
        if status:
            params["status"] = status
        return await self._request(
            "GET",
            "/api/v1/admin/disputes",
            params=params,
            headers=self._admin_headers(),
        )

    async def get_admin_dispute(self, identifier: str) -> dict[str, Any]:
        return await self._request(
            "GET",
            f"/api/v1/admin/disputes/{identifier}",
            headers=self._admin_headers(),
        )

    async def resolve_admin_dispute(
        self,
        identifier: str,
        *,
        resolution_mode: str,
        resolution_note: str = "",
        resolver: str = "",
    ) -> dict[str, Any]:
        return await self._request(
            "POST",
            f"/api/v1/admin/disputes/{identifier}/resolve",
            json={
                "resolution_mode": resolution_mode,
                "resolution_note": resolution_note,
                "resolver": resolver,
            },
            headers=self._admin_headers(),
        )

    async def get_provider_readiness(self) -> dict[str, Any]:
        return await self._request(
            "GET",
            "/api/v1/admin/providers/readiness",
            headers=self._admin_headers(),
        )

    @staticmethod
    def _normalize_trade_status(status: str | None) -> str:
        normalized = str(status or "").upper()
        return {
            "PENDING_DEPOSIT": "awaiting_deposit",
            "DEPOSIT_RECEIVED": "deposit_detected",
            "DEPOSIT_DETECTED": "deposit_detected",
            "DEPOSIT_CONFIRMED": "deposit_confirmed",
            "CONVERSION_IN_PROGRESS": "conversion_in_progress",
            "CONVERSION_COMPLETED": "conversion_completed",
            "PAYOUT_PENDING": "payout_processing",
            "PAYOUT_COMPLETED": "settled",
            "COMPLETED": "settled",
            "CANCELLED": "failed",
            "PAYOUT_FAILED": "failed",
            "QUOTE_EXPIRED": "failed",
            "FAILED": "failed",
            "DISPUTE": "needs_attention",
            "DISPUTE_CLOSED": "closed_without_payout",
        }.get(normalized, normalized.lower())

    @staticmethod
    def _default_confirmations_for_status(status: str) -> int:
        return {
            "awaiting_deposit": 0,
            "deposit_detected": 1,
            "deposit_confirmed": 2,
            "conversion_in_progress": 2,
            "conversion_completed": 2,
            "payout_processing": 2,
            "settled": 2,
            "failed": 0,
            "needs_attention": 2,
        }.get(status, 0)
