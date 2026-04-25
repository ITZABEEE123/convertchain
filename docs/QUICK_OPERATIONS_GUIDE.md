# Quick Operations Reference

## Most Common Tasks

### Check Recent AML Cases

```bash
curl -s -H "X-Admin-Token: $ADMIN_TOKEN" \
  "http://localhost:9000/api/v1/admin/compliance/cases?status=OPEN&limit=10" | jq '.'
```

### Disposition a Screening Case (False Positive)

```bash
curl -X POST -H "X-Admin-Token: $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "status": "DISMISSED",
    "disposition": "confirmed_false_positive",
    "disposition_note": "Name match only; verified customer identity documents.",
    "actor": "compliance_analyst_jane"
  }' \
  "http://localhost:9000/api/v1/admin/compliance/cases/AML-ABC123DEF456/disposition"
```

### Disposition a Velocity Alert (Confirmed Suspicious)

```bash
curl -X POST -H "X-Admin-Token: $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "status": "CONFIRMED",
    "disposition": "confirmed_suspicious",
    "disposition_note": "Pattern matches structuring behavior. Recommend STR filing.",
    "str_referral_metadata": {
      "filing_bank": "Access Bank",
      "filing_reference": "STR-2026-04-25-001",
      "analyst_name": "Jane Doe",
      "recommendation": "file_str"
    },
    "actor": "compliance_analyst_jane"
  }' \
  "http://localhost:9000/api/v1/admin/compliance/cases/AML-VEL456GHI789/disposition"
```

### Record a Sanctions Case Escalation (STR Referral)

```bash
curl -X POST -H "X-Admin-Token: $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "status": "REFERRED_STR",
    "disposition": "confirmed_sanctions_match",
    "disposition_note": "Confirmed match against OFAC SDN list. Referred to CBN.",
    "str_referral_metadata": {
      "filing_bank": "Access Bank",
      "filing_reference": "STR-2026-04-25-OFAC-001",
      "analyst_name": "Compliance Officer",
      "recommendation": "file_str",
      "regulatory_reason": "OFAC_SDN_MATCH"
    },
    "actor": "compliance_officer"
  }' \
  "http://localhost:9000/api/v1/admin/compliance/cases/AML-SAN789JKL012/disposition"
```

### Record DSAR Request Fulfillment

```bash
curl -X POST -H "X-Admin-Token: $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "11111111-1111-1111-1111-111111111111",
    "event_type": "DSAR_FULFILLED",
    "status": "COMPLETED",
    "reference": "DSAR-2026-04-25-001",
    "details": {
      "request_date": "2026-04-18",
      "fulfillment_date": "2026-04-25",
      "delivery_method": "secure_email",
      "recipient_email": "user@example.com"
    },
    "created_by": "privacy_officer"
  }' \
  "http://localhost:9000/api/v1/admin/compliance/data-protection/events"
```

### Record Legal Go-Live Approval

```bash
curl -X POST -H "X-Admin-Token: $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "environment": "production",
    "approval_status": "APPROVED",
    "approved_by": "Jane Smith, General Counsel",
    "legal_memo_ref": "MEMO-2026-04-COMPLIANCE-LAUNCH",
    "notes": "All controls reviewed. Ready for production deployment.",
    "evidence": {
      "review_date": "2026-04-24",
      "risk_level": "LOW",
      "approver_email": "jane.smith@company.com"
    },
    "signed_at": "2026-04-24T10:00:00Z"
  }' \
  "http://localhost:9000/api/v1/admin/compliance/legal-approvals"
```

---

## Common SQL Queries

### Find All Open AML Cases

```sql
SELECT case_ref, case_type, severity, reason, created_at
FROM aml_review_cases
WHERE status = 'OPEN'
ORDER BY created_at DESC;
```

### Find False Positives Dismissed

```sql
SELECT case_ref, case_type, disposition_note, resolved_at
FROM aml_review_cases
WHERE status = 'DISMISSED' AND disposition = 'confirmed_false_positive'
ORDER BY resolved_at DESC;
```

### Find All STR Referrals

```sql
SELECT case_ref, case_type, str_referral_metadata, resolved_at
FROM aml_review_cases
WHERE status = 'REFERRED_STR'
ORDER BY resolved_at DESC;
```

### Audit Trail for a Case

```sql
SELECT actor, event_type, note, evidence, created_at
FROM aml_case_events
WHERE case_id = (SELECT id FROM aml_review_cases WHERE case_ref = 'AML-ABC123')
ORDER BY created_at ASC;
```

### User's Screening Events

```sql
SELECT screening_scope, screening_type, decision, decision_reason, created_at
FROM compliance_screening_events
WHERE user_id = '11111111-1111-1111-1111-111111111111'
ORDER BY created_at DESC;
```

### User's Data Protection Events (DSAR/Erasure)

```sql
SELECT event_type, status, reference, details, created_at
FROM data_protection_events
WHERE user_id = '11111111-1111-1111-1111-111111111111'
  AND event_type IN ('DSAR_REQUESTED', 'DSAR_FULFILLED', 'ERASURE_REQUESTED', 'ERASURE_COMPLETED')
ORDER BY created_at DESC;
```

### Check Daily Limit Usage

```sql
SELECT user_id, SUM(net_amount) as daily_total, COUNT(*) as quote_count
FROM quotes
WHERE created_at >= NOW() - INTERVAL '1 day'
GROUP BY user_id
HAVING SUM(net_amount) > 5000000  -- TIER_1 daily limit
ORDER BY daily_total DESC;
```

### Check Monthly Limit Usage

```sql
SELECT user_id, SUM(net_amount) as monthly_total, COUNT(*) as quote_count
FROM quotes
WHERE created_at >= DATE_TRUNC('month', NOW())
GROUP BY user_id
HAVING SUM(net_amount) > 50000000  -- TIER_1 monthly limit
ORDER BY monthly_total DESC;
```

### Velocity Alert Check (Last 15 minutes)

```sql
SELECT user_id, COUNT(*) as quote_count_15m, ARRAY_AGG(quote_id) as quote_ids
FROM quotes
WHERE created_at >= NOW() - INTERVAL '15 minutes'
GROUP BY user_id
HAVING COUNT(*) >= 8  -- AML_VELOCITY_QUOTE_COUNT_15M threshold
ORDER BY quote_count_15m DESC;
```

### High-Risk Wallet Disputes

```sql
SELECT t.id, t.trade_ref, t.user_id, t.status, t.deposit_address, ts.updated_at
FROM trades t
JOIN (
  SELECT trade_id, MAX(updated_at) as updated_at
  FROM trade_status_history
  WHERE new_status = 'DISPUTE' AND metadata->>'reason' = 'high_risk_wallet_blocklist'
  GROUP BY trade_id
) ts ON t.id = ts.trade_id
ORDER BY ts.updated_at DESC;
```

---

## Environment Variable Quick Check

```bash
# Verify all compliance vars are set
echo "=== Limits ==="
env | grep KYC_TIER | sort

echo "=== Screening ==="
env | grep SANCTIONS_BLOCK_TERMS
env | grep PEP_ESCALATE_TERMS

echo "=== Monitoring ==="
env | grep AML_VELOCITY
env | grep AML_STRUCTURING

echo "=== Wallet Risk ==="
env | grep HIGH_RISK_WALLET

echo "=== Blockchain ==="
env | grep BLOCKCHAIN_MONITOR_MODE
env | grep DEPOSIT_ADDRESS
```

---

## Alert Handling Flowchart

```
User attempts quote/trade
          ↓
     ┌────────────────────────────────────┐
     │ Compliance Checks Run               │
     └────────────────────────────────────┘
               ↓
        ┌──────┴──────────────────────────────────┐
        ↓                                          ↓
   ┌─────────────────┐              ┌──────────────────────┐
   │ PASS            │              │ FAIL                 │
   │ Quote/Trade OK  │              │ Check Type?          │
   └─────────────────┘              └──────────────────────┘
        ↓                                    ↓
   Return Quote/                    ┌───────┴────────┬─────────────┬──────────┐
   Create Trade                     ↓                ↓             ↓          ↓
                          LIMIT_EXCEEDED  SCREENING_*  COMPLIANCE_REVIEW  OTHER
                                  ↓                ↓             ↓          ↓
                           422 +               403 +        409 +         5xx
                           guidance        case ref       case ref      ERROR
                                             ↓                ↓
                                    AML Case OPEN         AML Case OPEN
                                    (user notified)       (auto-created)
                                             ↓                ↓
                                    Admin reviews      Manual review required
                                    false positive?    (velocity, structuring,
                                             ↓          spike, failed KYC,
                                    DISMISS or         risky wallet, payout
                                    CONFIRM +          change)
                                    REFER_STR
```

---

## Troubleshooting

### Case Won't Disposition

**Error:** `"Case already closed (immutable)"`

**Fix:** Case with `status = 'CLOSED'` cannot be re-opened. Check case status first:

```bash
curl -s -H "X-Admin-Token: $ADMIN_TOKEN" \
  "http://localhost:9000/api/v1/admin/compliance/cases/AML-ABC123" | jq '.status'
```

If closed in error, restore from backup or file a manual override request.

---

### Admin Token Not Accepted

**Check:**
1. Token is set in environment: `echo $ADMIN_TOKEN`
2. Token matches server config: Check `.env` or deployment manifest
3. Token is not expired (rotate every 90 days)
4. Header is exactly `X-Admin-Token: <value>` (case-sensitive)

---

### Screening Terms Not Matching

**Symptoms:** User with "SENATOR" in name not triggering PEP escalation

**Check:**
1. Verify env var is set: `echo $PEP_ESCALATE_TERMS`
2. Verify term is included (case-insensitive substring match)
3. Remember: First name + Last name + Channel ID are concatenated
4. Test with cURL directly to see screening decision

```bash
curl -s -H "X-Service-Token: $SERVICE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"user_id":"...", "asset":"USDT", "amount":"100"}' \
  http://localhost:9000/api/v1/quotes | jq '.error'
```

---

### Wallet Blocklist Not Enforcing

**Symptom:** Deposit from blocked address didn't create HIGH_RISK_WALLET case

**Check:**
1. Address format matches `HIGH_RISK_WALLET_BLOCKLIST` (case-insensitive for Ethereum)
2. For USDC: Address is tagged with network, e.g., `polygon:0xabc...`
3. Watcher runs every 5 seconds; check logs for `deposit_watcher` activity
4. Query trades in DISPUTE state:

```bash
psql -c "SELECT id, deposit_address, status FROM trades WHERE status = 'DISPUTE' LIMIT 10;"
```

---

## Performance Baselines

| Operation | Latency | Notes |
|-----------|---------|-------|
| Quote creation (with compliance checks) | <50ms | Includes limits + screening + monitoring |
| Trade creation (with compliance checks) | <100ms | Includes all checks + blockchain address check |
| AML case list | <20ms | Query 50 cases |
| AML case disposition | <10ms | Single update + event insert |
| Deposit watcher cycle | ~2s | Full scan of PENDING_DEPOSIT trades |

Monitor in production:
```bash
# Prometheus queries
rate(http_request_duration_seconds_bucket{handler="quote",le="0.1"}[5m])
rate(http_request_duration_seconds_bucket{handler="trade",le="0.1"}[5m])
```

---

## Emergency Contacts

- **Compliance Lead:** compliance@company.com
- **Security:** security@company.com
- **On-Call (PagerDuty):** `compliance-oncall` escalation policy
- **Legal:** legal@company.com

---

## Documentation Links

- [Compliance Operations Runbook](compliance-operations-runbook.md)
- [Go-Live Checklist](compliance-go-live-checklist.md)
- [Admin API Reference](compliance-admin-api.md)
- [OpenAPI Spec](openapi.yaml)
- [Implementation Summary](IMPLEMENTATION_SUMMARY.md)
