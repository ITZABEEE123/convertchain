-- ============================================================
-- Migration 009: KYC/AML Limits, Screening, and Compliance Ops
-- ============================================================

CREATE TABLE IF NOT EXISTS compliance_screening_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  trade_id UUID REFERENCES trades(id) ON DELETE SET NULL,
  quote_id UUID REFERENCES quotes(id) ON DELETE SET NULL,
  screening_scope TEXT NOT NULL CHECK (screening_scope IN ('USER', 'COUNTERPARTY', 'WALLET')),
  screening_type TEXT NOT NULL CHECK (screening_type IN ('SANCTIONS', 'PEP', 'WALLET_RISK')),
  screening_subject TEXT NOT NULL,
  decision TEXT NOT NULL CHECK (decision IN ('CLEAR', 'POSSIBLE_MATCH', 'POSITIVE_MATCH', 'HIGH_RISK', 'BLOCKLISTED', 'ERROR')),
  decision_reason TEXT,
  provider_ref TEXT,
  metadata JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_compliance_screening_events_user_created
  ON compliance_screening_events(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_compliance_screening_events_trade_created
  ON compliance_screening_events(trade_id, created_at DESC);

CREATE TABLE IF NOT EXISTS aml_review_cases (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  case_ref TEXT NOT NULL UNIQUE,
  user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  trade_id UUID REFERENCES trades(id) ON DELETE SET NULL,
  quote_id UUID REFERENCES quotes(id) ON DELETE SET NULL,
  case_type TEXT NOT NULL CHECK (case_type IN (
    'SCREENING_MATCH',
    'VELOCITY_ALERT',
    'STRUCTURING_ALERT',
    'FAILED_KYC_ALERT',
    'HIGH_RISK_WALLET',
    'AMOUNT_SPIKE',
    'SUSPICIOUS_PAYOUT_CHANGE'
  )),
  severity TEXT NOT NULL CHECK (severity IN ('LOW', 'MEDIUM', 'HIGH', 'CRITICAL')),
  status TEXT NOT NULL CHECK (status IN ('OPEN', 'IN_REVIEW', 'ESCALATED', 'DISMISSED', 'CONFIRMED', 'REFERRED_STR', 'CLOSED')),
  reason TEXT NOT NULL,
  evidence JSONB,
  disposition TEXT,
  disposition_note TEXT,
  str_referral_metadata JSONB,
  assigned_to TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  resolved_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_aml_review_cases_status_created
  ON aml_review_cases(status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_aml_review_cases_user_created
  ON aml_review_cases(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS aml_case_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  case_id UUID NOT NULL REFERENCES aml_review_cases(id) ON DELETE CASCADE,
  actor TEXT NOT NULL,
  event_type TEXT NOT NULL,
  note TEXT,
  evidence JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_aml_case_events_case_created
  ON aml_case_events(case_id, created_at DESC);

CREATE TABLE IF NOT EXISTS legal_launch_approvals (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  environment TEXT NOT NULL,
  approval_status TEXT NOT NULL CHECK (approval_status IN ('PENDING', 'APPROVED', 'REJECTED', 'REVOKED')),
  approved_by TEXT,
  legal_memo_ref TEXT,
  notes TEXT,
  evidence JSONB,
  signed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_legal_launch_approvals_env
  ON legal_launch_approvals(LOWER(environment));

CREATE TABLE IF NOT EXISTS data_protection_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  event_type TEXT NOT NULL CHECK (event_type IN (
    'LAWFUL_BASIS_RECORDED',
    'RETENTION_POLICY_RECORDED',
    'DSAR_REQUESTED',
    'DSAR_FULFILLED',
    'ERASURE_REQUESTED',
    'ERASURE_COMPLETED',
    'ANONYMIZATION_COMPLETED',
    'BREACH_INCIDENT_RECORDED',
    'BREACH_RESPONSE_RECORDED'
  )),
  status TEXT NOT NULL CHECK (status IN ('OPEN', 'IN_PROGRESS', 'COMPLETED', 'REJECTED')),
  reference TEXT,
  details JSONB,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_data_protection_events_user_created
  ON data_protection_events(user_id, created_at DESC);

DROP TRIGGER IF EXISTS set_aml_review_cases_updated_at ON aml_review_cases;
CREATE TRIGGER set_aml_review_cases_updated_at
  BEFORE UPDATE ON aml_review_cases
  FOR EACH ROW
  EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS set_legal_launch_approvals_updated_at ON legal_launch_approvals;
CREATE TRIGGER set_legal_launch_approvals_updated_at
  BEFORE UPDATE ON legal_launch_approvals
  FOR EACH ROW
  EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS set_data_protection_events_updated_at ON data_protection_events;
CREATE TRIGGER set_data_protection_events_updated_at
  BEFORE UPDATE ON data_protection_events
  FOR EACH ROW
  EXECUTE FUNCTION update_updated_at_column();
