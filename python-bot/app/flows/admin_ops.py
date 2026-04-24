from __future__ import annotations

import re
from typing import Any

import structlog

from app.services.engine_client import EngineClient, EngineError

log = structlog.get_logger()

ADMIN_DISPUTES_PATTERN = re.compile(r"^admin\s+disputes(?:\s+(open|closed|all))?$", re.IGNORECASE)
ADMIN_DISPUTE_PATTERN = re.compile(r"^admin\s+dispute\s+(\S+)$", re.IGNORECASE)
ADMIN_RESOLVE_PATTERN = re.compile(
    r"^admin\s+resolve\s+(\S+)\s+(retry|close|force_paid|force-complete|force_complete)(?:\s+(.+))?$",
    re.IGNORECASE,
)
ADMIN_READINESS_PATTERN = re.compile(r"^admin\s+(readiness|providers|provider\s+readiness)$", re.IGNORECASE)


class AdminFlow:
    def __init__(self, engine_client: EngineClient):
        self._engine = engine_client

    @staticmethod
    def is_admin_command(text: str) -> bool:
        return text.strip().lower().startswith("admin")

    async def handle(self, *, text: str, admin_user_id: str, sender_name: str) -> str:
        text = text.strip()

        disputes_match = ADMIN_DISPUTES_PATTERN.match(text)
        if disputes_match:
            status_filter = (disputes_match.group(1) or "open").strip().lower()
            return await self._handle_list_disputes(status_filter=status_filter)

        readiness_match = ADMIN_READINESS_PATTERN.match(text)
        if readiness_match:
            return await self._handle_provider_readiness()

        dispute_match = ADMIN_DISPUTE_PATTERN.match(text)
        if dispute_match:
            return await self._handle_dispute_detail(identifier=dispute_match.group(1))

        resolve_match = ADMIN_RESOLVE_PATTERN.match(text)
        if resolve_match:
            identifier = resolve_match.group(1)
            resolution_mode = resolve_match.group(2)
            resolution_note = (resolve_match.group(3) or "").strip()
            resolver = f"telegram:{admin_user_id}"
            if sender_name.strip():
                resolver = f"{resolver}:{sender_name.strip()}"
            return await self._handle_resolve(
                identifier=identifier,
                resolution_mode=resolution_mode,
                resolution_note=resolution_note,
                resolver=resolver,
            )

        return self._help_text()

    async def _handle_list_disputes(self, *, status_filter: str) -> str:
        query_status = None if status_filter == "all" else status_filter.upper()
        try:
            response = await self._engine.list_admin_disputes(status=query_status, limit=10)
        except EngineError as exc:
            return self._format_engine_error("Could not load disputes right now.", exc)

        disputes = response.get("disputes", [])
        if not disputes:
            if status_filter == "all":
                return "*Admin Disputes*\n\nNo disputes were found."
            return "*Admin Disputes*\n\nNo open disputes right now."

        lines = [f"*Admin Disputes ({status_filter.title()})*"]
        for dispute in disputes:
            lines.append(
                (
                    f"- `{dispute.get('ticket_ref') or dispute.get('dispute_id')}`"
                    f" | `{dispute.get('trade_ref') or dispute.get('trade_id')}`"
                    f" | {str(dispute.get('status') or '-').upper()}"
                )
            )
            reason = str(dispute.get("reason") or "No reason recorded.").strip()
            lines.append(f"  {reason}")

        lines.append("")
        lines.append("Inspect one:")
        lines.append("- `admin dispute TRD-XXXXXXX`")
        lines.append("- `admin dispute DSP-XXXXXXX`")
        lines.append("")
        lines.append("Resolve one:")
        lines.append("- `admin resolve TRD-XXXXXXX retry`")
        lines.append("- `admin resolve TRD-XXXXXXX close`")
        lines.append("- `admin resolve TRD-XXXXXXX force_paid`")
        return "\n".join(lines)

    async def _handle_dispute_detail(self, *, identifier: str) -> str:
        try:
            dispute = await self._engine.get_admin_dispute(identifier)
        except EngineError as exc:
            return self._format_engine_error(f"Could not load dispute `{identifier}`.", exc)

        lines = [
            "*Dispute Detail*",
            "",
            f"Ticket: `{dispute.get('ticket_ref') or '-'}`",
            f"Dispute ID: `{dispute.get('dispute_id') or '-'}`",
            f"Trade Ref: `{dispute.get('trade_ref') or '-'}`",
            f"Trade ID: `{dispute.get('trade_id') or '-'}`",
            f"User ID: `{dispute.get('user_id') or '-'}`",
            f"Source: `{dispute.get('source') or '-'}`",
            f"Status: *{str(dispute.get('status') or '-').upper()}*",
            f"Reason: {dispute.get('reason') or 'No reason recorded.'}",
            f"Created At: {dispute.get('created_at') or '-'}",
            f"Updated At: {dispute.get('updated_at') or '-'}",
        ]
        if dispute.get("resolved_at"):
            lines.append(f"Resolved At: {dispute.get('resolved_at')}")
        if dispute.get("resolution_mode"):
            lines.append(f"Resolution Mode: `{dispute.get('resolution_mode')}`")
        if dispute.get("resolution_note"):
            lines.append(f"Resolution Note: {dispute.get('resolution_note')}")
        if dispute.get("resolver"):
            lines.append(f"Resolver: `{dispute.get('resolver')}`")
        return "\n".join(lines)

    async def _handle_resolve(
        self,
        *,
        identifier: str,
        resolution_mode: str,
        resolution_note: str,
        resolver: str,
    ) -> str:
        normalized_mode = self._normalize_resolution_mode(resolution_mode)
        if not normalized_mode:
            return self._help_text()

        try:
            dispute = await self._engine.resolve_admin_dispute(
                identifier,
                resolution_mode=normalized_mode,
                resolution_note=resolution_note,
                resolver=resolver,
            )
        except EngineError as exc:
            return self._format_engine_error(f"Could not resolve dispute `{identifier}`.", exc)

        outcome = {
            "retry_processing": "Trade has been requeued from the last safe processing stage.",
            "close_no_payout": "Dispute was closed without payout and no longer blocks account deletion.",
            "force_complete": "Trade was force-completed for administrative recovery.",
        }.get(normalized_mode, "Dispute resolution was recorded.")

        lines = [
            "*Dispute Resolved*",
            "",
            f"Ticket: `{dispute.get('ticket_ref') or dispute.get('dispute_id')}`",
            f"Trade Ref: `{dispute.get('trade_ref') or dispute.get('trade_id')}`",
            f"Status: *{str(dispute.get('status') or '-').upper()}*",
            f"Mode: `{dispute.get('resolution_mode') or normalized_mode}`",
            f"Resolver: `{dispute.get('resolver') or resolver}`",
            f"Result: {outcome}",
        ]
        if dispute.get("resolution_note"):
            lines.append(f"Note: {dispute.get('resolution_note')}")
        return "\n".join(lines)

    async def _handle_provider_readiness(self) -> str:
        try:
            report = await self._engine.get_provider_readiness()
        except EngineError as exc:
            return self._format_engine_error("Could not load provider readiness.", exc)

        graph = report.get("graph") or {}
        graph_details = graph.get("details") or {}
        lines = [
            "*Provider Readiness*",
            "",
            f"Overall: *{'HEALTHY' if report.get('overall_healthy') else 'ATTENTION REQUIRED'}*",
            f"Generated At: {report.get('generated_at') or '-'}",
            "",
            self._format_readiness_line("Graph", report.get("graph") or {}),
            self._format_readiness_line("Binance", report.get("binance") or {}),
            self._format_readiness_line("Bybit", report.get("bybit") or {}),
            self._format_readiness_line("SmileID", report.get("smileid") or {}),
            self._format_readiness_line("Sumsub", report.get("sumsub") or {}),
        ]

        destination = graph_details.get("recommended_webhook_destination")
        if destination:
            lines.extend(
                [
                    "",
                    "*Graph Webhook*",
                    f"- Public Base URL: `{graph_details.get('public_webhook_base_url') or '-'}`",
                    f"- Recommended Destination: `{destination}`",
                    f"- Secret Configured: {'yes' if graph_details.get('webhook_secret_configured') else 'no'}",
                ]
            )
        return "\n".join(lines)

    def _help_text(self) -> str:
        return (
            "*Admin Commands*\n\n"
            "- `admin disputes`\n"
            "- `admin disputes all`\n"
            "- `admin dispute TRD-XXXXXXX`\n"
            "- `admin readiness`\n"
            "- `admin resolve TRD-XXXXXXX retry`\n"
            "- `admin resolve TRD-XXXXXXX close`\n"
            "- `admin resolve TRD-XXXXXXX force_paid`\n\n"
            "Identifiers may be a dispute ID, ticket ref, trade ID, or trade ref."
        )

    @staticmethod
    def _normalize_resolution_mode(value: str) -> str:
        normalized = value.strip().lower().replace("-", "_")
        return {
            "retry": "retry_processing",
            "retry_processing": "retry_processing",
            "close": "close_no_payout",
            "close_no_payout": "close_no_payout",
            "force_paid": "force_complete",
            "force_complete": "force_complete",
        }.get(normalized, "")

    @staticmethod
    def _format_readiness_line(label: str, check: dict[str, Any]) -> str:
        status = "OK" if check.get("healthy") else "ISSUE"
        enabled = "enabled" if check.get("enabled") else "disabled"
        summary = str(check.get("summary") or "").strip() or "No summary returned."
        return f"- *{label}*: {status} ({enabled}) — {summary}"

    @staticmethod
    def _format_engine_error(prefix: str, exc: EngineError) -> str:
        message = prefix
        if exc.code:
            message += f"\n\nEngine code: `{exc.code}`"
        detail = ""
        if isinstance(exc.details, dict):
            reason = exc.details.get("reason")
            if reason:
                detail = str(reason)
        if not detail:
            detail = str(exc)
        return f"{message}\n\n{detail}"
