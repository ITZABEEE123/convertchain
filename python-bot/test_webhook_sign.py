from __future__ import annotations

import hashlib
import hmac
import json
import os
from pathlib import Path

from dotenv import load_dotenv


load_dotenv(Path(__file__).with_name(".env"))

APP_SECRET = os.getenv("WHATSAPP_APP_SECRET")
if not APP_SECRET:
    raise SystemExit("WHATSAPP_APP_SECRET is missing from .env")

payload = {
    "object": "whatsapp_business_account",
    "entry": [{
        "id": "BUSINESS_ACCOUNT_ID",
        "changes": [{
            "value": {
                "messaging_product": "whatsapp",
                "metadata": {
                    "display_phone_number": "15550783881",
                    "phone_number_id": "123456789",
                },
                "contacts": [{
                    "profile": {"name": "Test User"},
                    "wa_id": "2348012345678",
                }],
                "messages": [{
                    "from": "2348012345678",
                    "id": "wamid.test123",
                    "timestamp": "1704067200",
                    "text": {"body": "Hi"},
                    "type": "text",
                }],
            },
            "field": "messages",
        }],
    }],
}

body = json.dumps(payload, separators=(",", ":")).encode()
mac = hmac.new(APP_SECRET.encode(), body, hashlib.sha256)
signature = f"sha256={mac.hexdigest()}"

print(f"Payload body: {body.decode()}")
print(f"Signature: {signature}")
print()
print("Test command:")
print("curl -X POST http://localhost:8000/webhook/whatsapp \\")
print("  -H \"Content-Type: application/json\" \\")
print(f"  -H \"X-Hub-Signature-256: {signature}\" \\")
print(f"  -d '{body.decode()}'")
