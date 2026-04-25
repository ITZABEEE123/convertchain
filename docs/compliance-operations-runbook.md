# Compliance Operations Runbook

This runbook defines launch-blocking KYC/AML controls for live-money processing.

## 1. Tier Limits Enforcement

Service-layer checks run before quote and trade creation.

- Limit source: KYC tier policy (`TIER_1`..`TIER_4`).
- Enforcement points:
  - Quote creation evaluates daily/monthly quote volume.
  - Trade creation evaluates daily/monthly trade volume.
- Failure behavior:
  - Request is rejected with error code `LIMIT_EXCEEDED`.
  - Response includes upgrade guidance in `error.details.guidance`.

Optional overrides per tier:

- `KYC_TIER_1_DAILY_LIMIT_KOBO`, `KYC_TIER_1_MONTHLY_LIMIT_KOBO`
- `KYC_TIER_2_DAILY_LIMIT_KOBO`, `KYC_TIER_2_MONTHLY_LIMIT_KOBO`
- `KYC_TIER_3_DAILY_LIMIT_KOBO`, `KYC_TIER_3_MONTHLY_LIMIT_KOBO`
- `KYC_TIER_4_DAILY_LIMIT_KOBO`, `KYC_TIER_4_MONTHLY_LIMIT_KOBO`

## 2. Sanctions and PEP Screening

Screening runs before trading on user and counterparty fields.

- User scope: name/channel identity values.
- Counterparty scope: payout account name and account number.
- Decisions:
  - Positive sanctions match -> blocked (`SCREENING_BLOCKED`), AML case opened.
  - Possible PEP match -> escalated (`SCREENING_REVIEW_REQUIRED`), AML case opened.

Configuration terms:

- `SANCTIONS_BLOCK_TERMS` (comma-separated)
- `PEP_ESCALATE_TERMS` (comma-separated)

All decisions are written to `compliance_screening_events`.

## 3. Transaction Monitoring Rules

Monitoring runs before quote/trade completion and opens AML review cases.

Rules currently implemented:

- Velocity: high quote count in 15 minutes.
- Structuring: repeated near-limit quotes in a day.
- Failed KYC attempts (rejected users attempting to transact).
- Amount spikes versus historical quote average.
- Suspicious payout change to a new account.

Alerts create `aml_review_cases` and return `COMPLIANCE_REVIEW_REQUIRED` for manual review.

## 4. Wallet Risk Controls

High-risk wallet blocklist checks are enforced in two places:

- Trade-time risk check against configured deposit address.
- Deposit watcher check against observed or expected wallet address.

Configuration:

- `HIGH_RISK_WALLET_BLOCKLIST` (comma-separated exact addresses)

Behavior:

- Matching address causes dispute/manual review (`high_risk_wallet_blocklist`).

## 5. AML Case Management Workflow

Admin workflow is available under authenticated admin routes:

- `GET /api/v1/admin/compliance/cases`
- `GET /api/v1/admin/compliance/cases/:id`
- `POST /api/v1/admin/compliance/cases/:id/disposition`

Case capabilities:

- Review case reason and evidence.
- Record disposition and note.
- Attach STR referral metadata.
- Preserve timeline through `aml_case_events`.

## 6. Data Protection Controls

Data protection operations are auditable and testable through service/API workflows.

Implemented controls:

- Lawful basis and retention policy records via `data_protection_events`.
- DSAR request and fulfillment records.
- Erasure request and completion records.
- Breach incident and breach-response records.
- Account deletion and anonymization flow remains enforced before irreversible deletion.

Admin endpoint:

- `POST /api/v1/admin/compliance/data-protection/events`

Recommended retention baseline:

- KYC and financial audit records: 7 years.
- Screening and AML case records: 7 years.
- DSAR and breach records: 6 years.

## 7. Legal Sign-off Launch Packet

Launch approval is tracked through:

- `POST /api/v1/admin/compliance/legal-approvals`

Stored fields support go-live packet references:

- environment
- approval status
- legal memo reference
- signatory
- signed timestamp
- evidence payload

Go-live should remain blocked until target environment is explicitly marked `APPROVED`.
