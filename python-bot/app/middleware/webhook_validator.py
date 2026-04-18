# app/middleware/webhook_validator.py
# ============================================================
# Webhook signature validation.
#
# SECURITY DESIGN PRINCIPLES FOLLOWED:
# 1. Validate BEFORE processing — don't touch business logic until auth passes
# 2. Constant-time comparison — prevents timing attacks (hmac.compare_digest)
# 3. Fail closed — if anything is wrong, return False (deny by default)
# 4. No secret leakage — never log the secret, never log full signatures
# 5. Strict input validation — reject malformed signatures early
# ============================================================

from __future__ import annotations

import hashlib
import hmac
import structlog

log = structlog.get_logger()


class WebhookValidator:
    """
    Validates webhook signatures from external services.

    This class is instantiated ONCE at startup (in main.py's lifespan)
    with the app's secrets. It is then injected into route handlers
    via FastAPI's dependency injection system.

    WHY A CLASS AND NOT A FUNCTION?
    A class lets us hold the secrets as instance variables, set once at startup.
    We don't have to pass `settings.whatsapp_app_secret` to every function call.
    """

    def __init__(self, whatsapp_secret: str):
        """
        Initialize the validator with application secrets.

        Args:
            whatsapp_secret: The WHATSAPP_APP_SECRET from your .env file.
                            This is the key used to validate Meta's HMAC signatures.
        """
        # Encode the secret to bytes immediately.
        # HMAC requires bytes, not strings. We do this once at startup
        # rather than on every request (micro-optimization that adds up).
        self._whatsapp_secret_bytes: bytes = whatsapp_secret.encode("utf-8")

    def verify_whatsapp(self, body: bytes, signature_header: str) -> bool:
        """
        Verify a WhatsApp Cloud API webhook signature.

        Meta computes HMAC-SHA256(key=app_secret, message=body) and sends
        the result as "sha256=<hex_digest>" in the X-Hub-Signature-256 header.

        Args:
            body: The raw request body bytes (read BEFORE JSON parsing).
                  Using the exact bytes Meta sent ensures our HMAC matches.
            signature_header: The X-Hub-Signature-256 header value,
                              e.g. "sha256=abc123def456..."

        Returns:
            True if the signature is valid (request is authentic).
            False if invalid (forged, tampered, or from wrong source).

        IMPORTANT: This method NEVER raises exceptions.
        It returns False for any invalid input.
        This is the "fail closed" security principle — on any doubt, deny.
        """
        try:
            # ── Step 1: Validate signature format ─────────────────────────
            # Meta always prefixes with "sha256=" — reject anything else.
            # This prevents attackers from sending a signature in a different
            # format that might bypass validation through parsing tricks.
            if not signature_header.startswith("sha256="):
                log.warning(
                    "WhatsApp signature missing 'sha256=' prefix",
                    # Only log a short prefix to avoid leaking signature values
                    header_prefix=signature_header[:15] if len(signature_header) >= 15 else signature_header,
                )
                return False

            # Extract the hex digest (everything after "sha256=")
            received_hex = signature_header[len("sha256="):]

            # Validate the hex string is the expected length.
            # SHA-256 output is 32 bytes = 64 hex characters.
            # A wrong-length signature is either malformed or an attack attempt.
            if len(received_hex) != 64:
                log.warning(
                    "WhatsApp signature has wrong length",
                    expected=64,
                    received_length=len(received_hex),
                )
                return False

            # Validate it's actually a valid hex string.
            # `bytes.fromhex()` raises ValueError if it contains non-hex chars.
            # An attacker might try to inject special characters.
            try:
                received_bytes = bytes.fromhex(received_hex)
            except ValueError:
                log.warning("WhatsApp signature contains non-hex characters")
                return False

            # ── Step 2: Compute the expected HMAC ─────────────────────────
            # We use our secret key and the raw body bytes.
            # hmac.new() creates an HMAC object.
            # digestmod=hashlib.sha256 specifies SHA-256 as the hash function.
            mac = hmac.new(
                key=self._whatsapp_secret_bytes,
                msg=body,
                digestmod=hashlib.sha256,
            )
            # .digest() returns the HMAC as raw bytes (32 bytes for SHA-256)
            computed_bytes = mac.digest()

            # ── Step 3: Compare using constant-time comparison ─────────────
            # hmac.compare_digest compares two byte strings in constant time.
            # This prevents timing attacks (see detailed explanation above).
            #
            # We compare bytes objects (not hex strings) because:
            # 1. It's slightly faster (no string conversion)
            # 2. It avoids any potential case-sensitivity issues with hex strings
            is_valid = hmac.compare_digest(computed_bytes, received_bytes)

            if not is_valid:
                log.warning(
                    "WhatsApp webhook signature validation FAILED — possible forgery attempt"
                )

            return is_valid

        except Exception as e:
            # Catch-all: if anything unexpected happens during validation,
            # log it and return False. NEVER let an exception bypass validation.
            log.error(
                "Unexpected error during WhatsApp signature validation",
                error=str(e),
                exc_info=True,
            )
            return False

    def verify_graph_finance(self, body: bytes, signature_header: str) -> bool:
        """
        Verify a Meta Graph API finance webhook signature.

        This is the same HMAC-SHA256 mechanism as WhatsApp, because both
        WhatsApp Cloud API and Meta's Graph API use the same signing scheme.

        This method is here for future use when ConvertChain integrates
        additional Meta financial products (e.g., Meta Pay notifications).

        Args:
            body: Raw request body bytes.
            signature_header: The X-Hub-Signature-256 header value.

        Returns:
            True if valid, False if not.
        """
        # The signing algorithm is identical to WhatsApp.
        # In the future, if Graph Finance uses a DIFFERENT secret,
        # add a `_graph_finance_secret_bytes` instance variable
        # and use it here instead of `_whatsapp_secret_bytes`.
        return self.verify_whatsapp(body, signature_header)