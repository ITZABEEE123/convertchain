# Compliance Operations: Go-Live Checklist

**Date:** April 2026  
**Status:** Implementation complete, tests passing, ready for staged rollout  
**Owner:** Compliance Engineering  

---

## Pre-Launch Validation

### Schema & Database

- [x] Migration `000009_compliance_ops.up.sql` creates all compliance tables
- [x] Rollback migration `000009_compliance_ops.down.sql` tested
- [ ] **Before go-live:** Run full schema validation against staging database
- [ ] **Before go-live:** Backup existing production schema

**Migration tables:**
- `compliance_screening_events` — User/counterparty/wallet screening records
- `aml_review_cases` — Case tracking with disposition audit trail
- `aml_case_events` — Immutable case event log
- `legal_launch_approvals` — Environment-level legal sign-off records
- `data_protection_events` — Privacy/DSAR/breach audit trail

### Code Quality

- [x] Full test suite passing: `go test ./...`
- [x] Compliance packages tested:
  - `internal/service/compliance_ops_test.go` (limits, screening, monitoring)
  - `internal/api/handlers/admin_compliance_handler_test.go` (AML admin workflows)
  - `internal/workers/deposit_watcher_test.go` (high-risk wallet escalation)
- [x] Formatting complete: `gofmt -w [compliance files]`
- [ ] **Before go-live:** Code review sign-off from security/legal
- [ ] **Before go-live:** Penetration testing on admin endpoints

---

## Environment Configuration

### Required Environment Variables

#### KYC Tier Limits (Daily/Monthly in Kobo)

```bash
# Tier 1: Default ₦5,000 / ₦50,000
KYC_TIER_1_DAILY_LIMIT_KOBO=5000000
KYC_TIER_1_MONTHLY_LIMIT_KOBO=50000000

# Tier 2: Default ₦20,000 / ₦200,000
KYC_TIER_2_DAILY_LIMIT_KOBO=20000000
KYC_TIER_2_MONTHLY_LIMIT_KOBO=200000000

# Tier 3: Default ₦100,000 / ₦1,000,000
KYC_TIER_3_DAILY_LIMIT_KOBO=100000000
KYC_TIER_3_MONTHLY_LIMIT_KOBO=1000000000

# Tier 4: Default ₦500,000 / ₦5,000,000
KYC_TIER_4_DAILY_LIMIT_KOBO=500000000
KYC_TIER_4_MONTHLY_LIMIT_KOBO=5000000000
```

#### Sanctions & PEP Screening

```bash
# Comma-separated block-on-match terms (case-insensitive substring match)
SANCTIONS_BLOCK_TERMS=TERROR,SANCTIONED,OFAC,SDN,EMBARGO

# Comma-separated escalate-on-match terms
PEP_ESCALATE_TERMS=MINISTER,SENATOR,POLITICALLY EXPOSED,PEP,GOVERNOR
```

#### AML Monitoring Thresholds

```bash
# Velocity: quote count in 15-minute window
AML_VELOCITY_QUOTE_COUNT_15M=8

# Structuring: near-limit quotes in a day (threshold: 80% of daily limit)
AML_STRUCTURING_QUOTE_COUNT_DAY=3
```

#### High-Risk Wallet Blocklist

```bash
# Comma-separated wallet addresses to dispute on match
HIGH_RISK_WALLET_BLOCKLIST=0x1234abcd...,bc1qrisk...,polygon:0xabc...
```

#### Blockchain Configuration (for deposit address validation)

```bash
# Sandbox mode (local development) — use sandbox:// addresses
BLOCKCHAIN_MONITOR_MODE=sandbox

# Production mode (uses real blockchain adapters)
# BLOCKCHAIN_MONITOR_MODE=production
# BTC_DEPOSIT_ADDRESS=bc1q...
# USDC_ETH_DEPOSIT_ADDRESS=0x...
# USDC_POLYGON_DEPOSIT_ADDRESS=0x...
```

---

## Launch Gates

### Tier 1: Technical Readiness

- [x] Code compiles and tests pass
- [x] Migration tested against fresh database
- [x] Admin compliance endpoints accessible and authenticated
- [x] Screening decision logic deterministic (no flaky tests)
- [ ] **Gate:** Staging deployment successful with smoke tests passing
- [ ] **Gate:** Load testing confirms latency <100ms for limit checks

**Validation command:**
```bash
cd go-engine
go test ./... -v
go build ./cmd/server
```

---

### Tier 2: Security & Audit

- [ ] **Legal sign-off:** Compliance control design review
- [ ] **Security review:** Admin token rotation plan and key management verified
- [ ] **Audit:** Initial AML case templates and disposition codes documented
- [ ] **Privacy:** Data retention policy recorded in `RecordDataProtectionEvent` workflow
- [ ] **Ops:** On-call escalation for high-severity AML alerts defined

**Evidence artifact:** Legal memo reference stored in `legal_launch_approvals` table

---

### Tier 3: Operations & Monitoring

- [ ] **Dashboard:** AML case dashboard configured (queries to `aml_review_cases`)
- [ ] **Alerts:** Compliance alert rule created (velocity/structuring alerts → Slack/PagerDuty)
- [ ] **Metrics:** Prometheus metrics instrumentation for case/screening counts
- [ ] **Runbook:** On-call guide for `SCREENING_BLOCKED` and `COMPLIANCE_REVIEW_REQUIRED` responses
- [ ] **Logging:** Centralized logging enabled for admin operations on compliance endpoints

---

### Tier 4: Business Acceptance

- [ ] **Compliance review:** Case disposition workflow tested with sample scenarios
- [ ] **Acceptance test:** Full sandbox trade through each limit/screening/monitoring path
- [ ] **User communication:** Support scripts for limit-exceeded and screening-escalation messages
- [ ] **Fallback plan:** Documented manual override process for false-positive screening hits

---

## Deployment Plan

### Staging Deployment (Day 1)

1. **Schema:** Apply migration `000009_compliance_ops` to staging DB
   ```bash
   docker compose -f docker-compose.staging.yml run --rm migrate
   ```

2. **Environment:** Load compliance variables (see above) to staging environment

3. **Smoke Test:** Run full transaction lifecycle
   ```bash
   ./scripts/smoke_local_stack.sh
   ```

4. **Admin Test:** Verify AML case workflows
   ```bash
   curl -H "X-Admin-Token: $ADMIN_TOKEN" \
        http://staging-engine:9000/api/v1/admin/compliance/cases
   ```

5. **Monitoring:** Enable compliance audit logging and alert rules

### Production Deployment (Day 2-3, after sign-off)

1. **Maintenance window:** Schedule 30-min downtime for schema application
   ```bash
   # Production migration with backup
   pg_dump convertchain_prod > /backup/pre_compliance_$(date +%s).sql
   docker compose -f docker-compose.yml run --rm migrate
   ```

2. **Gradual rollout:** Enable feature flags
   ```bash
   # Day 2: Warnings only (compliance checks logged but not blocking)
   COMPLIANCE_MODE=warn
   
   # Day 3+: Enforce (blocking trades for limit/screening/monitoring failures)
   COMPLIANCE_MODE=enforce
   ```

3. **Initial disable plan:** If critical issues found, revert to pre-compliance trade flow
   ```bash
   # Rollback migration if needed
   migrate -path go-engine/migrations -database "$DATABASE_URL" down 1
   ```

---

## Operational Runbooks

### Scenario 1: User Hits Daily Limit

**Symptom:** Quote request returns `ERR_TIER_LIMIT_EXCEEDED`

**Response:**
1. User sees upgrade guidance in API response
2. Support checks `KYC_TIER_1_DAILY_LIMIT_KOBO` and user's daily quota from logs
3. If limit is wrong, adjust environment variable and redeploy
4. If user is upgrading tier, advise them to resubmit KYC

**Query to validate:**
```sql
SELECT SUM(net_amount) FROM quotes 
WHERE user_id = $1 AND created_at >= NOW() - INTERVAL '1 day'
  AND status != 'EXPIRED';
```

---

### Scenario 2: User Hits Screening Block

**Symptom:** Trade request returns `ERR_SCREENING_BLOCKED` with case reference (e.g., `AML-ABC123DEF4`)

**Response:**
1. Admin looks up case: `GET /api/v1/admin/compliance/cases/AML-ABC123DEF4`
2. Case shows screening scope (USER, COUNTERPARTY, WALLET) and match term
3. Admin reviews evidence and user details
4. If false positive: disposition with `confirmed_false_positive` and note
5. If true positive: disposition with `confirmed_suspicious` and STR referral metadata

**Manual override (if approved by compliance officer):**
```bash
curl -X POST "http://prod-engine:9000/api/v1/admin/compliance/cases/$CASE_ID/disposition" \
  -H "X-Admin-Token: $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "status": "DISMISSED",
    "disposition": "confirmed_false_positive",
    "disposition_note": "Name match only; verified identity documents clear.",
    "actor": "compliance_officer_name"
  }'
```

Then user can retry trade.

---

### Scenario 3: Velocity or Structuring Alert

**Symptom:** Trade request returns `ERR_COMPLIANCE_REVIEW_REQUIRED` with alert type in logs

**Response:**
1. Alert type is in case: `VELOCITY_ALERT` (8+ quotes in 15 min) or `STRUCTURING_ALERT` (3+ near-limit quotes)
2. Admin reviews trading pattern and user profile
3. If legitimate high-volume customer: disposition with `confirmed_legitimate_pattern`
4. If suspicious activity: escalate with STR metadata and assign to compliance analyst

**Query to check recent quote velocity:**
```sql
SELECT COUNT(*) FROM quotes 
WHERE user_id = $1 AND created_at >= NOW() - INTERVAL '15 minutes';
```

---

### Scenario 4: High-Risk Wallet Deposit

**Symptom:** Trade moves to `DISPUTE` status with reason `high_risk_wallet_blocklist`

**Response:**
1. Deposit watcher detected address in `HIGH_RISK_WALLET_BLOCKLIST`
2. Trade is paused; user notified of manual review requirement
3. Admin can:
   - Investigate source address legitimacy
   - Whitelist address if false positive: update `HIGH_RISK_WALLET_BLOCKLIST`
   - Confirm and close dispute without payout (require user to re-initiate with compliant address)

---

## Post-Launch Monitoring (First 30 Days)

### Key Metrics to Track

1. **Limit rejections:** % of quotes hitting daily/monthly ceiling by tier
2. **Screening hits:** Screening decision breakdown (CLEAR / POSSIBLE_MATCH / POSITIVE_MATCH)
3. **False positives:** Screening cases manually overridden as legitimate
4. **Alert volume:** Velocity/structuring alerts per day (trend should stabilize)
5. **Case resolution time:** Median time from alert → disposition
6. **High-risk wallet hits:** Deposits blocked per week (should trend down as users learn)

### Review Triggers

- **> 5% of TIER_1 users hitting daily limits:** Limits may be too restrictive; review with product
- **> 10% screening false positives:** Adjust block/escalate terms or add whitelist
- **> 50% structuring alerts:** Threshold may be too sensitive; tune `AML_STRUCTURING_QUOTE_COUNT_DAY`
- **> 1 hour median case resolution:** Need more compliance staff or admin tooling improvements

---

## Rollback Plan

### If Critical Issues Found

**Step 1: Stop processing new trades**
```bash
COMPLIANCE_MODE=warn  # Log but don't block
```

**Step 2: Investigate**
```bash
# Check recent errors in logs
docker compose logs -f go-engine | grep "LIMIT_EXCEEDED\|SCREENING_\|COMPLIANCE_REVIEW"

# Query problematic cases
SELECT * FROM aml_review_cases 
ORDER BY created_at DESC LIMIT 20;
```

**Step 3: Rollback schema if unrecoverable**
```bash
# Full schema rollback
migrate -path go-engine/migrations -database "$DATABASE_URL" down 1

# Restore from pre-deployment backup
psql convertchain_prod < /backup/pre_compliance_*.sql
```

---

## Success Criteria

✅ **Day 1:** Schema deployed, smoke test passes, no errors in logs  
✅ **Day 7:** First 100 transactions processed; case counts tracked  
✅ **Day 30:** 
- No critical issues requiring rollback
- Legal sign-off on live compliance operations
- Runbooks validated with real-world scenarios
- Monitoring dashboards operational
- Team trained on AML case disposition workflows

---

## Approval Sign-Off

| Role | Name | Signature | Date |
|------|------|-----------|------|
| Engineering Lead | _____ | _____ | _____ |
| Compliance Lead | _____ | _____ | _____ |
| Security Review | _____ | _____ | _____ |
| Legal | _____ | _____ | _____ |
| Operations | _____ | _____ | _____ |

---

## References

- Schema: [migrations/000009_compliance_ops.up.sql](../migrations/000009_compliance_ops.up.sql)
- Service Logic: [internal/service/compliance_ops.go](../go-engine/internal/service/compliance_ops.go)
- Admin API: [internal/api/handlers/admin_handler.go](../go-engine/internal/api/handlers/admin_handler.go)
- Operational Runbook: [docs/compliance-operations-runbook.md](compliance-operations-runbook.md)
- OpenAPI Spec: [docs/openapi.yaml](openapi.yaml)
