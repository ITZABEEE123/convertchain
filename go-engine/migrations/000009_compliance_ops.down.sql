DROP TRIGGER IF EXISTS set_data_protection_events_updated_at ON data_protection_events;
DROP TABLE IF EXISTS data_protection_events;

DROP TRIGGER IF EXISTS set_legal_launch_approvals_updated_at ON legal_launch_approvals;
DROP TABLE IF EXISTS legal_launch_approvals;

DROP TABLE IF EXISTS aml_case_events;

DROP TRIGGER IF EXISTS set_aml_review_cases_updated_at ON aml_review_cases;
DROP TABLE IF EXISTS aml_review_cases;

DROP TABLE IF EXISTS compliance_screening_events;
