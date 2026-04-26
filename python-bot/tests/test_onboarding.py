# tests/test_onboarding.py
# ============================================================
# Tests for the OnboardingFlow.
# All external dependencies (Go engine, Redis) are mocked.
# ============================================================

from __future__ import annotations

import json
from datetime import datetime, timezone
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from app.flows.onboarding import (
    OnboardingFlow,
    STEP_CONSENT,
    STEP_COLLECT_PHONE,
    STEP_COLLECT_NAME,
    STEP_COLLECT_DOB,
    STEP_COLLECT_NIN,
    STEP_COLLECT_BVN,
    STEP_UPLOAD_SELFIE,
    STEP_KYC_SUBMITTED,
)
from app.services.engine_client import EngineError


# ── Fixtures ─────────────────────────────────────────────────────────────────
# Fixtures are reusable test helpers decorated with @pytest.fixture.
# They are injected into test functions by name.

@pytest.fixture
def mock_session_service():
    """
    A fake SessionService that stores data in a plain dict.
    Uses AsyncMock so async methods work in async tests.
    """
    storage: dict = {}

    service = MagicMock()

    async def get(user_id):
        return storage.get(user_id, {})

    async def set(user_id, data):
        storage[user_id] = data.copy()

    async def delete(user_id):
        storage.pop(user_id, None)

    service.get = get
    service.set = set
    service.delete = delete
    service._storage = storage  # Expose for test assertions

    return service


@pytest.fixture
def mock_engine_client():
    """
    A fake EngineClient with AsyncMock methods.
    Default return values are set to valid success responses.
    Tests can override these as needed.
    """
    engine = MagicMock()

    # Default: create_user succeeds
    engine.create_user = AsyncMock(return_value={"user_id": "usr_test123"})

    # Default: record_consent succeeds
    engine.record_consent = AsyncMock(return_value={"consent_recorded": True})

    # Default: submit_kyc returns pending
    engine.submit_kyc = AsyncMock(return_value={"kyc_id": "kyc_test456", "status": "pending"})

    # Default: user has not completed KYC yet
    engine.get_kyc_status = AsyncMock(return_value={
        "kyc_status": "not_started",
    })

    return engine


@pytest.fixture
def onboarding_flow(mock_session_service, mock_engine_client):
    """Create an OnboardingFlow instance with mock dependencies."""
    return OnboardingFlow(
        session_service=mock_session_service,
        engine_client=mock_engine_client,
        channel="whatsapp",
    )


# ── Test: start() ──────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_start_creates_user_and_returns_welcome(onboarding_flow, mock_engine_client, mock_session_service):
    """start() should call create_user and return a welcome message."""
    result = await onboarding_flow.start(
        user_id="+2348012345678",
        sender_name="John",
    )

    # Verify create_user was called with correct arguments
    mock_engine_client.create_user.assert_called_once_with(
        channel_type="whatsapp",
        channel_user_id="+2348012345678",
    )

    # Verify the response contains expected content
    assert "Welcome to" in result
    assert "ConvertChain" in result
    assert "YES" in result  # Should ask user to type YES

    # Verify session was created
    session = await mock_session_service.get("+2348012345678")
    assert session["flow"] == "onboarding"
    assert session["engine_user_id"] == "usr_test123"




@pytest.mark.asyncio
async def test_start_resumes_verified_user(onboarding_flow, mock_session_service, mock_engine_client):
    mock_engine_client.get_kyc_status = AsyncMock(return_value={
        "kyc_status": "approved",
        "tier": 1,
        "transaction_password_set": True,
    })

    result = await onboarding_flow.start(
        user_id="+2348012345678",
        sender_name="John",
    )

    session = await mock_session_service.get("+2348012345678")
    assert session["onboarded"] is True
    assert session["step"] is None
    assert session["flow"] is None
    assert "already verified" in result.lower()


@pytest.mark.asyncio
async def test_collect_bvn_recovers_when_user_is_already_approved(onboarding_flow, mock_session_service, mock_engine_client):
    mock_engine_client.submit_kyc = AsyncMock(
        side_effect=EngineError("Engine returned 409: This user has already been KYC approved", status_code=409)
    )
    mock_engine_client.get_kyc_status = AsyncMock(return_value={
        "kyc_status": "approved",
        "tier": 1,
        "transaction_password_set": True,
    })

    await mock_session_service.set("+2348012345678", {
        "flow": "onboarding",
        "step": "COLLECT_BVN",
        "engine_user_id": "usr_test123",
        "channel": "whatsapp",
        "data": {
            "phone": "+2348012345678",
            "first_name": "John",
            "last_name": "Doe",
            "date_of_birth": "1990-01-15",
            "nin": "12345678901",
        },
    })

    session = await mock_session_service.get("+2348012345678")
    result = await onboarding_flow.handle_step(
        user_id="+2348012345678",
        session=session,
        text="22345678901",
        image_id=None,
    )

    updated_session = await mock_session_service.get("+2348012345678")
    assert updated_session["onboarded"] is True
    assert updated_session["step"] is None
    assert updated_session["flow"] is None
    assert "already verified" in result.lower()


@pytest.mark.asyncio
async def test_collect_bvn_starts_transaction_password_when_kyc_is_immediately_approved(
    onboarding_flow,
    mock_session_service,
    mock_engine_client,
):
    mock_engine_client.submit_kyc = AsyncMock(
        return_value={"kyc_id": "kyc_test456", "status": "APPROVED"}
    )

    await mock_session_service.set("+2348012345678", {
        "flow": "onboarding",
        "step": "COLLECT_BVN",
        "engine_user_id": "usr_test123",
        "channel": "whatsapp",
        "data": {
            "phone": "+2348012345678",
            "first_name": "John",
            "last_name": "Doe",
            "date_of_birth": "1990-01-15",
            "nin": "12345678901",
        },
    })

    session = await mock_session_service.get("+2348012345678")
    result = await onboarding_flow.handle_step(
        user_id="+2348012345678",
        session=session,
        text="22345678901",
        image_id=None,
    )

    updated_session = await mock_session_service.get("+2348012345678")
    assert updated_session["step"] == "SET_TX_PASSWORD"
    assert updated_session["flow"] == "onboarding"
    assert "transaction password" in result.lower()
    assert "step 7 of 9" in result.lower()
    assert "step 8 of 9" in result.lower()


@pytest.mark.asyncio
async def test_collect_bvn_returns_immediate_rejection_reason(
    onboarding_flow,
    mock_session_service,
    mock_engine_client,
):
    mock_engine_client.submit_kyc = AsyncMock(
        return_value={
            "kyc_id": "kyc_test456",
            "status": "REJECTED",
            "rejection_reason": "Name does not match BVN records",
        }
    )

    await mock_session_service.set("+2348012345678", {
        "flow": "onboarding",
        "step": "COLLECT_BVN",
        "engine_user_id": "usr_test123",
        "channel": "whatsapp",
        "data": {
            "phone": "+2348012345678",
            "first_name": "John",
            "last_name": "Doe",
            "date_of_birth": "1990-01-15",
            "nin": "12345678901",
        },
    })

    session = await mock_session_service.get("+2348012345678")
    result = await onboarding_flow.handle_step(
        user_id="+2348012345678",
        session=session,
        text="22345678901",
        image_id=None,
    )

    updated_session = await mock_session_service.get("+2348012345678")
    assert updated_session == {}
    assert "name does not match bvn records" in result.lower()


@pytest.mark.asyncio
async def test_collect_bvn_starts_sumsub_link_flow(
    onboarding_flow,
    mock_session_service,
    mock_engine_client,
):
    mock_engine_client.submit_kyc = AsyncMock(
        return_value={
            "kyc_id": "kyc_test456",
            "status": "PENDING",
            "provider": "sumsub",
            "provider_ref": "applicant-123",
            "verification_url": "https://api.sumsub.com/websdk/p/test-link",
        }
    )

    await mock_session_service.set("+2348012345678", {
        "flow": "onboarding",
        "step": "COLLECT_BVN",
        "engine_user_id": "usr_test123",
        "channel": "telegram",
        "data": {
            "phone": "+2348012345678",
            "first_name": "John",
            "last_name": "Doe",
            "date_of_birth": "1990-01-15",
            "nin": "12345678901",
        },
    })

    session = await mock_session_service.get("+2348012345678")
    result = await onboarding_flow.handle_step(
        user_id="+2348012345678",
        session=session,
        text="22345678901",
        image_id=None,
    )

    updated_session = await mock_session_service.get("+2348012345678")
    assert updated_session["step"] == STEP_KYC_SUBMITTED
    assert updated_session["data"]["kyc_provider"] == "sumsub"
    assert updated_session["data"]["sumsub_applicant_id"] == "applicant-123"
    assert "sumsub" in result.lower()
    assert "https://api.sumsub.com/websdk/p/test-link" in result


@pytest.mark.asyncio
async def test_collect_bvn_submits_go_kyc_payload_shape(
    onboarding_flow,
    mock_session_service,
    mock_engine_client,
):
    await mock_session_service.set("+2348012345678", {
        "flow": "onboarding",
        "step": "COLLECT_BVN",
        "engine_user_id": "11111111-1111-1111-1111-111111111111",
        "channel": "telegram",
        "data": {
            "phone": "+2348012345678",
            "first_name": "John",
            "last_name": "Doe",
            "date_of_birth": "1990-01-15",
            "nin": "12345678901",
        },
    })

    session = await mock_session_service.get("+2348012345678")
    await onboarding_flow.handle_step(
        user_id="+2348012345678",
        session=session,
        text="22345678901",
        image_id=None,
    )

    payload = mock_engine_client.submit_kyc.await_args.args[0]
    assert payload == {
        "user_id": "11111111-1111-1111-1111-111111111111",
        "first_name": "John",
        "last_name": "Doe",
        "date_of_birth": "1990-01-15",
        "phone_number": "+2348012345678",
        "nin": "12345678901",
        "bvn": "22345678901",
    }


@pytest.mark.asyncio
async def test_collect_bvn_provider_configuration_error_is_safe_for_user(
    onboarding_flow,
    mock_session_service,
    mock_engine_client,
):
    mock_engine_client.submit_kyc = AsyncMock(
        side_effect=EngineError(
            "Engine returned 502: KYC provider is not configured",
            status_code=502,
            code="PROVIDER_CONFIGURATION_ERROR",
        )
    )

    await mock_session_service.set("+2348012345678", {
        "flow": "onboarding",
        "step": "COLLECT_BVN",
        "engine_user_id": "11111111-1111-1111-1111-111111111111",
        "channel": "telegram",
        "data": {
            "phone": "+2348012345678",
            "first_name": "John",
            "last_name": "Doe",
            "date_of_birth": "1990-01-15",
            "nin": "12345678901",
        },
    })

    session = await mock_session_service.get("+2348012345678")
    result = await onboarding_flow.handle_step(
        user_id="+2348012345678",
        session=session,
        text="22345678901",
        image_id=None,
    )

    assert "temporarily unavailable" in result.lower()
    assert "sumsub" not in result.lower()


@pytest.mark.asyncio
async def test_collect_bvn_retries_after_user_not_found(
    onboarding_flow,
    mock_session_service,
    mock_engine_client,
):
    mock_engine_client.create_user = AsyncMock(return_value={"user_id": "22222222-2222-2222-2222-222222222222"})
    mock_engine_client.submit_kyc = AsyncMock(side_effect=[
        EngineError("Engine returned 404: User not found", status_code=404, code="USER_NOT_FOUND"),
        {"kyc_id": "kyc_test456", "status": "pending"},
    ])

    await mock_session_service.set("+2348012345678", {
        "flow": "onboarding",
        "step": "COLLECT_BVN",
        "engine_user_id": "11111111-1111-1111-1111-111111111111",
        "channel": "telegram",
        "data": {
            "phone": "+2348012345678",
            "first_name": "John",
            "last_name": "Doe",
            "date_of_birth": "1990-01-15",
            "nin": "12345678901",
        },
    })

    session = await mock_session_service.get("+2348012345678")
    await onboarding_flow.handle_step(
        user_id="+2348012345678",
        session=session,
        text="22345678901",
        image_id=None,
    )

    assert mock_engine_client.submit_kyc.await_count == 2
    retry_payload = mock_engine_client.submit_kyc.await_args.args[0]
    assert retry_payload["user_id"] == "22222222-2222-2222-2222-222222222222"


@pytest.mark.asyncio
async def test_confirm_transaction_password_reports_final_setup_step(
    onboarding_flow,
    mock_session_service,
    mock_engine_client,
):
    mock_engine_client.setup_transaction_password = AsyncMock(return_value={"configured": True})

    await mock_session_service.set("+2348012345678", {
        "flow": "onboarding",
        "step": "CONFIRM_TX_PASSWORD",
        "engine_user_id": "usr_test123",
        "channel": "whatsapp",
        "data": {
            "first_name": "John",
            "pending_transaction_password": "secret123",
            "tx_password_started_at": datetime.now(timezone.utc).isoformat(),
        },
    })

    session = await mock_session_service.get("+2348012345678")
    result = await onboarding_flow.handle_step(
        user_id="+2348012345678",
        session=session,
        text="secret123",
        image_id=None,
    )

    updated_session = await mock_session_service.get("+2348012345678")
    assert updated_session["onboarded"] is True
    assert updated_session["step"] is None
    assert "step 9 of 9" in result.lower()


@pytest.mark.asyncio
async def test_start_handles_engine_error(onboarding_flow, mock_engine_client):
    """start() should return a friendly error if the engine is unavailable."""
    mock_engine_client.create_user = AsyncMock(
        side_effect=EngineError("Connection failed", status_code=503)
    )

    result = await onboarding_flow.start(
        user_id="+2348012345678",
        sender_name="John",
    )

    assert "technical difficulties" in result.lower() or "try again" in result.lower()


# ── Test: CONSENT step ────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_consent_accepts_yes(onboarding_flow, mock_session_service):
    """CONSENT step should advance to COLLECT_PHONE when user types YES."""
    # Set up initial session (as if start() was already called)
    await mock_session_service.set("+2348012345678", {
        "flow": "onboarding",
        "step": "CONSENT",
        "engine_user_id": "usr_test123",
        "channel": "whatsapp",
        "data": {},
    })

    session = await mock_session_service.get("+2348012345678")
    result = await onboarding_flow.handle_step(
        user_id="+2348012345678",
        session=session,
        text="YES",
        image_id=None,
    )

    # Check step advanced
    updated_session = await mock_session_service.get("+2348012345678")
    assert updated_session["step"] == "COLLECT_PHONE"
    assert updated_session["data"]["consent_given"] is True

    # Check response mentions phone number
    assert "phone" in result.lower()


@pytest.mark.asyncio
async def test_consent_rejects_non_yes(onboarding_flow, mock_session_service):
    """CONSENT step should not advance if user doesn't type YES."""
    await mock_session_service.set("+2348012345678", {
        "flow": "onboarding",
        "step": "CONSENT",
        "engine_user_id": "usr_test123",
        "channel": "whatsapp",
        "data": {},
    })

    session = await mock_session_service.get("+2348012345678")
    result = await onboarding_flow.handle_step(
        user_id="+2348012345678",
        session=session,
        text="no thank you",
        image_id=None,
    )

    # Step should NOT have advanced
    updated_session = await mock_session_service.get("+2348012345678")
    assert updated_session["step"] == "CONSENT"

    # Response should prompt for YES
    assert "YES" in result


# ── Test: COLLECT_PHONE step ───────────────────────────────────────────────────

@pytest.mark.parametrize("phone, expected_valid", [
    ("08012345678", True),        # Valid MTN
    ("+2348012345678", True),     # Valid E.164
    ("09012345678", True),        # Valid Airtel
    ("07012345678", True),        # Valid Glo
    ("07123456789", True),        # Valid (071 prefix)
    ("12345678901", False),       # Invalid prefix (120...)
    ("0801234567", False),        # Too short (9 digits)
    ("08012345678901", False),    # Too long (13 digits)
    ("+1234567890123", False),    # US number, not Nigerian
    ("hello", False),             # Not a number at all
])
@pytest.mark.asyncio
async def test_collect_phone_validation(onboarding_flow, mock_session_service, phone, expected_valid):
    """COLLECT_PHONE should accept valid Nigerian numbers and reject invalid ones."""
    await mock_session_service.set("+2348012345678", {
        "flow": "onboarding",
        "step": "COLLECT_PHONE",
        "engine_user_id": "usr_test123",
        "channel": "whatsapp",
        "data": {"consent_given": True},
    })

    session = await mock_session_service.get("+2348012345678")
    await onboarding_flow.handle_step(
        user_id="+2348012345678",
        session=session,
        text=phone,
        image_id=None,
    )

    updated_session = await mock_session_service.get("+2348012345678")

    if expected_valid:
        assert updated_session["step"] == "COLLECT_NAME", f"Expected advance for phone: {phone}"
        assert "phone" in updated_session["data"]
    else:
        assert updated_session["step"] == "COLLECT_PHONE", f"Expected rejection for phone: {phone}"


# ── Test: COLLECT_NAME step ────────────────────────────────────────────────────

@pytest.mark.parametrize("name, expected_valid", [
    ("John Oluwaseun", True),
    ("Fatima Al-Rashid Musa", True),
    ("Chukwuemeka O'Brien", True),
    ("John", False),              # Single word — need first + last
    ("123 Numbers", False),       # Numbers not allowed
    ("  ", False),                # Whitespace only
])
@pytest.mark.asyncio
async def test_collect_name_validation(onboarding_flow, mock_session_service, name, expected_valid):
    """COLLECT_NAME should accept valid full names and reject single names."""
    await mock_session_service.set("+2348012345678", {
        "flow": "onboarding",
        "step": "COLLECT_NAME",
        "engine_user_id": "usr_test123",
        "channel": "whatsapp",
        "data": {"consent_given": True, "phone": "+2348012345678"},
    })

    session = await mock_session_service.get("+2348012345678")
    await onboarding_flow.handle_step(
        user_id="+2348012345678",
        session=session,
        text=name,
        image_id=None,
    )

    updated_session = await mock_session_service.get("+2348012345678")
    if expected_valid:
        assert updated_session["step"] == "COLLECT_DOB"
    else:
        assert updated_session["step"] == "COLLECT_NAME"


# ── Test: COLLECT_DOB step (age validation) ────────────────────────────────────

@pytest.mark.asyncio
async def test_collect_dob_rejects_underage():
    """COLLECT_DOB should reject users under 18."""
    from app.flows.onboarding import OnboardingFlow
    from unittest.mock import MagicMock, AsyncMock

    storage = {}
    session_service = MagicMock()
    session_service.get = AsyncMock(side_effect=lambda uid: storage.get(uid, {}))
    session_service.set = AsyncMock(side_effect=lambda uid, data: storage.update({uid: data.copy()}))

    engine = MagicMock()
    flow = OnboardingFlow(session_service=session_service, engine_client=engine, channel="whatsapp")

    from datetime import date
    today = date.today()
    # Generate a DOB that makes user 17 years old
    underage_dob = f"{today.day:02d}/{today.month:02d}/{today.year - 17}"

    session = {
        "flow": "onboarding",
        "step": "COLLECT_DOB",
        "engine_user_id": "usr_test123",
        "channel": "whatsapp",
        "data": {"phone": "+2348012345678", "first_name": "John", "last_name": "Doe"},
    }

    result = await flow.handle_step(
        user_id="+2348012345678",
        session=session,
        text=underage_dob,
        image_id=None,
    )

    assert "18" in result or "age" in result.lower()


# ── Test: KYC_SUBMITTED step ───────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_kyc_submitted_approved(onboarding_flow, mock_session_service, mock_engine_client):
    """KYC_SUBMITTED should mark session as onboarded when KYC is approved."""
    mock_engine_client.get_kyc_status = AsyncMock(return_value={
        "kyc_status": "approved",
        "tier": 1,
        "transaction_password_set": True,
    })

    await mock_session_service.set("+2348012345678", {
        "flow": "onboarding",
        "step": "KYC_SUBMITTED",
        "engine_user_id": "usr_test123",
        "channel": "whatsapp",
        "data": {"first_name": "John", "last_name": "Doe"},
    })

    session = await mock_session_service.get("+2348012345678")
    result = await onboarding_flow.handle_step(
        user_id="+2348012345678",
        session=session,
        text="status",
        image_id=None,
    )

    updated_session = await mock_session_service.get("+2348012345678")
    assert updated_session.get("onboarded") is True
    assert "Congratulations" in result or "verified" in result.lower()


@pytest.mark.asyncio
async def test_kyc_submitted_rejected(onboarding_flow, mock_session_service, mock_engine_client):
    """KYC_SUBMITTED should show rejection reason and clear session on reject."""
    mock_engine_client.get_kyc_status = AsyncMock(return_value={
        "kyc_status": "rejected",
        "rejection_reason": "NIN does not match provided name",
    })

    await mock_session_service.set("+2348012345678", {
        "flow": "onboarding",
        "step": "KYC_SUBMITTED",
        "engine_user_id": "usr_test123",
        "channel": "whatsapp",
        "data": {},
    })

    session = await mock_session_service.get("+2348012345678")
    result = await onboarding_flow.handle_step(
        user_id="+2348012345678",
        session=session,
        text="status",
        image_id=None,
    )

    assert "NIN does not match" in result or "Verification Failed" in result
