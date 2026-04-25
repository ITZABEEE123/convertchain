# Compliance Admin API Reference

## Overview

The compliance admin API enables manual case review, disposition, and legal/privacy operations. All endpoints require `X-Admin-Token` header and are restricted to authenticated admin users.

**Base URL:** `http://engine:9000/api/v1/admin/compliance`

---

## Authentication

All requests must include:

```bash
X-Admin-Token: <admin_token>
```

Token is configured via `ADMIN_TOKEN` environment variable. Rotate every 90 days.

---

## AML Case Management

### List AML Cases

**Endpoint:** `GET /compliance/cases`

**Query Parameters:**
- `status` (optional): Filter by case status (OPEN, IN_REVIEW, ESCALATED, DISMISSED, CONFIRMED, REFERRED_STR, CLOSED)
- `limit` (optional): Maximum 200 results; default 50

**Response:**
```json
[
  {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "case_ref": "AML-5E8400E29B4",
    "user_id": "11111111-1111-1111-1111-111111111111",
    "trade_id": "22222222-2222-2222-2222-222222222222",
    "quote_id": "33333333-3333-3333-3333-333333333333",
    "case_type": "VELOCITY_ALERT",
    "severity": "HIGH",
    "status": "OPEN",
    "reason": "High quote count in 15 minutes",
    "evidence": {
      "count_15m": 8,
      "threshold": 8,
      "user_id": "11111111-1111-1111-1111-111111111111"
    },
    "disposition": null,
    "disposition_note": null,
    "str_referral_metadata": null,
    "assigned_to": null,
    "created_at": "2026-04-25T14:30:00Z",
    "updated_at": "2026-04-25T14:30:00Z",
    "resolved_at": null
  }
]
```

**Status codes:**
- 200: Success
- 401: Invalid or missing X-Admin-Token
- 400: Invalid query parameters

---

### Get AML Case

**Endpoint:** `GET /compliance/cases/:id`

**Path Parameters:**
- `id`: Case ID (UUID) or case reference (e.g., `AML-5E8400E29B4`)

**Response:** Single case object (see List endpoint)

**Status codes:**
- 200: Success
- 401: Invalid token
- 404: Case not found

---

### Disposition AML Case

**Endpoint:** `POST /compliance/cases/:id/disposition`

**Path Parameters:**
- `id`: Case ID (UUID) or case reference

**Request Body:**
```json
{
  "status": "CONFIRMED",
  "disposition": "confirmed_suspicious",
  "disposition_note": "User trading pattern matches known structuring behavior. Recommend STR filing.",
  "str_referral_metadata": {
    "filing_bank": "Access Bank",
    "filing_reference": "STR-2026-04-25-001",
    "analyst_name": "John Doe",
    "recommendation": "file_str"
  },
  "actor": "compliance_analyst_1"
}
```

**Required Fields:**
- `status`: One of:
  - `OPEN` — Case still under initial review
  - `IN_REVIEW` — Active investigation
  - `ESCALATED` — Raised to compliance committee
  - `DISMISSED` — No action needed
  - `CONFIRMED` — Confirmed suspicious activity
  - `REFERRED_STR` — Referred to Financial Intelligence Unit (STR filing)
  - `CLOSED` — Final resolution recorded
- `disposition`: Human-readable outcome summary
- `actor`: Username or ID of analyst making the determination

**Optional Fields:**
- `disposition_note`: Detailed notes (max 2000 chars)
- `str_referral_metadata`: If status is `REFERRED_STR`, include:
  - `filing_bank`: Originating financial institution
  - `filing_reference`: Unique identifier for STR filing
  - `analyst_name`: Name of analyst
  - `recommendation`: `file_str` or `monitor`

**Response:**
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "case_ref": "AML-5E8400E29B4",
  "status": "CONFIRMED",
  "disposition": "confirmed_suspicious",
  "disposition_note": "User trading pattern matches known structuring behavior. Recommend STR filing.",
  "updated_at": "2026-04-25T14:35:00Z",
  "resolved_at": "2026-04-25T14:35:00Z"
}
```

**Status codes:**
- 200: Success
- 400: Invalid status or missing required fields
- 401: Invalid token
- 404: Case not found
- 409: Case already closed (immutable)

**Case Event Audit Trail:**

Every disposition creates an immutable event in `aml_case_events` table:
```sql
SELECT * FROM aml_case_events 
WHERE case_id = $1 
ORDER BY created_at DESC;
```

Fields logged:
- `actor` — Who made the decision
- `event_type` — 'CASE_DISPOSITIONED'
- `note` — Disposition note
- `evidence` — JSON with status and disposition
- `created_at` — Timestamp (immutable)

---

## Legal Launch Approvals

### Record Legal Sign-Off

**Endpoint:** `POST /compliance/legal-approvals`

**Request Body:**
```json
{
  "environment": "production",
  "approval_status": "APPROVED",
  "approved_by": "Jane Smith, General Counsel",
  "legal_memo_ref": "MEMO-2026-04-COMPLIANCE-LAUNCH",
  "notes": "Reviewed compliance control design. All requirements met. Ready for production go-live.",
  "evidence": {
    "review_date": "2026-04-24",
    "reviewers": ["jane.smith@company.com", "compliance.lead@company.com"],
    "risk_assessment": "LOW",
    "approved_limit_policy": "DEFAULT",
    "approved_screening_terms": "STANDARD_OFAC"
  },
  "signed_at": "2026-04-24T10:00:00Z"
}
```

**Required Fields:**
- `environment`: `staging` or `production`
- `approval_status`: `PENDING`, `APPROVED`, `REJECTED`, or `REVOKED`

**Optional Fields:**
- `approved_by`: Approving authority
- `legal_memo_ref`: Reference to legal memo or policy document
- `notes`: Detailed notes on approval decision
- `evidence`: Structured approval evidence (JSONB)
- `signed_at`: Signature timestamp (ISO 8601)

**Response:**
```json
{
  "id": "660e8400-e29b-41d4-a716-446655440001",
  "environment": "production",
  "approval_status": "APPROVED",
  "approved_by": "Jane Smith, General Counsel",
  "legal_memo_ref": "MEMO-2026-04-COMPLIANCE-LAUNCH",
  "created_at": "2026-04-24T10:00:00Z",
  "updated_at": "2026-04-24T10:00:00Z"
}
```

**Status codes:**
- 200: Success (creates or updates environment approval)
- 400: Invalid approval status
- 401: Invalid token

**Go-Live Gate:**

Before enabling `COMPLIANCE_MODE=enforce`, verify:
```sql
SELECT * FROM legal_launch_approvals 
WHERE environment = 'production' 
  AND approval_status = 'APPROVED';
```

At least one record must exist with status `APPROVED`.

---

## Data Protection Events

### Record Data Protection Event

**Endpoint:** `POST /compliance/data-protection/events`

**Request Body:**
```json
{
  "user_id": "11111111-1111-1111-1111-111111111111",
  "event_type": "DSAR_FULFILLED",
  "status": "COMPLETED",
  "reference": "DSAR-2026-04-001",
  "details": {
    "request_date": "2026-04-15",
    "fulfillment_date": "2026-04-22",
    "data_format": "json",
    "delivery_method": "secure_email",
    "recipient": "user@example.com"
  },
  "created_by": "privacy_officer_1"
}
```

**Required Fields:**
- `event_type`: One of:
  - `LAWFUL_BASIS_RECORDED` — Legal basis for processing recorded
  - `RETENTION_POLICY_RECORDED` — Retention period recorded
  - `DSAR_REQUESTED` — Data Subject Access Request received
  - `DSAR_FULFILLED` — DSAR fulfilled and data delivered
  - `ERASURE_REQUESTED` — Right to be forgotten request
  - `ERASURE_COMPLETED` — Account/data erased
  - `ANONYMIZATION_COMPLETED` — Data anonymized
  - `BREACH_INCIDENT_RECORDED` — Data breach recorded
  - `BREACH_RESPONSE_RECORDED` — Breach response action taken
- `status`: `OPEN`, `IN_PROGRESS`, `COMPLETED`, or `REJECTED`

**Optional Fields:**
- `user_id`: UUID of subject (nullable for BREACH events)
- `reference`: Request ID or reference number
- `details`: Structured event details (JSONB)
- `created_by`: Actor name or ID

**Response:**
```json
{
  "id": "770e8400-e29b-41d4-a716-446655440002",
  "user_id": "11111111-1111-1111-1111-111111111111",
  "event_type": "DSAR_FULFILLED",
  "status": "COMPLETED",
  "reference": "DSAR-2026-04-001",
  "created_at": "2026-04-22T14:30:00Z",
  "updated_at": "2026-04-22T14:30:00Z"
}
```

**Status codes:**
- 200: Success
- 400: Invalid event_type or status
- 401: Invalid token

**Audit Trail Retention:**

All data protection events are immutable. Query for DSAR/erasure history:
```sql
SELECT * FROM data_protection_events 
WHERE user_id = $1 AND event_type IN ('DSAR_REQUESTED', 'DSAR_FULFILLED', 'ERASURE_COMPLETED')
ORDER BY created_at DESC;
```

---

## Error Responses

All endpoints return consistent error format:

```json
{
  "ok": false,
  "error": {
    "code": "ERR_CODE",
    "message": "Human-readable error message",
    "details": null
  }
}
```

**Common Error Codes:**
- `ERR_INVALID_CASE_STATUS` — Status not in allowed list
- `ERR_CASE_NOT_FOUND` — Case ID/reference not found
- `ERR_UNAUTHORIZED` — Missing or invalid X-Admin-Token
- `ERR_VALIDATION` — Invalid request body
- `ERR_INTERNAL` — Server error

---

## Rate Limiting

Admin endpoints are **not rate-limited** but are logged for audit purposes.

All administrative actions generate audit events in:
- `aml_case_events` (for case dispositions)
- `data_protection_events` (for privacy operations)

---

## Integration Examples

### Example 1: Close a VELOCITY_ALERT as False Positive

```bash
#!/bin/bash
ADMIN_TOKEN="your-admin-token"
ENGINE="http://prod-engine:9000"
CASE_ID="AML-5E8400E29B4"

curl -X POST "$ENGINE/api/v1/admin/compliance/cases/$CASE_ID/disposition" \
  -H "X-Admin-Token: $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "status": "DISMISSED",
    "disposition": "confirmed_false_positive",
    "disposition_note": "User is known high-volume day trader. Pattern is legitimate.",
    "actor": "compliance_analyst_jane"
  }'
```

### Example 2: Record DSAR Fulfillment

```bash
ADMIN_TOKEN="your-admin-token"
ENGINE="http://prod-engine:9000"
USER_ID="11111111-1111-1111-1111-111111111111"

curl -X POST "$ENGINE/api/v1/admin/compliance/data-protection/events" \
  -H "X-Admin-Token: $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{
    \"user_id\": \"$USER_ID\",
    \"event_type\": \"DSAR_FULFILLED\",
    \"status\": \"COMPLETED\",
    \"reference\": \"DSAR-2026-04-25-001\",
    \"details\": {
      \"request_date\": \"2026-04-18\",
      \"fulfillment_date\": \"2026-04-25\",
      \"delivery_method\": \"secure_email\"
    },
    \"created_by\": \"privacy_officer\"
  }"
```

### Example 3: Record Legal Approval for Go-Live

```bash
ADMIN_TOKEN="your-admin-token"
ENGINE="http://prod-engine:9000"

curl -X POST "$ENGINE/api/v1/admin/compliance/legal-approvals" \
  -H "X-Admin-Token: $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "environment": "production",
    "approval_status": "APPROVED",
    "approved_by": "Jane Smith, General Counsel",
    "legal_memo_ref": "MEMO-2026-04-COMPLIANCE",
    "notes": "All controls reviewed and approved for production deployment.",
    "evidence": {
      "review_date": "2026-04-24",
      "risk_level": "LOW",
      "sign_off_date": "2026-04-24T10:00:00Z"
    },
    "signed_at": "2026-04-24T10:00:00Z"
  }'
```

---

## Support & Escalation

For critical issues:

1. **Case not found:** Verify case ID or reference format
2. **Permission denied:** Check X-Admin-Token is valid and not rotated
3. **Status transition invalid:** Refer to allowed status transitions in documentation
4. **Data loss concern:** Verify immutable audit trail in `aml_case_events` and `data_protection_events` tables

Contact: compliance-ops@company.com
