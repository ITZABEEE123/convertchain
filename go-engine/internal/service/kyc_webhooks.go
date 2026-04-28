package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	errProviderNotConfigured   = errors.New("provider_not_configured")
	errInvalidWebhookSignature = errors.New("invalid_webhook_signature")
	errMalformedWebhookPayload = errors.New("malformed_webhook_payload")
	errWebhookTargetNotFound   = errors.New("webhook_target_not_found")
	errUnsupportedWebhookEvent = errors.New("unsupported_webhook_event")
)

func (s *ApplicationService) HandleSmileIDWebhook(ctx context.Context, payload []byte, signature, timestamp string) error {
	if s.kycOrchestrator == nil || !s.kycOrchestrator.SupportsTier1() {
		return errProviderNotConfigured
	}
	if !s.kycOrchestrator.VerifySmileIDCallback(signature, timestamp) {
		return errInvalidWebhookSignature
	}

	raw, err := decodeWebhookPayload(payload)
	if err != nil {
		return errMalformedWebhookPayload
	}

	eventType := firstJSONString(raw,
		[]string{"callback_type"},
		[]string{"type"},
		[]string{"event_type"},
	)
	if eventType == "" {
		eventType = "smileid.callback"
	}

	providerEventID := firstJSONString(raw,
		[]string{"event_id"},
		[]string{"eventId"},
		[]string{"job_id"},
		[]string{"jobId"},
		[]string{"reference_id"},
	)
	eventID, processed, err := s.ensureWebhookEvent(ctx, "smileid", eventType, payload, signature, providerEventID)
	if err != nil {
		return err
	}
	if processed {
		return nil
	}

	userID := firstJSONString(raw,
		[]string{"partner_params", "user_id"},
		[]string{"partner_params", "userId"},
		[]string{"partnerParams", "user_id"},
		[]string{"partnerParams", "userId"},
		[]string{"user_id"},
		[]string{"userId"},
	)
	providerRef := firstJSONString(raw,
		[]string{"result", "SmileJobID"},
		[]string{"SmileJobID"},
		[]string{"job_id"},
		[]string{"jobId"},
		[]string{"reference_id"},
	)
	status := normalizeWebhookKYCStatus(firstJSONString(raw,
		[]string{"status"},
		[]string{"result", "ResultText"},
		[]string{"result", "result_text"},
		[]string{"result", "status"},
	))
	if status == "" {
		status = "PENDING"
	}
	reason := firstJSONString(raw,
		[]string{"reason"},
		[]string{"message"},
		[]string{"result", "detail"},
		[]string{"result", "error_message"},
		[]string{"result", "ResultText"},
	)
	tier := normalizeWebhookTier("smile_id", firstJSONString(raw, []string{"tier"}))

	return s.handleKYCWebhookEvent(ctx, eventID, "smile_id", userID, providerRef, status, tier, reason)
}

func (s *ApplicationService) HandleSumsubWebhook(ctx context.Context, payload []byte, digest, algorithm string) error {
	if s.kycOrchestrator == nil || !s.kycOrchestrator.SupportsTier2() {
		return errProviderNotConfigured
	}
	if !s.kycOrchestrator.VerifySumsubWebhook(payload, digest, algorithm, s.options.SumsubWebhookSecret) {
		return errInvalidWebhookSignature
	}

	raw, err := decodeWebhookPayload(payload)
	if err != nil {
		return errMalformedWebhookPayload
	}

	eventType := firstJSONString(raw,
		[]string{"type"},
		[]string{"event_type"},
	)
	if eventType == "" {
		eventType = "sumsub.callback"
	}

	providerEventID := firstJSONString(raw,
		[]string{"correlationId"},
		[]string{"eventId"},
		[]string{"event_id"},
		[]string{"id"},
	)
	if providerEventID == "" {
		parts := []string{}
		for _, part := range []string{
			firstJSONString(raw, []string{"applicantId"}, []string{"applicant_id"}),
			firstJSONString(raw, []string{"inspectionId"}, []string{"inspection_id"}),
			firstJSONString(raw, []string{"createdAtMs"}, []string{"created_at_ms"}),
			eventType,
		} {
			if strings.TrimSpace(part) != "" {
				parts = append(parts, strings.TrimSpace(part))
			}
		}
		if len(parts) > 1 {
			providerEventID = strings.Join(parts, ":")
		}
	}
	eventID, processed, err := s.ensureWebhookEvent(ctx, "sumsub", eventType, payload, digest, providerEventID)
	if err != nil {
		return err
	}
	if processed {
		return nil
	}

	userID := firstJSONString(raw,
		[]string{"externalUserId"},
		[]string{"external_user_id"},
		[]string{"review", "externalUserId"},
	)
	providerRef := firstJSONString(raw,
		[]string{"applicantId"},
		[]string{"applicant_id"},
		[]string{"inspectionId"},
	)
	status := normalizeSumsubKYCStatus(raw)
	if status == "" {
		status = "PENDING"
	}
	reason := firstJSONString(raw,
		[]string{"reviewResult", "moderationComment"},
		[]string{"reviewResult", "clientComment"},
		[]string{"review_result", "moderation_comment"},
		[]string{"message"},
		[]string{"reason"},
	)
	tier := normalizeWebhookTier("sumsub", firstJSONString(raw,
		[]string{"targetTier"},
		[]string{"target_tier"},
		[]string{"tier"},
		[]string{"levelName"},
	))

	return s.handleKYCWebhookEvent(ctx, eventID, "sumsub", userID, providerRef, status, tier, reason)
}

func (s *ApplicationService) handleKYCWebhookEvent(ctx context.Context, eventID uuid.UUID, provider, userID, providerRef, status, tier, reason string) error {
	resolvedUserID, err := s.resolveWebhookTargetUserID(ctx, provider, userID, providerRef)
	if err != nil {
		if markErr := s.markWebhookEventProcessed(ctx, eventID, err); markErr != nil {
			return markErr
		}
		if errors.Is(err, errWebhookTargetNotFound) {
			s.logger.Warn("ignoring KYC webhook without a known user target", "provider", provider, "provider_ref", providerRef)
			return nil
		}
		return err
	}

	if err := s.applyKYCWebhookOutcome(ctx, resolvedUserID, provider, providerRef, status, tier, reason); err != nil {
		if markErr := s.markWebhookEventProcessed(ctx, eventID, err); markErr != nil {
			return markErr
		}
		if errors.Is(err, errUnsupportedWebhookEvent) {
			s.logger.Warn("ignoring unsupported KYC webhook event", "provider", provider, "user_id", resolvedUserID, "provider_ref", providerRef, "status", status)
			return nil
		}
		return err
	}

	return s.markWebhookEventProcessed(ctx, eventID, nil)
}

func (s *ApplicationService) applyKYCWebhookOutcome(ctx context.Context, userID, provider, providerRef, status, tier, reason string) error {
	userUUID, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil {
		return errWebhookTargetNotFound
	}

	normalizedStatus := normalizeWebhookKYCStatus(status)
	if normalizedStatus == "" {
		return errUnsupportedWebhookEvent
	}

	normalizedTier := strings.TrimSpace(tier)
	if normalizedTier == "" {
		normalizedTier, err = s.defaultWebhookTier(ctx, userUUID, provider)
		if err != nil {
			return err
		}
	}

	switch normalizedStatus {
	case "APPROVED", "REJECTED":
		if err := s.SaveKYCResult(ctx, userUUID, normalizedTier, normalizedStatus); err != nil {
			return err
		}
	case "PENDING":
		if provider == "smile_id" {
			if err := s.SaveKYCResult(ctx, userUUID, normalizedTier, normalizedStatus); err != nil {
				return err
			}
		}
	default:
		return errUnsupportedWebhookEvent
	}

	verified, verifiedAt, rejectionReason := webhookOutcomeFields(normalizedStatus, reason)
	if err := s.updateKYCWebhookDocuments(ctx, userUUID, provider, providerRef, verified, verifiedAt, rejectionReason); err != nil {
		return err
	}
	if normalizedStatus == "APPROVED" {
		if err := s.enqueueKYCVerifiedNotification(ctx, userUUID, normalizedTier); err != nil && s.logger != nil {
			s.logger.Warn("failed to enqueue kyc verified notification", "user_id", userUUID.String(), "tier", normalizedTier, "error", err)
		}
	}
	return nil
}

func (s *ApplicationService) defaultWebhookTier(ctx context.Context, userID uuid.UUID, provider string) (string, error) {
	switch provider {
	case "smile_id":
		return "TIER_1", nil
	case "sumsub":
		currentTier, err := s.GetUserKYCTier(ctx, userID)
		if err != nil {
			return "", err
		}
		return nextKYCTier(normalizePersistedKYCTier(currentTier)), nil
	default:
		return "TIER_1", nil
	}
}

func (s *ApplicationService) enqueueKYCVerifiedNotification(ctx context.Context, userID uuid.UUID, tier string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var firstName string
	var passwordSet bool
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(first_name, ''), txn_password_hash IS NOT NULL
		FROM users
		WHERE id = $1::uuid
	`, userID).Scan(&firstName, &passwordSet); err != nil {
		return err
	}
	if passwordSet {
		return tx.Commit(ctx)
	}

	normalizedTier := normalizeKYCTier(tier)
	payload := map[string]interface{}{
		"user_id":    userID.String(),
		"first_name": strings.TrimSpace(firstName),
		"kyc_tier":   normalizedTier,
		"next_step":  "SET_TRANSACTION_PASSWORD",
	}
	dedupeKey := fmt.Sprintf("kyc_verified:%s:%s", userID.String(), normalizedTier)
	if err := s.enqueueNotificationTx(ctx, tx, userID, nil, "kyc.verified", payload, dedupeKey); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *ApplicationService) resolveWebhookTargetUserID(ctx context.Context, provider, userID, providerRef string) (string, error) {
	normalizedUserID := strings.TrimSpace(userID)
	if normalizedUserID != "" {
		if _, err := uuid.Parse(normalizedUserID); err == nil {
			return normalizedUserID, nil
		}
	}

	normalizedProviderRef := strings.TrimSpace(providerRef)
	if normalizedProviderRef == "" {
		return "", errWebhookTargetNotFound
	}

	resolvedUserID, err := s.findUserIDByProviderRef(ctx, provider, normalizedProviderRef)
	if err != nil {
		return "", err
	}
	if resolvedUserID == "" {
		return "", errWebhookTargetNotFound
	}

	return resolvedUserID, nil
}

func (s *ApplicationService) findUserIDByProviderRef(ctx context.Context, provider, providerRef string) (string, error) {
	var userID string
	err := s.db.QueryRow(ctx, `
        SELECT user_id::text
        FROM kyc_documents
        WHERE provider = $1 AND provider_ref = $2
        ORDER BY created_at DESC
        LIMIT 1
    `, provider, providerRef).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return userID, nil
}

func (s *ApplicationService) ensureWebhookEvent(ctx context.Context, source, eventType string, payload []byte, signature string, providerEventID string) (uuid.UUID, bool, error) {
	var eventID uuid.UUID
	var processed bool
	payloadHash := sha256.Sum256(payload)
	payloadSHA256 := hex.EncodeToString(payloadHash[:])
	normalizedProviderEventID := strings.TrimSpace(providerEventID)

	if normalizedProviderEventID != "" {
		err := s.db.QueryRow(ctx, `
        SELECT id, processed
        FROM webhook_events
        WHERE source = $1
          AND provider_event_id = $2
        ORDER BY created_at DESC
        LIMIT 1
    `, source, normalizedProviderEventID).Scan(&eventID, &processed)
		if err == nil {
			return eventID, processed, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, err
		}
	}

	err := s.db.QueryRow(ctx, `
        SELECT id, processed
        FROM webhook_events
        WHERE source = $1
          AND payload_sha256 = $2
        ORDER BY created_at DESC
        LIMIT 1
    `, source, payloadSHA256).Scan(&eventID, &processed)
	if err == nil {
		return eventID, processed, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, err
	}

	err = s.db.QueryRow(ctx, `
        INSERT INTO webhook_events (source, event_type, payload, signature, provider_event_id, payload_sha256)
        VALUES ($1, $2, $3::jsonb, NULLIF($4, ''), NULLIF($5, ''), $6)
        RETURNING id, processed
    `, source, eventType, string(payload), strings.TrimSpace(signature), normalizedProviderEventID, payloadSHA256).Scan(&eventID, &processed)
	if err != nil {
		if strings.Contains(err.Error(), "webhook_events_source_provider_event") || strings.Contains(err.Error(), "webhook_events_source_payload_hash") {
			err = s.db.QueryRow(ctx, `
                SELECT id, processed
                FROM webhook_events
                WHERE source = $1
                  AND (
                    (provider_event_id IS NOT NULL AND provider_event_id = NULLIF($2, ''))
                    OR payload_sha256 = $3
                  )
                ORDER BY created_at DESC
                LIMIT 1
            `, source, normalizedProviderEventID, payloadSHA256).Scan(&eventID, &processed)
			if err == nil {
				return eventID, processed, nil
			}
		}
		return uuid.Nil, false, err
	}

	return eventID, processed, nil
}

func normalizeSumsubKYCStatus(raw map[string]any) string {
	reviewAnswer := firstJSONString(raw,
		[]string{"reviewResult", "reviewAnswer"},
		[]string{"review_result", "review_answer"},
	)
	if reviewAnswer != "" {
		return normalizeWebhookKYCStatus(reviewAnswer)
	}

	status := strings.ToUpper(strings.TrimSpace(firstJSONString(raw,
		[]string{"reviewStatus"},
		[]string{"review_status"},
		[]string{"status"},
	)))
	switch status {
	case "COMPLETED":
		return "PENDING"
	default:
		return normalizeWebhookKYCStatus(status)
	}
}

func (s *ApplicationService) markWebhookEventProcessed(ctx context.Context, eventID uuid.UUID, processErr error) error {
	errorText := ""
	if processErr != nil {
		errorText = processErr.Error()
	}

	_, err := s.db.Exec(ctx, `
        UPDATE webhook_events
        SET processed = TRUE,
            processed_at = $2,
            error = NULLIF($3, '')
        WHERE id = $1::uuid
    `, eventID, time.Now().UTC(), errorText)
	return err
}

func (s *ApplicationService) updateKYCWebhookDocuments(ctx context.Context, userID uuid.UUID, provider, providerRef string, verified *bool, verifiedAt *time.Time, rejectionReason string) error {
	docTypes := webhookDocTypes(provider)
	if len(docTypes) == 0 {
		return nil
	}

	_, err := s.db.Exec(ctx, `
        UPDATE kyc_documents
        SET provider = $2,
            provider_ref = COALESCE(NULLIF($3, ''), provider_ref),
            verified = $4,
            verified_at = $5,
            rejected_reason = NULLIF($6, '')
        WHERE user_id = $1::uuid
          AND doc_type::text = ANY($7::text[])
    `, userID, provider, providerRef, verified, verifiedAt, strings.TrimSpace(rejectionReason), docTypes)
	return err
}

func webhookDocTypes(provider string) []string {
	switch provider {
	case "smile_id":
		return []string{"NIN", "BVN"}
	case "sumsub":
		return []string{"SELFIE", "PROOF_OF_ADDRESS"}
	default:
		return nil
	}
}

func decodeWebhookPayload(payload []byte) (map[string]any, error) {
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("empty payload")
	}
	return raw, nil
}

func firstJSONString(raw map[string]any, paths ...[]string) string {
	for _, path := range paths {
		value := valueAtPath(raw, path)
		if value == nil {
			continue
		}
		normalized := stringifyJSONValue(value)
		if normalized != "" {
			return normalized
		}
	}
	return ""
}

func valueAtPath(value any, path []string) any {
	current := value
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		next, ok := object[key]
		if !ok {
			return nil
		}
		current = next
	}
	return current
}

func stringifyJSONValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	case float64:
		return strings.TrimSpace(fmt.Sprintf("%v", typed))
	case int:
		return strings.TrimSpace(fmt.Sprintf("%d", typed))
	case int64:
		return strings.TrimSpace(fmt.Sprintf("%d", typed))
	case bool:
		return strings.TrimSpace(fmt.Sprintf("%t", typed))
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", typed))
	}
}

func normalizeWebhookKYCStatus(raw string) string {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	if normalized == "" {
		return ""
	}

	switch normalized {
	case "APPROVED", "APPROVE", "VALID", "VERIFIED", "GREEN", "SUCCESS", "COMPLETED", "CLEARED":
		return "APPROVED"
	case "REJECTED", "DECLINED", "DENIED", "FAILED", "ERROR", "INVALID", "RED":
		return "REJECTED"
	case "PENDING", "PROCESSING", "UNDER_REVIEW", "QUEUED", "ON_HOLD", "REVIEW_PENDING":
		return "PENDING"
	}

	switch {
	case strings.Contains(normalized, "APPROV"), strings.Contains(normalized, "SUCCESS"), strings.Contains(normalized, "GREEN"), strings.Contains(normalized, "VALID"):
		return "APPROVED"
	case strings.Contains(normalized, "REJECT"), strings.Contains(normalized, "DECLIN"), strings.Contains(normalized, "FAIL"), strings.Contains(normalized, "DENIED"), strings.Contains(normalized, "RED"):
		return "REJECTED"
	default:
		return "PENDING"
	}
}

func normalizeWebhookTier(provider, raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}

	normalized := strings.ToUpper(strings.TrimSpace(raw))
	switch normalized {
	case "TELEGRAM-TIER2", "TIER2", "TIER_2":
		return "TIER_2"
	case "TELEGRAM-TIER3", "TIER3", "TIER_3":
		return "TIER_3"
	case "TELEGRAM-TIER4", "TIER4", "TIER_4":
		return "TIER_4"
	case "TELEGRAM-TIER1", "TIER1", "TIER_1":
		return "TIER_1"
	}

	if provider == "smile_id" {
		return "TIER_1"
	}

	return normalizeKYCTier(normalized)
}

func normalizePersistedKYCTier(raw string) string {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	if normalized == "" {
		return "TIER_0"
	}

	switch normalized {
	case "TIER_0", "TIER0":
		return "TIER_0"
	default:
		return normalizeKYCTier(normalized)
	}
}

func nextKYCTier(current string) string {
	switch normalizePersistedKYCTier(current) {
	case "TIER_0":
		return "TIER_1"
	case "TIER_1":
		return "TIER_2"
	case "TIER_2":
		return "TIER_3"
	case "TIER_3":
		return "TIER_4"
	default:
		return "TIER_4"
	}
}

func kycTierRank(tier string) int {
	switch normalizePersistedKYCTier(tier) {
	case "TIER_0":
		return 0
	case "TIER_1":
		return 1
	case "TIER_2":
		return 2
	case "TIER_3":
		return 3
	case "TIER_4":
		return 4
	default:
		return 0
	}
}

func webhookOutcomeFields(status, reason string) (*bool, *time.Time, string) {
	normalizedStatus := normalizeWebhookKYCStatus(status)
	switch normalizedStatus {
	case "APPROVED":
		verified := true
		now := time.Now().UTC()
		return &verified, &now, ""
	case "REJECTED":
		verified := false
		return &verified, nil, strings.TrimSpace(reason)
	default:
		return nil, nil, strings.TrimSpace(reason)
	}
}
