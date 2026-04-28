# app/flows/onboarding.py
# ============================================================
# KYC onboarding flow — 10-step user registration process.
#
# DESIGN PATTERN: Session-based state machine
# The "step" key in Redis tells us where the user is in the flow.
# Each handle_step() call reads the step, processes input,
# updates state, and returns the prompt for the next step.
# ============================================================

from __future__ import annotations

import re
from datetime import date, datetime, timezone
from typing import Any

import structlog

from app.services.engine_client import EngineClient, EngineError
from app.services.session import SessionService

log = structlog.get_logger()

# ── Step constants ─────────────────────────────────────────────────────────────
# Using string constants instead of magic strings ("COLLECT_NAME") prevents
# typos that are silently wrong. If you misspell a constant name, Python
# gives you a NameError immediately.
STEP_GREETING = "GREETING"
STEP_CONSENT = "CONSENT"
STEP_COLLECT_PHONE = "COLLECT_PHONE"
STEP_COLLECT_NAME = "COLLECT_NAME"
STEP_COLLECT_DOB = "COLLECT_DOB"
STEP_COLLECT_NIN = "COLLECT_NIN"
STEP_COLLECT_BVN = "COLLECT_BVN"
STEP_UPLOAD_SELFIE = "UPLOAD_SELFIE"
STEP_KYC_SUBMITTED = "KYC_SUBMITTED"
STEP_SET_TX_PASSWORD = "SET_TX_PASSWORD"
STEP_CONFIRM_TX_PASSWORD = "CONFIRM_TX_PASSWORD"
STEP_COMPLETED = "COMPLETED"

TX_PASSWORD_SESSION_TIMEOUT_SECONDS = 300

# ── Validation patterns ────────────────────────────────────────────────────────
# Nigerian mobile phone prefixes (MTN, Airtel, Glo, 9mobile):
# 080, 081 (MTN), 090, 091 (Airtel), 070, 071 (Glo), 080 (Glo), 081 (9mobile)
NIGERIAN_PHONE_PATTERN = re.compile(r"^(?:\+234|0)(7[0-9]|8[01]|9[01])\d{8}$")

# NIN: 11 digits
NIN_PATTERN = re.compile(r"^\d{11}$")

# BVN: 11 digits
BVN_PATTERN = re.compile(r"^\d{11}$")

# Full name: at least two words (first + last name)
NAME_PATTERN = re.compile(r"^[A-Za-z\-\']+\s+[A-Za-z\-\']+(\s+[A-Za-z\-\']+)*$")

# Date of birth formats: DD/MM/YYYY, DD-MM-YYYY, YYYY-MM-DD
DOB_PATTERNS = [
    (re.compile(r"^(\d{2})[\/\-](\d{2})[\/\-](\d{4})$"), "dmy"),   # DD/MM/YYYY
    (re.compile(r"^(\d{4})[\/\-](\d{2})[\/\-](\d{2})$"), "ymd"),   # YYYY-MM-DD
]

# Minimum age for using the platform
MINIMUM_AGE_YEARS = 18


class OnboardingFlow:
    """
    10-step KYC onboarding flow for new ConvertChain users.

    STATE MACHINE:
    GREETING → CONSENT → COLLECT_PHONE → COLLECT_NAME →
    COLLECT_DOB → COLLECT_NIN → COLLECT_BVN → UPLOAD_SELFIE →
    KYC_SUBMITTED → COMPLETED

    The session dict structure during onboarding:
    {
        "flow": "onboarding",
        "step": "COLLECT_NAME",
        "channel": "whatsapp",
        "engine_user_id": "usr_abc123",  # Set after create_user
        "data": {
            "phone": "+2348012345678",
            "consent_given": True,
            "first_name": "John",
            "last_name": "Oluwaseun",
            "date_of_birth": "1990-01-15",
            "nin": "12345678901",
            "bvn": "12345678901",
            "kyc_id": "kyc_xyz789"
        }
    }
    """

    def __init__(
        self,
        session_service: SessionService,
        engine_client: EngineClient,
        channel: str,
    ):
        self._session = session_service
        self._engine = engine_client
        self._channel = channel

    async def start(self, user_id: str, sender_name: str) -> str:
        """
        Start the onboarding flow for a new user.
        Creates their record in the Go engine and shows the welcome message.
        """
        log.info("Starting onboarding", channel=self._channel, user_id_partial=user_id[:6])

        try:
            result = await self._engine.create_user(
                channel_type=self._channel,
                channel_user_id=user_id,
            )
            engine_user_id = result.get("user_id") or result.get("id")
        except EngineError as e:
            log.error("Failed to create user in engine", error=str(e))
            return (
                "We are experiencing technical difficulties. "
                "Please try again in a few minutes.\n"
                "If this persists, contact support@convertchain.com"
            )

        if not engine_user_id:
            log.error("Engine create_user returned no user id", channel=self._channel, user_id_partial=user_id[:6])
            return (
                "We could not start your account setup right now.\n"
                "Please try again in a few minutes."
            )

        try:
            kyc_status_result = await self._engine.get_kyc_status(engine_user_id)
        except EngineError as e:
            log.warning(
                "Failed to pre-check user KYC status during onboarding start",
                error=str(e),
                engine_user_id=engine_user_id,
            )
            kyc_status_result = {}

        if kyc_status_result.get("kyc_status") == "approved":
            existing_session = {
                "flow": "onboarding",
                "step": None,
                "channel": self._channel,
                "engine_user_id": engine_user_id,
                "onboarded": False,
                "data": {},
            }
            if kyc_status_result.get("transaction_password_set", True):
                await self._mark_user_onboarded(user_id, existing_session)
                return self._verified_main_menu(sender_name, recovered=True)
            return await self._begin_transaction_password_setup(
                user_id,
                existing_session,
                sender_name=sender_name,
                recovered=True,
            )

        await self._session.set(user_id, {
            "flow": "onboarding",
            "step": STEP_GREETING,
            "channel": self._channel,
            "engine_user_id": engine_user_id,
            "data": {},
        })

        name_part = f", {sender_name}" if sender_name and sender_name != "there" else ""

        return (
            f"Welcome to *ConvertChain*{name_part}!\n\n"
            "ConvertChain lets you convert your cryptocurrency to Nigerian Naira "
            "and receive payment directly to your bank account.\n\n"
            "----------------------\n"
            "*Before we start, please read our terms:*\n\n"
            "- You must be 18 years or older\n"
            "- You must provide accurate identity information (NIN + BVN)\n"
            "- Your data is processed under our Privacy Policy\n"
            "- ConvertChain complies with CBN AML/KYC regulations\n\n"
            "----------------------\n"
            "Type *YES* to accept our Terms of Service and Privacy Policy, "
            "and begin your account setup.\n\n"
            "Type *CANCEL* to exit at any time."
        )

    async def handle_step(
        self,
        user_id: str,
        session: dict,
        text: str,
        image_id: str | None,
    ) -> str:
        """
        Process user input for the current onboarding step.
        Routes to the specific step handler based on session state.
        """
        step = session.get("step", STEP_GREETING)

        step_handlers = {
            STEP_GREETING:      self._handle_greeting,
            STEP_CONSENT:       self._handle_consent,
            STEP_COLLECT_PHONE: self._handle_collect_phone,
            STEP_COLLECT_NAME:  self._handle_collect_name,
            STEP_COLLECT_DOB:   self._handle_collect_dob,
            STEP_COLLECT_NIN:   self._handle_collect_nin,
            STEP_COLLECT_BVN:   self._handle_collect_bvn,
            STEP_UPLOAD_SELFIE: self._handle_upload_selfie,
            STEP_KYC_SUBMITTED: self._handle_kyc_submitted,
            STEP_SET_TX_PASSWORD: self._handle_set_tx_password,
            STEP_CONFIRM_TX_PASSWORD: self._handle_confirm_tx_password,
        }

        handler = step_handlers.get(step)
        if not handler:
            log.warning("Unknown onboarding step", step=step, user_id=user_id[:6])
            return "Something went wrong. Type *hi* to restart."

        return await handler(user_id, session, text, image_id)

    # ═══════════════════════════════════════════════════════════════════════════
    # STEP HANDLERS
    # ═══════════════════════════════════════════════════════════════════════════

    async def _handle_greeting(
        self, user_id: str, session: dict, text: str, image_id: str | None
    ) -> str:
        """Step 1: GREETING — Show consent form, wait for YES."""
        # Advance to waiting for consent
        session["step"] = STEP_CONSENT
        await self._session.set(user_id, session)

        return (
            "📋 *Account Setup — Step 1 of 9*\n\n"
            "To use ConvertChain, we need to verify your identity as required "
            "by the Central Bank of Nigeria (CBN).\n\n"
            "We will collect:\n"
            "✓ Your phone number\n"
            "✓ Your full name\n"
            "✓ Date of birth\n"
            "✓ NIN (National Identification Number)\n"
            "✓ BVN (Bank Verification Number)\n"
            "✓ A selfie photo\n\n"
            "Your data is encrypted and stored securely.\n"
            "We will never sell your data to third parties.\n\n"
            "*Do you consent to the collection and processing of this data?*\n"
            "Type *YES* to continue."
        )

    async def _handle_consent(
        self, user_id: str, session: dict, text: str, image_id: str | None
    ) -> str:
        """Step 2: CONSENT — Accept YES, reject anything else."""
        if text.upper() != "YES":
            return (
                "⚠️ To use ConvertChain, you must accept our Terms.\n\n"
                "Type *YES* to accept and continue, or *CANCEL* to exit."
            )

        engine_user_id = session.get("engine_user_id")

        # Record consent in the Go engine (legal requirement — timestamped record)
        try:
            await self._engine.record_consent(engine_user_id)
        except EngineError as e:
            log.error("Failed to record consent", error=str(e), user_id=user_id[:6])
            return "⚠️ Technical error. Please try again."

        session["step"] = STEP_COLLECT_PHONE
        session["data"]["consent_given"] = True
        await self._session.set(user_id, session)

        return (
            "✅ Consent recorded. Thank you!\n\n"
            "📱 *Step 2 of 9 — Phone Number*\n\n"
            "Please enter your Nigerian mobile phone number.\n\n"
            "Example: *08012345678* or *+2348012345678*\n\n"
            "_Supported networks: MTN, Airtel, Glo, 9mobile_"
        )

    async def _handle_collect_phone(
        self, user_id: str, session: dict, text: str, image_id: str | None
    ) -> str:
        """Step 3: COLLECT_PHONE — Validate Nigerian phone number."""
        # Normalize: remove spaces, dashes
        phone = re.sub(r"[\s\-]", "", text)

        if not NIGERIAN_PHONE_PATTERN.match(phone):
            return (
                "⚠️ *Invalid phone number.*\n\n"
                "Please enter a valid Nigerian mobile number.\n"
                "Examples:\n"
                "  • 08012345678\n"
                "  • 09012345678\n"
                "  • +2348012345678\n\n"
                "Supported prefixes: 070, 080, 081, 090, 091"
            )

        # Normalize to E.164 format: +2348012345678
        if phone.startswith("0"):
            phone = "+234" + phone[1:]
        elif phone.startswith("234"):
            phone = "+" + phone

        session["step"] = STEP_COLLECT_NAME
        session["data"]["phone"] = phone
        await self._session.set(user_id, session)

        return (
            f"✅ Phone number: *{phone}*\n\n"
            "👤 *Step 3 of 9 — Full Name*\n\n"
            "Please enter your *full legal name* as it appears on your ID.\n\n"
            "Format: *First Last* (or *First Middle Last*)\n"
            "Example: *Chukwuemeka Okafor* or *Aminat Bello Suleiman*\n\n"
            "_Use your official name — it must match your NIN/BVN records._"
        )

    async def _handle_collect_name(
        self, user_id: str, session: dict, text: str, image_id: str | None
    ) -> str:
        """Step 4: COLLECT_NAME — Parse and validate full name."""
        name = text.strip()

        if not NAME_PATTERN.match(name):
            return (
                "⚠️ *Invalid name format.*\n\n"
                "Please enter your full name with at least a first and last name.\n"
                "Example: *John Oluwaseun* or *Fatima Al-Rashid Musa*\n\n"
                "Only letters, hyphens, and apostrophes are allowed."
            )

        # Split into first + last (+ optional middle)
        parts = name.split()
        first_name = parts[0].title()  # Capitalize first letter
        last_name = parts[-1].title()
        middle_name = " ".join(parts[1:-1]).title() if len(parts) > 2 else None

        session["step"] = STEP_COLLECT_DOB
        session["data"]["first_name"] = first_name
        session["data"]["last_name"] = last_name
        if middle_name:
            session["data"]["middle_name"] = middle_name
        await self._session.set(user_id, session)

        full_name = f"{first_name} {last_name}"
        if middle_name:
            full_name = f"{first_name} {middle_name} {last_name}"

        return (
            f"✅ Name: *{full_name}*\n\n"
            "🎂 *Step 4 of 9 — Date of Birth*\n\n"
            "Please enter your date of birth.\n\n"
            "Format: *DD/MM/YYYY*\n"
            "Example: *15/01/1990*\n\n"
            "_You must be 18 years or older to use ConvertChain._"
        )

    async def _handle_collect_dob(
        self, user_id: str, session: dict, text: str, image_id: str | None
    ) -> str:
        """Step 5: COLLECT_DOB — Parse date, validate age >= 18."""
        dob = self._parse_date(text.strip())

        if dob is None:
            return (
                "⚠️ *Invalid date format.*\n\n"
                "Please use DD/MM/YYYY format.\n"
                "Example: *15/01/1990*\n\n"
                "Other accepted formats:\n"
                "  • 15-01-1990\n"
                "  • 1990-01-15"
            )

        # Age validation
        today = date.today()
        age_years = (today - dob).days // 365

        if age_years < MINIMUM_AGE_YEARS:
            return (
                f"⚠️ *Age requirement not met.*\n\n"
                f"You must be at least {MINIMUM_AGE_YEARS} years old to use ConvertChain.\n\n"
                "Type *CANCEL* to exit."
            )

        if dob > today:
            return "⚠️ Date of birth cannot be in the future. Please check your entry."

        if age_years > 120:
            return "⚠️ Invalid date of birth. Please check your entry."

        dob_str = dob.isoformat()  # "1990-01-15" — ISO 8601 format

        session["step"] = STEP_COLLECT_NIN
        session["data"]["date_of_birth"] = dob_str
        await self._session.set(user_id, session)

        return (
            f"✅ Date of birth: *{dob.strftime('%d %B %Y')}*\n\n"
            "🪪 *Step 5 of 9 — National ID Number (NIN)*\n\n"
            "Please enter your 11-digit *National Identification Number (NIN)*.\n\n"
            "You can find your NIN:\n"
            "  • On your NIN slip from NIMC\n"
            "  • By dialling *346# on your phone\n"
            "  • On your National ID card\n\n"
            "Example: *12345678901*"
        )

    async def _handle_collect_nin(
        self, user_id: str, session: dict, text: str, image_id: str | None
    ) -> str:
        """Step 6: COLLECT_NIN — Validate 11-digit NIN."""
        nin = re.sub(r"\s", "", text)  # Remove any spaces

        if not NIN_PATTERN.match(nin):
            return (
                "⚠️ *Invalid NIN.*\n\n"
                "Your NIN must be exactly *11 digits* with no letters or spaces.\n"
                "Example: *12345678901*\n\n"
                "To get your NIN, dial *346# on your phone."
            )

        session["step"] = STEP_COLLECT_BVN
        session["data"]["nin"] = nin
        await self._session.set(user_id, session)

        return (
            "✅ NIN recorded.\n\n"
            "🏦 *Step 6 of 9 — Bank Verification Number (BVN)*\n\n"
            "Please enter your 11-digit *Bank Verification Number (BVN)*.\n\n"
            "You can find your BVN:\n"
            "  • Dial *565*0# on your registered bank phone\n"
            "  • Visit your bank branch\n"
            "  • Check your mobile banking app\n\n"
            "Example: *22345678901*\n\n"
            "_Your BVN is shared with NIBSS (Nigeria Inter-Bank Settlement "
            "System) for identity verification._"
        )

    async def _handle_collect_bvn(
        self, user_id: str, session: dict, text: str, image_id: str | None
    ) -> str:
        """Step 7: COLLECT_BVN - Validate BVN, submit KYC to Go engine."""
        bvn = re.sub(r"\s", "", text)

        if not BVN_PATTERN.match(bvn):
            return (
                "Invalid BVN.\n\n"
                "Your BVN must be exactly *11 digits*.\n"
                "Example: *22345678901*\n\n"
                "To get your BVN, dial *565*0# on your registered phone."
            )

        session["data"]["bvn"] = bvn

        kyc_payload = {
            "user_id": session["engine_user_id"],
            "first_name": session["data"]["first_name"],
            "last_name": session["data"]["last_name"],
            "date_of_birth": session["data"]["date_of_birth"],
            "phone_number": session["data"]["phone"],
            "nin": session["data"]["nin"],
            "bvn": bvn,
        }

        log.info(
            "Submitting KYC payload",
            user_id=session.get("engine_user_id"),
            payload_keys=sorted(kyc_payload.keys()),
        )

        kyc_result = None
        try:
            kyc_result = await self._engine.submit_kyc(kyc_payload)
        except EngineError as e:
            if e.status_code == 404 or str(e.code or "").upper() == "USER_NOT_FOUND":
                try:
                    user_result = await self._engine.create_user(
                        channel_type=self._channel,
                        channel_user_id=user_id,
                    )
                    recovered_engine_user_id = user_result.get("user_id") or user_result.get("id")
                    if not recovered_engine_user_id:
                        raise EngineError("Engine create_user returned no user id")
                    session["engine_user_id"] = recovered_engine_user_id
                    kyc_payload["user_id"] = recovered_engine_user_id
                    await self._session.set(user_id, session)
                    log.info(
                        "Retrying KYC after user re-resolution",
                        user_id=recovered_engine_user_id,
                        payload_keys=sorted(kyc_payload.keys()),
                    )
                    kyc_result = await self._engine.submit_kyc(kyc_payload)
                except EngineError as retry_error:
                    log.error(
                        "KYC submission failed after user re-resolution",
                        error_code=retry_error.code,
                        status_code=retry_error.status_code,
                        user_id=session.get("engine_user_id"),
                    )
                    return self._kyc_error_message(retry_error)

            if e.status_code == 409:
                try:
                    kyc_status_result = await self._engine.get_kyc_status(session["engine_user_id"])
                except EngineError as status_error:
                    log.error(
                        "Failed to recover after duplicate KYC submission",
                        error=str(status_error),
                        user_id=session.get("engine_user_id"),
                    )
                else:
                    if kyc_status_result.get("kyc_status") == "approved":
                        log.info(
                            "Recovered from duplicate KYC submission for already-approved user",
                            user_id=session.get("engine_user_id"),
                        )
                        display_name = session["data"].get("first_name", "")
                        if kyc_status_result.get("transaction_password_set", True):
                            await self._mark_user_onboarded(user_id, session)
                            return self._verified_main_menu(display_name, recovered=True)
                        return await self._begin_transaction_password_setup(
                            user_id,
                            session,
                            sender_name=display_name,
                            recovered=True,
                        )

            if kyc_result is None:
                log.error(
                    "KYC submission failed",
                    error_code=e.code,
                    status_code=e.status_code,
                    user_id=session.get("engine_user_id"),
                )
                return self._kyc_error_message(e)

        kyc_id = kyc_result.get("kyc_id")

        immediate_status = str(kyc_result.get("status") or "").upper()
        immediate_reason = (
            kyc_result.get("rejection_reason")
            or kyc_result.get("reason")
            or "Identity could not be verified"
        )

        if immediate_status == "APPROVED":
            session["data"]["kyc_id"] = kyc_id
            return await self._begin_transaction_password_setup(
                user_id,
                session,
                sender_name=session["data"].get("first_name", ""),
                recovered=False,
            )

        if immediate_status == "REJECTED":
            await self._session.delete(user_id)
            return (
                "Verification failed.\n\n"
                f"Reason: {immediate_reason}\n\n"
                "Please check your NIN, BVN, name, and date of birth, then type *hi* to try again."
            )

        verification_url = (
            kyc_result.get("verification_url")
            or kyc_result.get("applicant_verification_url")
            or kyc_result.get("websdk_url")
        )
        provider = str(kyc_result.get("provider") or "").lower()
        if provider == "sumsub" and verification_url:
            session["step"] = STEP_KYC_SUBMITTED
            session["data"]["kyc_id"] = kyc_id
            session["data"]["kyc_provider"] = "sumsub"
            session["data"]["sumsub_verification_url"] = verification_url
            if kyc_result.get("provider_ref"):
                session["data"]["sumsub_applicant_id"] = kyc_result["provider_ref"]
            await self._session.set(user_id, session)

            return (
                "BVN submitted. Your identity check is ready.\n\n"
                "*Step 7 of 9 - Sumsub Verification*\n\n"
                "Open this secure Sumsub link and complete the verification steps:\n"
                f"{verification_url}\n\n"
                "When you finish, return here and type *status*.\n\n"
                "If the link expires, type *status* and I will request a fresh link."
            )

        session["step"] = STEP_UPLOAD_SELFIE
        session["data"]["kyc_id"] = kyc_id
        await self._session.set(user_id, session)

        return (
            "BVN submitted. Identity verification is in progress.\n\n"
            "*Step 7 of 9 - Selfie Verification*\n\n"
            "Please take a clear selfie photo and send it here.\n\n"
            "*Photo requirements:*\n"
            "- Your face must be clearly visible\n"
            "- Good lighting (avoid dark or backlit photos)\n"
            "- No sunglasses, hats, or face coverings\n"
            "- Hold the photo upright\n\n"
            "_This is required for Tier 2 KYC compliance._"
        )

    async def _handle_upload_selfie(
        self, user_id: str, session: dict, text: str, image_id: str | None
    ) -> str:
        """Step 8: UPLOAD_SELFIE — Handle selfie image upload."""
        if not image_id:
            return (
                "📸 *Please send a selfie photo.*\n\n"
                "I'm waiting for an image, not a text message.\n\n"
                "*How to send:*\n"
                "• Tap the paperclip/attachment icon\n"
                "• Select 'Camera' or 'Photo'\n"
                "• Take or choose a photo of your face"
            )

        # Store the image ID — the Go engine will fetch it using the platform's media API
        session["step"] = STEP_KYC_SUBMITTED
        session["data"]["selfie_media_id"] = image_id
        session["data"]["selfie_channel"] = self._channel
        await self._session.set(user_id, session)

        # Note: In production, you would submit the selfie to the Go engine here.
        # The Go engine fetches the image using the WhatsApp/Telegram media download API,
        # then runs face-matching against the NIN/BVN profile photo.

        return (
            "✅ *Selfie received!*\n\n"
            "⏳ *Step 8 of 9 — Verification in Progress*\n\n"
            "We are verifying your identity. This usually takes *1–5 minutes*.\n\n"
            "We'll notify you here when your account is ready.\n\n"
            "You can type *status* to check your verification status at any time."
        )

    async def _handle_kyc_submitted(
        self, user_id: str, session: dict, text: str, image_id: str | None
    ) -> str:
        """Step 9: KYC_SUBMITTED — Poll KYC status from Go engine."""
        engine_user_id = session.get("engine_user_id")

        try:
            kyc_status_result = await self._engine.get_kyc_status(engine_user_id)
        except EngineError as e:
            log.error("Failed to get KYC status", error=str(e))
            return "⚠️ Could not check verification status. Please try again."

        kyc_status = kyc_status_result.get("kyc_status")

        if kyc_status == "approved":
            # KYC passed! Complete onboarding.
            first_name = session["data"].get("first_name", "")
            if kyc_status_result.get("transaction_password_set", True):
                await self._mark_user_onboarded(user_id, session)
                return (
                    f"🎉 *Congratulations, {first_name}! You're verified!*\n\n"
                    "Your identity has been confirmed. Your ConvertChain account is ready.\n\n"
                    "━━━━━━━━━━━━━━━━━━━━━━\n"
                    "💱 *Start Trading*\n\n"
                    "To sell crypto, type:\n"
                    "  `sell 0.25 BTC`\n"
                    "  `sell 1 ETH`\n"
                    "  `sell 100 USDT`\n\n"
                    "Supported coins: BTC, ETH, USDT, USDC, BNB\n\n"
                    "Type *help* for all available commands."
                )
            return await self._begin_transaction_password_setup(
                user_id,
                session,
                sender_name=first_name,
                recovered=False,
            )

        elif kyc_status == "rejected":
            reason = kyc_status_result.get("rejection_reason", "Identity could not be verified")
            # Clear the session — user needs to restart
            await self._session.delete(user_id)

            return (
                f"❌ *Verification Failed*\n\n"
                f"Reason: {reason}\n\n"
                "Common issues:\n"
                "• NIN/BVN mismatch with your provided name or date of birth\n"
                "• Selfie photo quality was too low\n"
                "• Information entered doesn't match government records\n\n"
                "You can try again by typing *hi*.\n"
                "For assistance, contact support@convertchain.com"
            )

        else:
            # Still pending
            verification_url = (
                kyc_status_result.get("verification_url")
                or session.get("data", {}).get("sumsub_verification_url")
            )
            provider = str(
                kyc_status_result.get("provider")
                or session.get("data", {}).get("kyc_provider")
                or ""
            ).lower()
            if provider == "sumsub" and verification_url:
                session.setdefault("data", {})["sumsub_verification_url"] = verification_url
                await self._session.set(user_id, session)
                return (
                    "Your Sumsub verification is still pending.\n\n"
                    "Open this secure link to continue or resume verification:\n"
                    f"{verification_url}\n\n"
                    "When you finish, return here and type *status*."
                )
            return (
                "⏳ *Verification still in progress.*\n\n"
                "Your identity is being verified against government databases. "
                "This can take 1–10 minutes.\n\n"
                "Please wait a few more minutes and type *status* to check again.\n\n"
                "_If it's been more than 30 minutes, please contact support._"
            )

    async def _handle_set_tx_password(
        self, user_id: str, session: dict, text: str, image_id: str | None
    ) -> str:
        if self._transaction_password_prompt_expired(session):
            return await self._reset_transaction_password_prompt(user_id, session)

        password = text.strip()
        if len(password) < 6:
            return (
                "Your transaction password must be at least 6 characters.\n\n"
                "Choose something you will remember but others cannot guess."
            )
        if " " in password:
            return "Transaction password cannot contain spaces. Please enter it again."

        session.setdefault("data", {})["pending_transaction_password"] = password
        session["data"]["tx_password_started_at"] = datetime.now(timezone.utc).isoformat()
        session["step"] = STEP_CONFIRM_TX_PASSWORD
        await self._session.set(user_id, session)

        return (
            "Confirm your transaction password.\n\n"
            "Send the same password again to finish securing your account."
        )

    async def _handle_confirm_tx_password(
        self, user_id: str, session: dict, text: str, image_id: str | None
    ) -> str:
        if self._transaction_password_prompt_expired(session):
            return await self._reset_transaction_password_prompt(user_id, session)

        confirmation = text.strip()
        pending_password = session.get("data", {}).get("pending_transaction_password", "")
        if not pending_password:
            return await self._reset_transaction_password_prompt(user_id, session)

        if confirmation != pending_password:
            session["step"] = STEP_SET_TX_PASSWORD
            session["data"].pop("pending_transaction_password", None)
            session["data"]["tx_password_started_at"] = datetime.now(timezone.utc).isoformat()
            await self._session.set(user_id, session)
            return (
                "Those passwords did not match.\n\n"
                "Let's try again. Enter a new transaction password."
            )

        try:
            await self._engine.setup_transaction_password(
                {
                    "user_id": session["engine_user_id"],
                    "transaction_password": pending_password,
                    "confirm_password": confirmation,
                }
            )
        except EngineError as exc:
            log.error("Failed to set transaction password", error=str(exc), user_id=session.get("engine_user_id"))
            return (
                "We could not save your transaction password right now.\n\n"
                "Please try again in a moment."
            )

        session.setdefault("data", {}).pop("pending_transaction_password", None)
        session["data"].pop("tx_password_started_at", None)
        await self._mark_user_onboarded(user_id, session)

        first_name = session.get("data", {}).get("first_name", "")
        return (
            "✅ *Step 9 of 9 — Account Secured*\n\n"
            f"Account secured successfully, {first_name or 'there'}.\n\n"
            "Your transaction password is now active and your account setup is complete.\n\n"
            "You can now:\n"
            "- `add bank`\n"
            "- `sell 0.25 BTC`\n"
            "- `status`\n"
            "- `help`"
        )

    # ═══════════════════════════════════════════════════════════════════════════
    # HELPERS
    # ═══════════════════════════════════════════════════════════════════════════

    async def _mark_user_onboarded(self, user_id: str, session: dict[str, Any]) -> None:
        session["flow"] = None
        session["step"] = None
        session["onboarded"] = True
        session["transaction_password_set"] = True
        session.setdefault("data", {}).pop("pending_transaction_password", None)
        session["data"].pop("tx_password_started_at", None)
        await self._session.set(user_id, session)

    async def _begin_transaction_password_setup(
        self,
        user_id: str,
        session: dict[str, Any],
        *,
        sender_name: str,
        recovered: bool,
    ) -> str:
        session["flow"] = "onboarding"
        session["step"] = STEP_SET_TX_PASSWORD
        session["onboarded"] = False
        session.setdefault("data", {})["tx_password_started_at"] = datetime.now(timezone.utc).isoformat()
        await self._session.set(user_id, session)

        intro = (
            f"Welcome back, {sender_name}.\n\n"
            if sender_name and sender_name != "there"
            else "Welcome back.\n\n"
        )
        if not recovered:
            intro = (
                "✅ *Step 7 of 9 — Identity Verified*\n\n"
                f"Congratulations, {sender_name or 'there'}! Your identity has been verified.\n\n"
            )

        return (
            f"{intro}"
            "*Step 8 of 9 — Secure Your Account*\n\n"
            "Before you can trade, set a transaction password.\n\n"
            "You will use this password whenever you confirm a trade or delete your account.\n\n"
            "Send a transaction password with at least 6 characters.\n"
            "This setup prompt expires after 5 minutes of inactivity."
        )

    async def _reset_transaction_password_prompt(self, user_id: str, session: dict[str, Any]) -> str:
        session.setdefault("data", {}).pop("pending_transaction_password", None)
        session["data"]["tx_password_started_at"] = datetime.now(timezone.utc).isoformat()
        session["step"] = STEP_SET_TX_PASSWORD
        await self._session.set(user_id, session)
        return (
            "Your transaction-password setup session expired.\n\n"
            "Please enter a new transaction password to continue."
        )

    @staticmethod
    def _transaction_password_prompt_expired(session: dict[str, Any]) -> bool:
        started_at = session.get("data", {}).get("tx_password_started_at")
        if not started_at:
            return False

        try:
            started = datetime.fromisoformat(started_at)
        except ValueError:
            return True

        return (datetime.now(timezone.utc) - started).total_seconds() > TX_PASSWORD_SESSION_TIMEOUT_SECONDS

    def _verified_main_menu(self, name: str, *, recovered: bool = False) -> str:
        greeting = f"Welcome back, {name}!" if name and name != "there" else "Welcome back!"
        status_line = "Your account is already verified." if recovered else "You are already verified."
        return (
            f"{greeting}\n\n"
            f"{status_line}\n\n"
            "*What would you like to do?*\n\n"
            "Trade\n"
            "  - sell 0.25 BTC\n"
            "  - sell 1 ETH\n"
            "  - sell 100 USDT\n\n"
            "Banks\n"
            "  - add bank\n"
            "  - banks\n"
            "  - use bank 1\n\n"
            "Check Status\n"
            "  - status\n\n"
            "Account\n"
            "  - delete account\n\n"
            "Help\n"
            "  - help"
        )

    @staticmethod
    def _kyc_error_message(exc: EngineError) -> str:
        code = str(exc.code or "").upper()
        if exc.status_code == 400 or code == "VALIDATION_ERROR":
            return (
                "Some verification details look missing or invalid.\n\n"
                "Please type *hi* to restart KYC and carefully review your name, date of birth, NIN, and BVN."
            )
        if exc.status_code == 404 or code == "USER_NOT_FOUND":
            return (
                "We could not find your account setup in the engine.\n\n"
                "Please type *hi* to restart account setup."
            )
        if exc.status_code == 409 or code == "DUPLICATE_KYC_SUBMISSION":
            return (
                "Your KYC submission already exists.\n\n"
                "Please type *status* to continue checking your verification."
            )
        if exc.status_code == 502 or code in {
            "PROVIDER_CONFIGURATION_ERROR",
            "PROVIDER_AUTH_FAILED",
            "PROVIDER_ERROR",
        }:
            return (
                "Verification service is temporarily unavailable.\n\n"
                "Please try again later."
            )
        return (
            "KYC submission failed.\n\n"
            "We could not verify your information at this time.\n"
            "Please try again in a few minutes.\n\n"
            "If this keeps happening, please contact support@convertchain.com"
        )

    def _parse_date(self, text: str) -> date | None:
        """
        Parse a date string into a Python date object.
        Supports: DD/MM/YYYY, DD-MM-YYYY, YYYY-MM-DD.

        Returns None if parsing fails.
        """
        for pattern, format_type in DOB_PATTERNS:
            match = pattern.match(text)
            if match:
                try:
                    if format_type == "dmy":
                        day, month, year = int(match.group(1)), int(match.group(2)), int(match.group(3))
                    else:  # ymd
                        year, month, day = int(match.group(1)), int(match.group(2)), int(match.group(3))

                    return date(year, month, day)
                except ValueError:
                    # Invalid calendar date (e.g., 31/02/YYYY)
                    return None

        return None

## The Session State Machine Visualized
