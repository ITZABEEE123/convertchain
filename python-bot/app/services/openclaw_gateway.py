from __future__ import annotations

from typing import Any

import httpx


class OpenClawGatewayClient:
    """
    Minimal OpenClaw HTTP adapter.

    This keeps OpenClaw integration project-managed and explicit even when
    running OpenClaw as an external runtime on localhost.
    """

    def __init__(self, base_url: str, token: str | None = None, timeout: float = 20.0):
        self._base_url = base_url.rstrip("/")
        self._token = token
        self._http = httpx.AsyncClient(timeout=httpx.Timeout(timeout))

    def _headers(self) -> dict[str, str]:
        headers = {"Content-Type": "application/json"}
        if self._token:
            headers["Authorization"] = f"Bearer {self._token}"
        return headers

    async def close(self) -> None:
        await self._http.aclose()

    async def health(self) -> dict[str, Any]:
        """
        Best-effort health check. Different OpenClaw builds can expose
        different status routes, so we try a small fallback list.
        """
        endpoints = ("/health", "/healthz", "/api/health")
        for endpoint in endpoints:
            try:
                resp = await self._http.get(f"{self._base_url}{endpoint}", headers=self._headers())
                if resp.is_success:
                    return {"ok": True, "endpoint": endpoint, "status": resp.status_code}
            except Exception:
                continue
        return {"ok": False, "endpoint": None, "status": None}

    async def forward_inbound_event(self, payload: dict[str, Any]) -> httpx.Response:
        """
        Forward a normalized inbound event to OpenClaw.

        The exact endpoint may vary by deployment. Start with /api/events/inbound
        and adjust to your OpenClaw contract when you confirm it.
        """
        return await self._http.post(
            f"{self._base_url}/api/events/inbound",
            json=payload,
            headers=self._headers(),
        )
