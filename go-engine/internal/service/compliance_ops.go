package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"convert-chain/go-engine/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type tierLimitPolicy struct {
	DailyKobo   int64
	MonthlyKobo int64
}

type TierLimitExceededError struct {
	Tier             string
	DailyLimitKobo   int64
	MonthlyLimitKobo int64
	DailyUsedKobo    int64
	MonthlyUsedKobo  int64
	AttemptKobo      int64
	Guidance         string
}

func (e *TierLimitExceededError) Error() string {
	return "limit_exceeded"
}

func (e *TierLimitExceededError) DetailsMap() map[string]interface{} {
	return map[string]interface{}{
		"tier":               e.Tier,
		"daily_limit_kobo":   e.DailyLimitKobo,
		"monthly_limit_kobo": e.MonthlyLimitKobo,
		"daily_used_kobo":    e.DailyUsedKobo,
		"monthly_used_kobo":  e.MonthlyUsedKobo,
		"attempt_kobo":       e.AttemptKobo,
		"guidance":           e.Guidance,
	}
}

type ScreeningBlockedError struct {
	Reason string
}

func (e *ScreeningBlockedError) Error() string {
	return "screening_blocked"
}

type ScreeningReviewRequiredError struct {
	Reason string
}

func (e *ScreeningReviewRequiredError) Error() string {
	return "screening_review_required"
}

type ComplianceReviewRequiredError struct {
	Reason string
}

func (e *ComplianceReviewRequiredError) Error() string {
	return "compliance_review_required"
}

func defaultTierLimitPolicies() map[string]tierLimitPolicy {
	return map[string]tierLimitPolicy{
		"TIER_1": {DailyKobo: 5_000_000, MonthlyKobo: 50_000_000},
		"TIER_2": {DailyKobo: 20_000_000, MonthlyKobo: 200_000_000},
		"TIER_3": {DailyKobo: 100_000_000, MonthlyKobo: 1_000_000_000},
		"TIER_4": {DailyKobo: 500_000_000, MonthlyKobo: 5_000_000_000},
	}
}

func tierLimitPolicyForTier(tier string) tierLimitPolicy {
	policies := defaultTierLimitPolicies()
	normalized := strings.ToUpper(strings.TrimSpace(tier))
	policy, ok := policies[normalized]
	if !ok {
		return tierLimitPolicy{}
	}

	prefix := "KYC_" + normalized + "_"
	policy.DailyKobo = readInt64Env(prefix+"DAILY_LIMIT_KOBO", policy.DailyKobo)
	policy.MonthlyKobo = readInt64Env(prefix+"MONTHLY_LIMIT_KOBO", policy.MonthlyKobo)
	return policy
}

func tierUpgradeGuidance(tier string) string {
	switch strings.ToUpper(strings.TrimSpace(tier)) {
	case "TIER_0":
		return "Complete KYC to unlock trading limits."
	case "TIER_1":
		return "Upgrade to TIER_2 by submitting selfie and proof of address for higher limits."
	case "TIER_2":
		return "Upgrade to TIER_3 with enhanced document verification to increase limits."
	case "TIER_3":
		return "Upgrade to TIER_4 business verification for enterprise limits."
	default:
		return "Contact compliance support for a limits review."
	}
}

func readInt64Env(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func (s *ApplicationService) enforceQuoteTierLimits(ctx context.Context, user *domain.User, projectedKobo int64) error {
	if user == nil {
		return nil
	}
	policy := tierLimitPolicyForTier(user.KYCTier)
	if policy.DailyKobo <= 0 || policy.MonthlyKobo <= 0 {
		return &TierLimitExceededError{
			Tier:             user.KYCTier,
			DailyLimitKobo:   0,
			MonthlyLimitKobo: 0,
			DailyUsedKobo:    0,
			MonthlyUsedKobo:  0,
			AttemptKobo:      projectedKobo,
			Guidance:         "Complete KYC to unlock trading limits.",
		}
	}

	dayStart := time.Now().UTC().Truncate(24 * time.Hour)
	monthStart := time.Date(dayStart.Year(), dayStart.Month(), 1, 0, 0, 0, 0, time.UTC)

	var dailyUsed int64
	if err := s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(net_amount), 0)
		FROM quotes
		WHERE user_id = $1::uuid
		  AND created_at >= $2
	`, user.ID, dayStart).Scan(&dailyUsed); err != nil {
		return err
	}

	var monthlyUsed int64
	if err := s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(net_amount), 0)
		FROM quotes
		WHERE user_id = $1::uuid
		  AND created_at >= $2
	`, user.ID, monthStart).Scan(&monthlyUsed); err != nil {
		return err
	}

	if dailyUsed+projectedKobo > policy.DailyKobo || monthlyUsed+projectedKobo > policy.MonthlyKobo {
		return &TierLimitExceededError{
			Tier:             strings.ToUpper(strings.TrimSpace(user.KYCTier)),
			DailyLimitKobo:   policy.DailyKobo,
			MonthlyLimitKobo: policy.MonthlyKobo,
			DailyUsedKobo:    dailyUsed,
			MonthlyUsedKobo:  monthlyUsed,
			AttemptKobo:      projectedKobo,
			Guidance:         tierUpgradeGuidance(user.KYCTier),
		}
	}

	return nil
}

func (s *ApplicationService) enforceTradeTierLimits(ctx context.Context, user *domain.User, projectedKobo int64) error {
	if user == nil {
		return nil
	}
	policy := tierLimitPolicyForTier(user.KYCTier)
	if policy.DailyKobo <= 0 || policy.MonthlyKobo <= 0 {
		return &TierLimitExceededError{
			Tier:             user.KYCTier,
			DailyLimitKobo:   0,
			MonthlyLimitKobo: 0,
			DailyUsedKobo:    0,
			MonthlyUsedKobo:  0,
			AttemptKobo:      projectedKobo,
			Guidance:         "Complete KYC to unlock trading limits.",
		}
	}

	dayStart := time.Now().UTC().Truncate(24 * time.Hour)
	monthStart := time.Date(dayStart.Year(), dayStart.Month(), 1, 0, 0, 0, 0, time.UTC)

	var dailyUsed int64
	if err := s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(to_amount_expected), 0)
		FROM trades
		WHERE user_id = $1::uuid
		  AND created_at >= $2
		  AND status <> 'CANCELLED'::trade_status
	`, user.ID, dayStart).Scan(&dailyUsed); err != nil {
		return err
	}

	var monthlyUsed int64
	if err := s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(to_amount_expected), 0)
		FROM trades
		WHERE user_id = $1::uuid
		  AND created_at >= $2
		  AND status <> 'CANCELLED'::trade_status
	`, user.ID, monthStart).Scan(&monthlyUsed); err != nil {
		return err
	}

	if dailyUsed+projectedKobo > policy.DailyKobo || monthlyUsed+projectedKobo > policy.MonthlyKobo {
		return &TierLimitExceededError{
			Tier:             strings.ToUpper(strings.TrimSpace(user.KYCTier)),
			DailyLimitKobo:   policy.DailyKobo,
			MonthlyLimitKobo: policy.MonthlyKobo,
			DailyUsedKobo:    dailyUsed,
			MonthlyUsedKobo:  monthlyUsed,
			AttemptKobo:      projectedKobo,
			Guidance:         tierUpgradeGuidance(user.KYCTier),
		}
	}

	return nil
}

func loadNormalizedTermsFromEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	items := strings.Split(raw, ",")
	out := make([]string, 0, len(items))
	for _, item := range items {
		normalized := strings.ToUpper(strings.TrimSpace(item))
		if normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}

func containsAnyTerm(value string, terms []string) (string, bool) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	for _, term := range terms {
		if strings.Contains(normalized, term) {
			return term, true
		}
	}
	return "", false
}

func (s *ApplicationService) recordScreeningEvent(
	ctx context.Context,
	userID *uuid.UUID,
	tradeID *uuid.UUID,
	quoteID *uuid.UUID,
	scope string,
	screeningType string,
	subject string,
	decision string,
	reason string,
	providerRef string,
	metadata map[string]interface{},
) error {
	if s.db == nil {
		return nil
	}
	payload, _ := json.Marshal(metadata)
	var reasonValue interface{}
	if strings.TrimSpace(reason) != "" {
		reasonValue = strings.TrimSpace(reason)
	}
	var providerRefValue interface{}
	if strings.TrimSpace(providerRef) != "" {
		providerRefValue = strings.TrimSpace(providerRef)
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO compliance_screening_events (
			user_id,
			trade_id,
			quote_id,
			screening_scope,
			screening_type,
			screening_subject,
			decision,
			decision_reason,
			provider_ref,
			metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)
	`, userID, tradeID, quoteID, scope, screeningType, subject, decision, reasonValue, providerRefValue, string(payload))
	return err
}

func (s *ApplicationService) evaluateScreeningChecks(
	ctx context.Context,
	user *domain.User,
	account *domain.BankAccount,
	quoteID *uuid.UUID,
	tradeID *uuid.UUID,
	subjectHint string,
) error {
	if user == nil {
		return nil
	}

	sanctionsTerms := loadNormalizedTermsFromEnv("SANCTIONS_BLOCK_TERMS")
	pepTerms := loadNormalizedTermsFromEnv("PEP_ESCALATE_TERMS")
	if len(sanctionsTerms) == 0 {
		sanctionsTerms = []string{"TERROR", "SANCTIONED", "OFAC", "SDN"}
	}
	if len(pepTerms) == 0 {
		pepTerms = []string{"MINISTER", "SENATOR", "POLITICALLY EXPOSED", "PEP"}
	}

	userSubject := strings.TrimSpace(strings.Join([]string{user.FirstName, user.LastName, user.ChannelUserID, subjectHint}, " "))
	if match, hit := containsAnyTerm(userSubject, sanctionsTerms); hit {
		reason := fmt.Sprintf("Potential sanctions match on user identity (%s)", match)
		_ = s.recordScreeningEvent(ctx, &user.ID, tradeID, quoteID, "USER", "SANCTIONS", userSubject, "POSITIVE_MATCH", reason, "local-term-screening", map[string]interface{}{"match": match})
		caseRecord, _ := s.createAMLReviewCase(ctx, &user.ID, tradeID, quoteID, "SCREENING_MATCH", "CRITICAL", reason, map[string]interface{}{"scope": "USER", "screening": "SANCTIONS", "match": match}, "system")
		if caseRecord != nil {
			return &ScreeningBlockedError{Reason: reason + "; case_ref=" + caseRecord.CaseRef}
		}
		return &ScreeningBlockedError{Reason: reason}
	}
	_ = s.recordScreeningEvent(ctx, &user.ID, tradeID, quoteID, "USER", "SANCTIONS", userSubject, "CLEAR", "", "local-term-screening", nil)

	if match, hit := containsAnyTerm(userSubject, pepTerms); hit {
		reason := fmt.Sprintf("Possible PEP match on user identity (%s)", match)
		_ = s.recordScreeningEvent(ctx, &user.ID, tradeID, quoteID, "USER", "PEP", userSubject, "POSSIBLE_MATCH", reason, "local-term-screening", map[string]interface{}{"match": match})
		caseRecord, _ := s.createAMLReviewCase(ctx, &user.ID, tradeID, quoteID, "SCREENING_MATCH", "HIGH", reason, map[string]interface{}{"scope": "USER", "screening": "PEP", "match": match}, "system")
		if caseRecord != nil {
			return &ScreeningReviewRequiredError{Reason: reason + "; case_ref=" + caseRecord.CaseRef}
		}
		return &ScreeningReviewRequiredError{Reason: reason}
	}
	_ = s.recordScreeningEvent(ctx, &user.ID, tradeID, quoteID, "USER", "PEP", userSubject, "CLEAR", "", "local-term-screening", nil)

	if account != nil {
		counterparty := strings.TrimSpace(strings.Join([]string{account.AccountName, account.AccountNumber}, " "))
		if match, hit := containsAnyTerm(counterparty, sanctionsTerms); hit {
			reason := fmt.Sprintf("Potential sanctions match on counterparty (%s)", match)
			_ = s.recordScreeningEvent(ctx, &user.ID, tradeID, quoteID, "COUNTERPARTY", "SANCTIONS", counterparty, "POSITIVE_MATCH", reason, "local-term-screening", map[string]interface{}{"match": match})
			caseRecord, _ := s.createAMLReviewCase(ctx, &user.ID, tradeID, quoteID, "SCREENING_MATCH", "CRITICAL", reason, map[string]interface{}{"scope": "COUNTERPARTY", "screening": "SANCTIONS", "match": match}, "system")
			if caseRecord != nil {
				return &ScreeningBlockedError{Reason: reason + "; case_ref=" + caseRecord.CaseRef}
			}
			return &ScreeningBlockedError{Reason: reason}
		}
		_ = s.recordScreeningEvent(ctx, &user.ID, tradeID, quoteID, "COUNTERPARTY", "SANCTIONS", counterparty, "CLEAR", "", "local-term-screening", nil)
	}

	return nil
}

func (s *ApplicationService) monitoringVelocityThreshold() int {
	return int(readInt64Env("AML_VELOCITY_QUOTE_COUNT_15M", 8))
}

func (s *ApplicationService) monitoringStructuringThreshold() int {
	return int(readInt64Env("AML_STRUCTURING_QUOTE_COUNT_DAY", 3))
}

func (s *ApplicationService) evaluateQuoteMonitoring(ctx context.Context, user *domain.User, projectedKobo int64, quoteID *uuid.UUID) error {
	if user == nil {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(user.Status), "KYC_REJECTED") {
		reason := "Failed KYC alert: rejected user attempted transaction"
		caseRecord, _ := s.createAMLReviewCase(ctx, &user.ID, nil, quoteID, "FAILED_KYC_ALERT", "CRITICAL", reason, map[string]interface{}{"user_status": user.Status}, "system")
		if caseRecord != nil {
			return &ComplianceReviewRequiredError{Reason: reason + "; case_ref=" + caseRecord.CaseRef}
		}
		return &ComplianceReviewRequiredError{Reason: reason}
	}
	if s.db == nil {
		return nil
	}

	velocityWindowStart := time.Now().UTC().Add(-15 * time.Minute)
	var recentQuoteCount int
	if err := s.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM quotes
		WHERE user_id = $1::uuid
		  AND created_at >= $2
	`, user.ID, velocityWindowStart).Scan(&recentQuoteCount); err == nil {
		if recentQuoteCount >= s.monitoringVelocityThreshold() {
			reason := fmt.Sprintf("Velocity alert: %d quotes in 15 minutes", recentQuoteCount)
			caseRecord, _ := s.createAMLReviewCase(ctx, &user.ID, nil, quoteID, "VELOCITY_ALERT", "HIGH", reason, map[string]interface{}{"count_15m": recentQuoteCount}, "system")
			if caseRecord != nil {
				return &ComplianceReviewRequiredError{Reason: reason + "; case_ref=" + caseRecord.CaseRef}
			}
			return &ComplianceReviewRequiredError{Reason: reason}
		}
	}

	policy := tierLimitPolicyForTier(user.KYCTier)
	todayStart := time.Now().UTC().Truncate(24 * time.Hour)
	var nearLimitCount int
	if policy.DailyKobo > 0 {
		minNearLimit := int64(float64(policy.DailyKobo) * 0.8)
		if err := s.db.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM quotes
			WHERE user_id = $1::uuid
			  AND created_at >= $2
			  AND net_amount >= $3
		`, user.ID, todayStart, minNearLimit).Scan(&nearLimitCount); err == nil {
			if nearLimitCount >= s.monitoringStructuringThreshold() {
				reason := fmt.Sprintf("Structuring alert: %d near-limit quotes today", nearLimitCount)
				caseRecord, _ := s.createAMLReviewCase(ctx, &user.ID, nil, quoteID, "STRUCTURING_ALERT", "HIGH", reason, map[string]interface{}{"near_limit_quotes_today": nearLimitCount}, "system")
				if caseRecord != nil {
					return &ComplianceReviewRequiredError{Reason: reason + "; case_ref=" + caseRecord.CaseRef}
				}
				return &ComplianceReviewRequiredError{Reason: reason}
			}
		}
	}

	var avgQuote int64
	if err := s.db.QueryRow(ctx, `
		SELECT COALESCE(AVG(net_amount)::bigint, 0)
		FROM quotes
		WHERE user_id = $1::uuid
		  AND created_at >= $2
	`, user.ID, time.Now().UTC().AddDate(0, 0, -30)).Scan(&avgQuote); err == nil {
		if avgQuote > 0 && projectedKobo > avgQuote*3 {
			reason := fmt.Sprintf("Amount spike alert: projected=%d avg_30d=%d", projectedKobo, avgQuote)
			caseRecord, _ := s.createAMLReviewCase(ctx, &user.ID, nil, quoteID, "AMOUNT_SPIKE", "MEDIUM", reason, map[string]interface{}{"projected_kobo": projectedKobo, "avg_30d_kobo": avgQuote}, "system")
			if caseRecord != nil {
				return &ComplianceReviewRequiredError{Reason: reason + "; case_ref=" + caseRecord.CaseRef}
			}
			return &ComplianceReviewRequiredError{Reason: reason}
		}
	}

	return nil
}

func walletRiskBlocklist() []string {
	return loadNormalizedTermsFromEnv("HIGH_RISK_WALLET_BLOCKLIST")
}

func isWalletBlocked(address string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(address))
	if normalized == "" {
		return false
	}
	for _, blocked := range walletRiskBlocklist() {
		if blocked == normalized {
			return true
		}
	}
	return false
}

func (s *ApplicationService) evaluateTradeMonitoring(
	ctx context.Context,
	user *domain.User,
	account *domain.BankAccount,
	quote *domain.Quote,
	tradeID *uuid.UUID,
	depositAddress string,
) error {
	if user == nil || quote == nil {
		return nil
	}
	if isWalletBlocked(depositAddress) {
		reason := "High-risk wallet blocklist matched trade deposit address"
		caseRecord, _ := s.createAMLReviewCase(ctx, &user.ID, tradeID, &quote.ID, "HIGH_RISK_WALLET", "CRITICAL", reason, map[string]interface{}{"deposit_address": depositAddress}, "system")
		if caseRecord != nil {
			return &ComplianceReviewRequiredError{Reason: reason + "; case_ref=" + caseRecord.CaseRef}
		}
		return &ComplianceReviewRequiredError{Reason: reason}
	}

	if s.db == nil {
		return nil
	}

	if account != nil && time.Since(account.CreatedAt) < 24*time.Hour {
		var previousBankID *uuid.UUID
		_ = s.db.QueryRow(ctx, `
			SELECT bank_account_id
			FROM trades
			WHERE user_id = $1::uuid
			  AND bank_account_id IS NOT NULL
			ORDER BY created_at DESC
			LIMIT 1
		`, user.ID).Scan(&previousBankID)
		if previousBankID != nil && *previousBankID != account.ID {
			reason := "Suspicious payout change: recently added payout account differs from prior trade"
			caseRecord, _ := s.createAMLReviewCase(ctx, &user.ID, tradeID, &quote.ID, "SUSPICIOUS_PAYOUT_CHANGE", "HIGH", reason, map[string]interface{}{"new_bank_account_id": account.ID.String(), "previous_bank_account_id": previousBankID.String()}, "system")
			if caseRecord != nil {
				return &ComplianceReviewRequiredError{Reason: reason + "; case_ref=" + caseRecord.CaseRef}
			}
			return &ComplianceReviewRequiredError{Reason: reason}
		}
	}

	return nil
}

func scanAMLCase(src rowScanner, c *domain.AMLReviewCase) error {
	var userID *uuid.UUID
	var tradeID *uuid.UUID
	var quoteID *uuid.UUID
	var evidenceBytes []byte
	var strBytes []byte
	if err := src.Scan(
		&c.ID,
		&c.CaseRef,
		&userID,
		&tradeID,
		&quoteID,
		&c.CaseType,
		&c.Severity,
		&c.Status,
		&c.Reason,
		&evidenceBytes,
		&c.Disposition,
		&c.DispositionNote,
		&strBytes,
		&c.AssignedTo,
		&c.CreatedAt,
		&c.UpdatedAt,
		&c.ResolvedAt,
	); err != nil {
		return err
	}
	c.UserID = userID
	c.TradeID = tradeID
	c.QuoteID = quoteID
	if len(evidenceBytes) > 0 {
		_ = json.Unmarshal(evidenceBytes, &c.Evidence)
	}
	if len(strBytes) > 0 {
		_ = json.Unmarshal(strBytes, &c.STRReferralMetadata)
	}
	return nil
}

func (s *ApplicationService) createAMLReviewCase(
	ctx context.Context,
	userID *uuid.UUID,
	tradeID *uuid.UUID,
	quoteID *uuid.UUID,
	caseType string,
	severity string,
	reason string,
	evidence map[string]interface{},
	actor string,
) (*domain.AMLReviewCase, error) {
	caseID := uuid.New()
	caseRef := "AML-" + strings.ToUpper(strings.ReplaceAll(caseID.String(), "-", "")[:10])
	if s.db == nil {
		return &domain.AMLReviewCase{
			ID:        caseID,
			CaseRef:   caseRef,
			UserID:    userID,
			TradeID:   tradeID,
			QuoteID:   quoteID,
			CaseType:  caseType,
			Severity:  severity,
			Status:    "OPEN",
			Reason:    reason,
			Evidence:  evidence,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}, nil
	}
	payload, _ := json.Marshal(evidence)

	caseRow := &domain.AMLReviewCase{}
	err := scanAMLCase(
		s.db.QueryRow(ctx, `
			INSERT INTO aml_review_cases (
				id,
				case_ref,
				user_id,
				trade_id,
				quote_id,
				case_type,
				severity,
				status,
				reason,
				evidence
			) VALUES (
				$1::uuid,
				$2,
				$3,
				$4,
				$5,
				$6,
				$7,
				'OPEN',
				$8,
				$9::jsonb
			)
			RETURNING
				id,
				case_ref,
				user_id,
				trade_id,
				quote_id,
				case_type,
				severity,
				status,
				reason,
				evidence,
				disposition,
				disposition_note,
				str_referral_metadata,
				assigned_to,
				created_at,
				updated_at,
				resolved_at
		`, caseID, caseRef, userID, tradeID, quoteID, caseType, severity, reason, string(payload)),
		caseRow,
	)
	if err != nil {
		return nil, err
	}

	note := "case opened"
	if strings.TrimSpace(reason) != "" {
		note = reason
	}
	eventEvidence, _ := json.Marshal(map[string]interface{}{"severity": severity, "case_type": caseType})
	eventActor := strings.TrimSpace(actor)
	if eventActor == "" {
		eventActor = "system"
	}
	_, _ = s.db.Exec(ctx, `
		INSERT INTO aml_case_events (case_id, actor, event_type, note, evidence)
		VALUES ($1::uuid, $2, 'CASE_OPENED', $3, $4::jsonb)
	`, caseRow.ID, eventActor, note, string(eventEvidence))

	return caseRow, nil
}

func (s *ApplicationService) ListAMLCases(ctx context.Context, status string, limit int) ([]*domain.AMLReviewCase, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	normalizedStatus := strings.ToUpper(strings.TrimSpace(status))
	args := []interface{}{limit}
	filter := ""
	if normalizedStatus != "" {
		filter = "WHERE status = $2"
		args = append(args, normalizedStatus)
	}

	rows, err := s.db.Query(ctx, fmt.Sprintf(`
		SELECT
			id,
			case_ref,
			user_id,
			trade_id,
			quote_id,
			case_type,
			severity,
			status,
			reason,
			evidence,
			disposition,
			disposition_note,
			str_referral_metadata,
			assigned_to,
			created_at,
			updated_at,
			resolved_at
		FROM aml_review_cases
		%s
		ORDER BY created_at DESC
		LIMIT $1
	`, filter), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*domain.AMLReviewCase, 0)
	for rows.Next() {
		item := &domain.AMLReviewCase{}
		if err := scanAMLCase(rows, item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *ApplicationService) GetAMLCase(ctx context.Context, identifier string) (*domain.AMLReviewCase, error) {
	trimmed := strings.TrimSpace(identifier)
	if trimmed == "" {
		return nil, nil
	}

	query := `
		SELECT
			id,
			case_ref,
			user_id,
			trade_id,
			quote_id,
			case_type,
			severity,
			status,
			reason,
			evidence,
			disposition,
			disposition_note,
			str_referral_metadata,
			assigned_to,
			created_at,
			updated_at,
			resolved_at
		FROM aml_review_cases
		WHERE %s
		LIMIT 1
	`

	item := &domain.AMLReviewCase{}
	if parsed, err := uuid.Parse(trimmed); err == nil {
		err = scanAMLCase(s.db.QueryRow(ctx, fmt.Sprintf(query, "id = $1::uuid"), parsed), item)
		if err == nil {
			return item, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}

	err := scanAMLCase(s.db.QueryRow(ctx, fmt.Sprintf(query, "UPPER(case_ref) = UPPER($1)"), trimmed), item)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return item, nil
}

func (s *ApplicationService) DispositionAMLCase(
	ctx context.Context,
	identifier string,
	status string,
	disposition string,
	note string,
	strReferralMetadata map[string]interface{},
	actor string,
) (*domain.AMLReviewCase, error) {
	item, err := s.GetAMLCase(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, pgx.ErrNoRows
	}

	normalizedStatus := strings.ToUpper(strings.TrimSpace(status))
	if normalizedStatus == "" {
		return nil, errors.New("invalid_case_status")
	}
	valid := map[string]bool{
		"IN_REVIEW":    true,
		"ESCALATED":    true,
		"DISMISSED":    true,
		"CONFIRMED":    true,
		"REFERRED_STR": true,
		"CLOSED":       true,
	}
	if !valid[normalizedStatus] {
		return nil, errors.New("invalid_case_status")
	}

	strPayload, _ := json.Marshal(strReferralMetadata)
	resolvedAt := any(nil)
	if normalizedStatus == "DISMISSED" || normalizedStatus == "CONFIRMED" || normalizedStatus == "REFERRED_STR" || normalizedStatus == "CLOSED" {
		resolvedAt = time.Now().UTC()
	}

	updated := &domain.AMLReviewCase{}
	err = scanAMLCase(
		s.db.QueryRow(ctx, `
			UPDATE aml_review_cases
			SET status = $2,
				disposition = NULLIF($3, ''),
				disposition_note = NULLIF($4, ''),
				str_referral_metadata = CASE WHEN $5::jsonb = '{}'::jsonb THEN str_referral_metadata ELSE $5::jsonb END,
				resolved_at = COALESCE($6, resolved_at)
			WHERE id = $1::uuid
			RETURNING
				id,
				case_ref,
				user_id,
				trade_id,
				quote_id,
				case_type,
				severity,
				status,
				reason,
				evidence,
				disposition,
				disposition_note,
				str_referral_metadata,
				assigned_to,
				created_at,
				updated_at,
				resolved_at
		`, item.ID, normalizedStatus, strings.TrimSpace(disposition), strings.TrimSpace(note), string(strPayload), resolvedAt),
		updated,
	)
	if err != nil {
		return nil, err
	}

	eventPayload, _ := json.Marshal(map[string]interface{}{
		"status":      normalizedStatus,
		"disposition": strings.TrimSpace(disposition),
	})
	eventActor := strings.TrimSpace(actor)
	if eventActor == "" {
		eventActor = "compliance"
	}
	_, _ = s.db.Exec(ctx, `
		INSERT INTO aml_case_events (case_id, actor, event_type, note, evidence)
		VALUES ($1::uuid, $2, 'CASE_DISPOSITIONED', $3, $4::jsonb)
	`, updated.ID, eventActor, strings.TrimSpace(note), string(eventPayload))

	return updated, nil
}

func (s *ApplicationService) RecordLegalLaunchApproval(
	ctx context.Context,
	environment string,
	approvalStatus string,
	approvedBy string,
	legalMemoRef string,
	notes string,
	evidence map[string]interface{},
	signedAt *time.Time,
) (*domain.LegalLaunchApproval, error) {
	payload, _ := json.Marshal(evidence)
	item := &domain.LegalLaunchApproval{}
	var evidenceBytes []byte
	err := s.db.QueryRow(ctx, `
		INSERT INTO legal_launch_approvals (
			environment,
			approval_status,
			approved_by,
			legal_memo_ref,
			notes,
			evidence,
			signed_at
		) VALUES (
			$1,
			$2,
			NULLIF($3, ''),
			NULLIF($4, ''),
			NULLIF($5, ''),
			$6::jsonb,
			$7
		)
		ON CONFLICT ((LOWER(environment))) DO UPDATE
		SET approval_status = EXCLUDED.approval_status,
			approved_by = EXCLUDED.approved_by,
			legal_memo_ref = EXCLUDED.legal_memo_ref,
			notes = EXCLUDED.notes,
			evidence = EXCLUDED.evidence,
			signed_at = EXCLUDED.signed_at
		RETURNING id, environment, approval_status, approved_by, legal_memo_ref, notes, evidence, signed_at, created_at, updated_at
	`, strings.ToLower(strings.TrimSpace(environment)), strings.ToUpper(strings.TrimSpace(approvalStatus)), strings.TrimSpace(approvedBy), strings.TrimSpace(legalMemoRef), strings.TrimSpace(notes), string(payload), signedAt).Scan(
		&item.ID,
		&item.Environment,
		&item.ApprovalStatus,
		&item.ApprovedBy,
		&item.LegalMemoRef,
		&item.Notes,
		&evidenceBytes,
		&item.SignedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if len(evidenceBytes) > 0 {
		_ = json.Unmarshal(evidenceBytes, &item.Evidence)
	}
	return item, nil
}

func (s *ApplicationService) RecordDataProtectionEvent(
	ctx context.Context,
	userID string,
	eventType string,
	status string,
	reference string,
	details map[string]interface{},
	createdBy string,
) (*domain.DataProtectionEvent, error) {
	var parsedUserID *uuid.UUID
	if strings.TrimSpace(userID) != "" {
		id, err := uuid.Parse(strings.TrimSpace(userID))
		if err != nil {
			return nil, err
		}
		parsedUserID = &id
	}
	payload, _ := json.Marshal(details)
	item := &domain.DataProtectionEvent{}
	var detailsBytes []byte
	err := s.db.QueryRow(ctx, `
		INSERT INTO data_protection_events (
			user_id,
			event_type,
			status,
			reference,
			details,
			created_by
		) VALUES (
			$1,
			$2,
			$3,
			NULLIF($4, ''),
			$5::jsonb,
			NULLIF($6, '')
		)
		RETURNING id, user_id, event_type, status, reference, details, created_by, created_at, updated_at
	`, parsedUserID, strings.ToUpper(strings.TrimSpace(eventType)), strings.ToUpper(strings.TrimSpace(status)), strings.TrimSpace(reference), string(payload), strings.TrimSpace(createdBy)).Scan(
		&item.ID,
		&item.UserID,
		&item.EventType,
		&item.Status,
		&item.Reference,
		&detailsBytes,
		&item.CreatedBy,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if len(detailsBytes) > 0 {
		_ = json.Unmarshal(detailsBytes, &item.Details)
	}
	return item, nil
}
