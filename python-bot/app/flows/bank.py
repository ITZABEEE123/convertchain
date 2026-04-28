from __future__ import annotations

import inspect
import re
from difflib import SequenceMatcher
from typing import Any

import structlog

from app.services.engine_client import EngineClient, EngineError
from app.services.session import SessionService

log = structlog.get_logger()

STEP_COLLECT_BANK_CODE = "COLLECT_BANK_CODE"
STEP_COLLECT_ACCOUNT_NUMBER = "COLLECT_ACCOUNT_NUMBER"
STEP_CONFIRM_BANK_ACCOUNT = "CONFIRM_BANK_ACCOUNT"

BANK_CODE_PATTERN = re.compile(r"^\d{3,6}$")
ACCOUNT_NUMBER_PATTERN = re.compile(r"^\d{10}$")
LABELED_BANK_CODE_PATTERN = re.compile(r"bank\s*code\s*[:=-]?\s*(\d{3,6})", re.IGNORECASE)
LABELED_ACCOUNT_NUMBER_PATTERN = re.compile(r"account\s*number\s*[:=-]?\s*(\d{10})", re.IGNORECASE)
STANDALONE_BANK_CODE_PATTERN = re.compile(r"\b(\d{3,6})\b")
STANDALONE_ACCOUNT_NUMBER_PATTERN = re.compile(r"\b(\d{10})\b")
USE_BANK_PATTERN = re.compile(r"^use\s+bank\s+(\d+)$", re.IGNORECASE)
CONFIRM_SAVE_KEYWORDS = {"yes", "y", "save", "confirm", "confirm save"}
RETRY_ACCOUNT_KEYWORDS = {"no", "n", "change", "edit", "retry", "different"}
CONFIRM_BANK_SUGGESTION_KEYWORDS = {"yes", "y"}
POPULAR_BANK_CHOICES = (
    ("zenith", "Zenith Bank", {"zenith", "zenith bank"}),
    ("gtbank", "GTBank", {"gtb", "gtbank", "guaranty trust", "guaranty trust bank", "guaranty"}),
    ("access", "Access Bank", {"access", "access bank"}),
    ("uba", "UBA", {"uba", "united bank for africa", "united bank africa"}),
    ("first", "First Bank", {"first bank", "firstbank", "fbn", "first bank nigeria"}),
    ("opay", "OPay", {"opay", "o pay", "paycom"}),
    ("palmpay", "PalmPay", {"palmpay", "palm pay"}),
    ("kuda", "Kuda", {"kuda", "kuda bank"}),
    ("moniepoint", "Moniepoint", {"moniepoint", "monie point"}),
    ("stanbic", "Stanbic IBTC", {"stanbic", "stanbic ibtc", "stanbic ibtc bank"}),
)
POPULAR_BANK_PRIORITY = {key: index for index, (key, _, _) in enumerate(POPULAR_BANK_CHOICES)}
BANK_NAME_ALIASES = {
    "058": {"gtbank", "gtb", "guaranty trust", "guaranty trust bank"},
    "044": {"access", "access bank"},
    "033": {"uba", "united bank for africa"},
    "011": {"first bank", "firstbank", "fbn"},
    "057": {"zenith", "zenith bank"},
    "035": {"wema", "wema bank", "alat", "alat by wema"},
    "214": {"fcmb", "first city monument bank"},
    "070": {"fidelity", "fidelity bank"},
    "032": {"union", "union bank"},
    "050": {"ecobank", "eco bank"},
    "076": {"polaris", "polaris bank"},
    "082": {"keystone", "keystone bank"},
    "221": {"stanbic", "stanbic ibtc", "stanbic ibtc bank"},
    "039": {"stanbic", "stanbic ibtc", "stanbic ibtc bank"},
    "301": {"jaiz", "jaiz bank"},
    "232": {"sterling", "sterling bank"},
    "305": {"opay", "o pay"},
    "796": {"moniepoint", "monie point"},
    "855": {"palmpay", "palm pay"},
    "999991": {"palmpay", "palm pay"},
    "999992": {"opay", "o pay"},
    "50211": {"kuda", "kuda bank"},
}
COMMON_BANK_WORDS = {"bank", "plc", "limited", "ltd", "microfinance", "mfb", "nigeria", "service", "services"}
BANK_TYPO_SUGGESTIONS = {
    "senith": "zenith",
    "zenit": "zenith",
}


class BankFlow:
    def __init__(
        self,
        session_service: SessionService,
        engine_client: EngineClient,
        channel: str,
    ):
        self._session = session_service
        self._engine = engine_client
        self._channel = channel

    async def start(self, user_id: str, session: dict) -> str:
        banks = await self._load_bank_directory()
        popular_banks = self._popular_bank_options(banks or [])
        session = self._clear_bank_state(session)
        session["flow"] = "bank"
        session["step"] = STEP_COLLECT_BANK_CODE
        session["bank_data"] = {"popular_banks": popular_banks}
        await self._session.set(user_id, session)

        popular_lines = []
        for index, bank in enumerate(popular_banks, start=1):
            popular_lines.append(f"{index}. {self._popular_display_label(bank)}")
        if not popular_lines:
            popular_lines = [f"{index}. {label}" for index, (_, label, _) in enumerate(POPULAR_BANK_CHOICES, start=1)]

        return (
            "*Add Bank Account*\n\n"
            "Choose one of these popular banks by typing the name or number:\n\n"
            + "\n".join(popular_lines)
            + "\n\n"
            "If your bank is not listed, just type the bank name.\n"
            "Examples: `Zenith`, `OPay`, `Moniepoint`, `Access`\n\n"
            "Type *cancel* anytime to stop."
        )

    async def handle_step(self, user_id: str, session: dict, text: str) -> str:
        step = session.get("step")

        if step == STEP_COLLECT_BANK_CODE:
            return await self._handle_collect_bank_code(user_id, session, text)
        if step == STEP_COLLECT_ACCOUNT_NUMBER:
            return await self._handle_collect_account_number(user_id, session, text)
        if step == STEP_CONFIRM_BANK_ACCOUNT:
            return await self._handle_confirm_bank_account(user_id, session, text)

        return "Unexpected bank setup state. Type *add bank* to restart."

    async def show_accounts(self, user_id: str, session: dict) -> str:
        engine_user_id = session.get("engine_user_id")
        if not engine_user_id:
            return "Account error. Type *hi* to refresh your account session."

        accounts = await self._load_accounts(engine_user_id)
        if accounts is None:
            return "Could not load your bank accounts right now. Please try again."
        if not accounts:
            return (
                "*No bank account on file.*\n\n"
                "Type *add bank* to link one before trading."
            )

        session, selected_id = await self._ensure_selected_account(user_id, session, accounts)

        lines = ["*Your Bank Accounts*", ""]
        for index, account in enumerate(accounts, start=1):
            account_id = account.get("bank_account_id", "")
            bank_name = account.get("bank_name") or "Bank"
            account_number = account.get("account_number") or "******0000"
            account_name = account.get("account_name") or "Unnamed Account"
            marker = " (selected)" if account_id == selected_id else ""
            lines.append(f"{index}. {bank_name} {account_number}{marker}")
            lines.append(f"   {account_name}")

        lines.extend(
            [
                "",
                "Use `use bank 1` to select a different account.",
                "Use `add bank` to add another account.",
            ]
        )
        return "\n".join(lines)

    async def select_account(self, user_id: str, session: dict, text: str) -> str:
        match = USE_BANK_PATTERN.match(text.strip())
        if not match:
            return "Use the format `use bank 1` to choose one of your saved accounts."

        engine_user_id = session.get("engine_user_id")
        if not engine_user_id:
            return "Account error. Type *hi* to refresh your account session."

        accounts = await self._load_accounts(engine_user_id)
        if accounts is None:
            return "Could not load your bank accounts right now. Please try again."
        if not accounts:
            return "You do not have any saved bank accounts yet. Type *add bank* to add one."

        index = int(match.group(1)) - 1
        if index < 0 or index >= len(accounts):
            return f"I could not find bank number {index + 1}. Type *banks* to see your saved accounts."

        selected = accounts[index]
        session = self._clear_bank_state(session)
        session["selected_bank_account_id"] = selected.get("bank_account_id")
        await self._session.set(user_id, session)

        return (
            "Bank account selected.\n\n"
            f"Bank: *{selected.get('bank_name') or 'Bank'}*\n"
            f"Account: `{selected.get('account_number') or '******0000'}`\n"
            f"Name: *{selected.get('account_name') or 'Unnamed Account'}*\n\n"
            "You can trade immediately with `sell 0.25 BTC`."
        )

    async def _handle_collect_bank_code(self, user_id: str, session: dict, text: str) -> str:
        banks = await self._load_bank_directory()
        bank_data = session.setdefault("bank_data", {})
        normalized_text = text.strip().lower()

        log.info("bank_match_started", user_id=user_id[:6])
        if banks:
            stored_suggestions = bank_data.get("bank_suggestions") or []
            if text.strip().isdigit() and stored_suggestions:
                index = int(text.strip()) - 1
                if 0 <= index < len(stored_suggestions):
                    matched_bank = stored_suggestions[index]
                    suggestions: list[dict[str, Any]] = []
                    needs_confirmation = False
                else:
                    matched_bank, suggestions, needs_confirmation = None, [], False
            elif normalized_text in CONFIRM_BANK_SUGGESTION_KEYWORDS and bank_data.get("bank_suggestion"):
                matched_bank = bank_data["bank_suggestion"]
                suggestions = []
                needs_confirmation = False
            else:
                popular_banks = bank_data.get("popular_banks") or self._popular_bank_options(banks)
                matched_bank, suggestions, needs_confirmation = self._resolve_bank_input(banks, text, popular_banks=popular_banks)

            if matched_bank is None:
                if suggestions:
                    log.info("bank_match_ambiguous", user_id=user_id[:6], suggestion_count=len(suggestions))
                    bank_data["bank_suggestions"] = suggestions
                    bank_data.pop("bank_suggestion", None)
                    await self._session.set(user_id, session)
                    if needs_confirmation and len(suggestions) == 1:
                        bank_data["bank_suggestion"] = suggestions[0]
                        bank_data.pop("bank_suggestions", None)
                        await self._session.set(user_id, session)
                        return self._format_bank_confirmation(suggestions[0])
                    return self._format_bank_suggestions(suggestions)
                return (
                    "I could not find that bank in the current bank directory.\n\n"
                    "Type the bank name again, or choose one of the popular bank numbers from the Add Bank menu.\n"
                    "Examples: `Zenith`, `GTBank`, `OPay`"
                )
            selected_bank = self._normalize_bank_record(matched_bank)
            bank_code = selected_bank["resolve_bank_code"]
            bank_name = selected_bank["bank_name"]
            log.info("bank_match_succeeded", user_id=user_id[:6], bank_code=bank_code, bank_name=bank_name)
        else:
            bank_code = self._extract_bank_code(text)
            if not BANK_CODE_PATTERN.match(bank_code):
                return (
                    "Invalid bank input. Please type the bank name.\n\n"
                    "Examples: `Zenith`, `Access Bank`, `UBA`"
                )
            selected_bank = {
                "bank_code": bank_code,
                "resolve_bank_code": bank_code,
                "bank_name": "",
                "currency": "NGN",
            }

        if not selected_bank.get("resolve_bank_code"):
            return (
                "I found that bank, but it is not currently available for account verification.\n\n"
                "Please type another bank name."
            )

        session["bank_data"] = {
            **bank_data,
            **selected_bank,
            "bank_code": selected_bank["resolve_bank_code"],
            "bank_name": selected_bank.get("bank_name", ""),
        }
        session["bank_data"].pop("bank_suggestions", None)
        session["bank_data"].pop("bank_suggestion", None)
        session["step"] = STEP_COLLECT_ACCOUNT_NUMBER
        await self._session.set(user_id, session)

        bank_label = f"*{selected_bank.get('bank_name') or 'that bank'}*"
        return (
            f"I found {bank_label}. Now send your 10-digit account number.\n\n"
            "Example: `1234567890`"
        )

    async def _handle_collect_account_number(self, user_id: str, session: dict, text: str) -> str:
        account_number = self._extract_account_number(text)
        if not ACCOUNT_NUMBER_PATTERN.match(account_number):
            return "Invalid account number. Please send exactly 10 digits."

        engine_user_id = session.get("engine_user_id")
        if not engine_user_id:
            return "Account error. Type *hi* to refresh your account session."

        bank_code = session.get("bank_data", {}).get("bank_code", "")
        bank_name = session.get("bank_data", {}).get("bank_name", "")

        try:
            resolution = await self._engine.resolve_bank_account(
                {
                    "user_id": engine_user_id,
                    "provider_bank_id": session.get("bank_data", {}).get("provider_bank_id", ""),
                    "bank_code": session.get("bank_data", {}).get("resolve_bank_code") or bank_code,
                    "bank_name": bank_name,
                    "account_number": account_number,
                    "currency": "NGN",
                }
            )
        except EngineError as exc:
            log.warning(
                "bank_account_resolve_failed",
                error_code=exc.code,
                status_code=exc.status_code,
                user_id=user_id[:6],
                account_last4=account_number[-4:],
                bank_code=bank_code,
            )
            return self._bank_resolve_error_message(exc)

        session.setdefault("bank_data", {})["bank_code"] = resolution.get("bank_code") or bank_code
        session["bank_data"]["resolve_bank_code"] = resolution.get("bank_code") or session["bank_data"].get("resolve_bank_code") or bank_code
        session["bank_data"]["bank_name"] = resolution.get("bank_name") or bank_name or "Bank"
        session["bank_data"]["account_number"] = account_number
        session["bank_data"]["account_name"] = resolution.get("account_name") or ""
        session["step"] = STEP_CONFIRM_BANK_ACCOUNT
        await self._session.set(user_id, session)

        account_name = session["bank_data"].get("account_name") or "Unknown Name"
        bank_name = session["bank_data"].get("bank_name") or "Bank"
        masked_number = self._mask_account_number(session["bank_data"].get("account_number") or account_number)

        return (
            "I found this account:\n\n"
            f"Bank: *{bank_name}*\n"
            f"Account Name: *{account_name}*\n"
            f"Account Number: `{masked_number}`\n\n"
            "Confirm?\n\n"
            "Type *YES* to save this bank account, or *NO* to enter a different account number."
        )

    async def _handle_confirm_bank_account(self, user_id: str, session: dict, text: str) -> str:
        response = text.strip().lower()
        if response in RETRY_ACCOUNT_KEYWORDS:
            session["step"] = STEP_COLLECT_ACCOUNT_NUMBER
            session.get("bank_data", {}).pop("account_number", None)
            session.get("bank_data", {}).pop("account_name", None)
            await self._session.set(user_id, session)
            return (
                "Okay, send the 10-digit account number again.\n\n"
                "Example: `1234567890`"
            )

        if response not in CONFIRM_SAVE_KEYWORDS:
            return "Please type *YES* to save this bank account, or *NO* to change the account number."

        engine_user_id = session.get("engine_user_id")
        if not engine_user_id:
            return "Account error. Type *hi* to refresh your account session."

        bank_data = session.get("bank_data", {})
        try:
            account = await self._engine.add_bank_account(
                {
                    "user_id": engine_user_id,
                    "provider_bank_id": bank_data.get("provider_bank_id", ""),
                    "bank_code": bank_data.get("resolve_bank_code") or bank_data.get("bank_code", ""),
                    "bank_name": bank_data.get("bank_name", ""),
                    "account_number": bank_data.get("account_number", ""),
                    "account_name": bank_data.get("account_name", ""),
                    "currency": "NGN",
                }
            )
        except EngineError as exc:
            log.warning("bank_account_save_failed", error_code=exc.code, status_code=exc.status_code, user_id=user_id[:6])
            return (
                "Could not save that verified bank account.\n\n"
                "Please try again in a moment."
            )

        session = self._clear_bank_state(session)
        session["selected_bank_account_id"] = account.get("bank_account_id")
        await self._session.set(user_id, session)

        bank_name = account.get("bank_name") or bank_data.get("bank_name") or "Bank"
        masked_number = account.get("account_number") or self._mask_account_number(bank_data.get("account_number", ""))
        account_name = account.get("account_name") or bank_data.get("account_name") or "Unnamed Account"

        return (
            "Bank account added and selected.\n\n"
            f"Bank: *{bank_name}*\n"
            f"Account: `{masked_number}`\n"
            f"Name: *{account_name}*\n\n"
            "You can trade immediately. Examples:\n"
            "- `sell 0.25 BTC`\n"
            "- `sell 100 USDT`\n\n"
            "Type *banks* anytime to review your saved accounts."
        )

    async def _load_accounts(self, engine_user_id: str) -> list[dict] | None:
        try:
            response = await self._engine.list_bank_accounts(engine_user_id)
        except EngineError as exc:
            log.error("Failed to load bank accounts", error=str(exc), engine_user_id=engine_user_id)
            return None
        return response.get("accounts", [])

    async def _load_bank_directory(self) -> list[dict] | None:
        try:
            response = self._engine.list_banks()
            if inspect.isawaitable(response):
                response = await response
        except EngineError as exc:
            log.warning("Failed to load bank directory", error=str(exc))
            return None
        if not isinstance(response, dict):
            return None
        return response.get("banks", [])

    async def _ensure_selected_account(
        self,
        user_id: str,
        session: dict,
        accounts: list[dict],
    ) -> tuple[dict, str]:
        selected_id = session.get("selected_bank_account_id")
        if selected_id and any(account.get("bank_account_id") == selected_id for account in accounts):
            return session, selected_id

        selected_id = accounts[0].get("bank_account_id", "")
        session["selected_bank_account_id"] = selected_id
        await self._session.set(user_id, session)
        return session, selected_id

    @staticmethod
    def _clear_bank_state(session: dict) -> dict:
        session.pop("bank_data", None)
        if session.get("flow") == "bank":
            session["flow"] = None
        if session.get("step") in {STEP_COLLECT_BANK_CODE, STEP_COLLECT_ACCOUNT_NUMBER, STEP_CONFIRM_BANK_ACCOUNT}:
            session["step"] = None
        return session

    @staticmethod
    def _extract_bank_code(text: str) -> str:
        payload = text.strip()

        labeled = LABELED_BANK_CODE_PATTERN.search(payload)
        if labeled:
            return labeled.group(1)

        if BANK_CODE_PATTERN.match(payload):
            return payload

        standalone = STANDALONE_BANK_CODE_PATTERN.search(payload)
        if standalone:
            return standalone.group(1)

        return payload

    @classmethod
    def _resolve_bank_input(
        cls,
        banks: list[dict[str, Any]],
        text: str,
        *,
        popular_banks: list[dict[str, Any]] | None = None,
    ) -> tuple[dict[str, Any] | None, list[dict[str, Any]], bool]:
        payload = text.strip()
        if payload.isdigit() and popular_banks:
            index = int(payload) - 1
            if 0 <= index < len(popular_banks):
                return popular_banks[index], [], False

        code = cls._extract_bank_code(payload)
        if BANK_CODE_PATTERN.match(code):
            matched = [bank for bank in banks if code in cls._bank_codes(bank)]
            if len(matched) == 1:
                return matched[0], [], False
            if len(matched) > 1:
                return None, cls._sort_bank_matches(matched)[:5], False

        query = cls._normalize_bank_lookup(payload)
        if not query:
            return None, [], False
        if query in BANK_TYPO_SUGGESTIONS:
            target_key = BANK_TYPO_SUGGESTIONS[query]
            typo_matches = [bank for bank in banks if cls._popular_key_for_bank(bank) == target_key]
            if typo_matches:
                return None, [cls._sort_bank_matches(typo_matches)[0]], True

        exact_matches: list[dict[str, Any]] = []
        fuzzy_matches: list[tuple[float, dict[str, Any]]] = []
        for bank in banks:
            terms = cls._bank_terms(bank)
            if query in terms:
                exact_matches.append(bank)
                continue
            if any(term.startswith(query) or query in term for term in terms):
                fuzzy_matches.append((0.82, bank))
                continue
            best_ratio = max((SequenceMatcher(None, query, term).ratio() for term in terms), default=0.0)
            if best_ratio >= 0.72:
                fuzzy_matches.append((best_ratio, bank))

        if len(exact_matches) == 1:
            return exact_matches[0], [], False
        if len(exact_matches) > 1:
            ordered_exact = cls._sort_bank_matches(exact_matches)
            if cls._bank_sort_key(ordered_exact[0])[1] == 0 and all(cls._bank_sort_key(bank)[1] > 0 for bank in ordered_exact[1:]):
                return ordered_exact[0], [], False
            return None, ordered_exact[:5], False
        if fuzzy_matches:
            ordered_pairs = sorted(
                fuzzy_matches,
                key=lambda item: (
                    -item[0],
                    cls._bank_sort_key(item[1]),
                ),
            )
            ordered = [bank for _, bank in ordered_pairs]
            best_score = ordered_pairs[0][0]
            second_score = ordered_pairs[1][0] if len(ordered_pairs) > 1 else 0.0
            if best_score >= 0.80 and best_score-second_score >= 0.05:
                return None, [ordered[0]], True
            return None, cls._sort_bank_matches(ordered)[:5], False

        return None, [], False

    @staticmethod
    def _format_bank_suggestions(matches: list[dict[str, Any]]) -> str:
        lines = [
            "I found a few close matches.",
            "",
            "Reply with the number:",
        ]
        for index, bank in enumerate(matches, start=1):
            name = BankFlow._bank_display_name(bank)
            short_code = (bank.get("short_code") or "").strip()
            suffix = f" — code ending {short_code}" if short_code else ""
            lines.append(f"{index}. {name}{suffix}")
        lines.extend(
            [
                "",
                "You can also type the full bank name again if that is easier.",
            ]
        )
        return "\n".join(lines)

    @staticmethod
    def _format_bank_confirmation(bank: dict[str, Any]) -> str:
        return f"Did you mean *{BankFlow._bank_display_name(bank)}*?\n\nReply *YES* to continue, or type another bank name."

    @classmethod
    def _bank_terms(cls, bank: dict[str, Any]) -> set[str]:
        code = cls._bank_resolve_code(bank)
        bank_name = cls._bank_display_name(bank)
        slug = bank.get("slug") or ""
        normalized_name = cls._normalize_bank_lookup(bank_name)
        terms = {normalized_name, cls._normalize_bank_lookup(slug)}
        compact = normalized_name.replace(" ", "")
        if compact:
            terms.add(compact)
        for raw_code in cls._bank_codes(bank):
            terms.add(raw_code)
        for alias in BANK_NAME_ALIASES.get(code, set()):
            normalized_alias = cls._normalize_bank_lookup(alias)
            if normalized_alias:
                terms.add(normalized_alias)
                terms.add(normalized_alias.replace(" ", ""))
        popular_key = cls._popular_key_for_bank(bank)
        if popular_key:
            for key, _, aliases in POPULAR_BANK_CHOICES:
                if key == popular_key:
                    for alias in aliases:
                        normalized_alias = cls._normalize_bank_lookup(alias)
                        if normalized_alias:
                            terms.add(normalized_alias)
                            terms.add(normalized_alias.replace(" ", ""))
        return {term for term in terms if term}

    @staticmethod
    def _sort_bank_matches(matches: list[dict[str, Any]]) -> list[dict[str, Any]]:
        return sorted(
            matches,
            key=BankFlow._bank_sort_key,
        )

    @staticmethod
    def _normalize_bank_lookup(value: str) -> str:
        normalized = re.sub(r"[^a-z0-9]+", " ", value.lower())
        tokens = [token for token in normalized.split() if token not in COMMON_BANK_WORDS]
        return " ".join(tokens)

    @classmethod
    def _popular_bank_options(cls, banks: list[dict[str, Any]]) -> list[dict[str, Any]]:
        options: list[dict[str, Any]] = []
        for key, _, aliases in POPULAR_BANK_CHOICES:
            matches = [
                bank for bank in banks
                if cls._popular_key_for_bank(bank) == key
                or any(cls._normalize_bank_lookup(alias) in cls._bank_terms(bank) for alias in aliases)
            ]
            if matches:
                options.append(cls._sort_bank_matches(matches)[0])
        return options

    @staticmethod
    def _popular_display_label(bank: dict[str, Any]) -> str:
        key = BankFlow._popular_key_for_bank(bank)
        for candidate, label, _ in POPULAR_BANK_CHOICES:
            if candidate == key:
                return label
        return BankFlow._bank_display_name(bank)

    @staticmethod
    def _popular_key_for_bank(bank: dict[str, Any]) -> str:
        name = BankFlow._normalize_bank_lookup(BankFlow._bank_display_name(bank))
        slug = BankFlow._normalize_bank_lookup(bank.get("slug") or "")
        compact = name.replace(" ", "")
        for key, _, aliases in POPULAR_BANK_CHOICES:
            normalized_aliases = {BankFlow._normalize_bank_lookup(alias) for alias in aliases}
            if key in {name, slug, compact} or name in normalized_aliases or slug in normalized_aliases:
                return key
            if any(alias and (alias in name or alias in slug) for alias in normalized_aliases):
                return key
        return ""

    @staticmethod
    def _bank_sort_key(bank: dict[str, Any]) -> tuple[int, int, str, str]:
        name = BankFlow._bank_display_name(bank)
        lowered = name.lower()
        wallet_penalty = 1 if any(term in lowered for term in ("mobile", "wallet", "easy wallet")) else 0
        popular_key = BankFlow._popular_key_for_bank(bank)
        return (
            POPULAR_BANK_PRIORITY.get(popular_key, 999),
            wallet_penalty,
            BankFlow._normalize_bank_lookup(name),
            BankFlow._bank_resolve_code(bank),
        )

    @staticmethod
    def _bank_codes(bank: dict[str, Any]) -> set[str]:
        return {
            str(bank.get(key) or "").strip()
            for key in ("resolve_bank_code", "nip_code", "bank_code", "short_code")
            if str(bank.get(key) or "").strip()
        }

    @staticmethod
    def _bank_resolve_code(bank: dict[str, Any]) -> str:
        return (
            str(bank.get("resolve_bank_code") or "").strip()
            or str(bank.get("nip_code") or "").strip()
            or str(bank.get("bank_code") or "").strip()
        )

    @staticmethod
    def _bank_display_name(bank: dict[str, Any]) -> str:
        return str(bank.get("bank_name") or bank.get("display_name") or "Bank").strip()

    @staticmethod
    def _normalize_bank_record(bank: dict[str, Any]) -> dict[str, Any]:
        resolve_code = BankFlow._bank_resolve_code(bank)
        return {
            "provider_bank_id": str(bank.get("provider_bank_id") or bank.get("bank_id") or "").strip(),
            "bank_code": resolve_code,
            "bank_name": BankFlow._bank_display_name(bank),
            "slug": str(bank.get("slug") or "").strip(),
            "nip_code": str(bank.get("nip_code") or resolve_code).strip(),
            "short_code": str(bank.get("short_code") or "").strip(),
            "resolve_bank_code": resolve_code,
            "currency": str(bank.get("currency") or "NGN").strip() or "NGN",
        }

    @staticmethod
    def _extract_account_number(text: str) -> str:
        payload = text.strip()

        labeled = LABELED_ACCOUNT_NUMBER_PATTERN.search(payload)
        if labeled:
            return labeled.group(1)

        if ACCOUNT_NUMBER_PATTERN.match(payload):
            return payload

        standalone = STANDALONE_ACCOUNT_NUMBER_PATTERN.search(payload)
        if standalone:
            return standalone.group(1)

        return payload

    @staticmethod
    def _mask_account_number(account_number: str) -> str:
        if len(account_number) <= 4:
            return account_number
        return "******" + account_number[-4:]

    @staticmethod
    def _bank_resolve_error_message(exc: EngineError) -> str:
        if exc.code in {"invalid_account_number", "invalid_bank_code", "account_not_found"} or exc.status_code == 400:
            return (
                "I could not verify this account. Please check the bank and 10-digit account number.\n\n"
                "You can send the account number again, or type *cancel* to stop."
            )
        if exc.code in {"provider_unavailable", "provider_error"} or exc.status_code in {502, 503, 504}:
            return (
                "Bank verification is temporarily unavailable. Please try again shortly.\n\n"
                "Your bank details have not been saved yet."
            )
        return (
            "Could not verify that bank account right now.\n\n"
            "Please double-check the bank and account number, then try again."
        )
