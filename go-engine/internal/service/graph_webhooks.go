package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"convert-chain/go-engine/internal/statemachine"
)

func (s *ApplicationService) HandleGraphWebhook(ctx context.Context, payload []byte, signature string) error {
	if s.graph == nil {
		return errProviderNotConfigured
	}
	if !s.verifyGraphWebhookSignature(payload, signature) {
		return errInvalidWebhookSignature
	}

	raw, err := decodeWebhookPayload(payload)
	if err != nil {
		return errMalformedWebhookPayload
	}

	eventType := firstJSONString(raw,
		[]string{"type"},
		[]string{"event_type"},
		[]string{"event"},
		[]string{"name"},
		[]string{"data", "type"},
	)
	if eventType == "" {
		eventType = "graph.callback"
	}

	eventID, processed, err := s.ensureWebhookEvent(ctx, "graph", eventType, payload, signature)
	if err != nil {
		return err
	}
	if processed {
		return nil
	}

	payoutID := firstJSONString(raw,
		[]string{"data", "payout", "id"},
		[]string{"data", "object", "id"},
		[]string{"data", "id"},
		[]string{"payout", "id"},
		[]string{"payout_id"},
		[]string{"id"},
	)
	status := firstJSONString(raw,
		[]string{"data", "payout", "status"},
		[]string{"data", "object", "status"},
		[]string{"data", "status"},
		[]string{"payout", "status"},
		[]string{"status"},
	)
	reason := firstJSONString(raw,
		[]string{"data", "reason"},
		[]string{"data", "message"},
		[]string{"data", "failure_reason"},
		[]string{"message"},
		[]string{"reason"},
	)

	if payoutID == "" {
		s.logger.Warn("ignoring graph webhook without payout identifier", "event_type", eventType)
		return s.markWebhookEventProcessed(ctx, eventID, nil)
	}

	trade, err := s.getTradeByGraphPayoutID(ctx, payoutID)
	if err != nil {
		if markErr := s.markWebhookEventProcessed(ctx, eventID, err); markErr != nil {
			return markErr
		}
		return err
	}
	if trade == nil {
		if markErr := s.markWebhookEventProcessed(ctx, eventID, errWebhookTargetNotFound); markErr != nil {
			return markErr
		}
		s.logger.Warn("ignoring graph webhook without a matching trade", "payout_id", payoutID, "event_type", eventType)
		return nil
	}

	normalizedOutcome := normalizeGraphPayoutOutcome(eventType, status)
	switch normalizedOutcome {
	case "completed":
		err = s.MarkPayoutComplete(ctx, trade.ID.String(), payoutID)
	case "failed":
		err = s.MarkPayoutFailed(ctx, trade.ID.String(), payoutID, graphWebhookNote(eventType, status, reason))
	case "pending":
		if trade.Status != string(statemachine.TradePayoutPending) {
			err = s.MarkPayoutPending(ctx, trade.ID.String(), payoutID)
		}
	default:
		s.logger.Info("ignoring non-terminal graph webhook event", "trade_id", trade.ID.String(), "payout_id", payoutID, "event_type", eventType, "status", status)
		return s.markWebhookEventProcessed(ctx, eventID, nil)
	}
	if err != nil {
		if markErr := s.markWebhookEventProcessed(ctx, eventID, err); markErr != nil {
			return markErr
		}
		return err
	}

	return s.markWebhookEventProcessed(ctx, eventID, nil)
}

func (s *ApplicationService) verifyGraphWebhookSignature(payload []byte, signature string) bool {
	secret := strings.TrimSpace(s.options.GraphWebhookSecret)
	if secret == "" {
		return s.graph != nil && s.graph.IsSandbox()
	}

	normalizedSignature := normalizeGraphWebhookSignature(signature)
	if normalizedSignature == "" {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(normalizedSignature))
}

func normalizeGraphWebhookSignature(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	if strings.Contains(trimmed, ",") {
		parts := strings.Split(trimmed, ",")
		for _, part := range parts {
			candidate := normalizeGraphWebhookSignature(part)
			if candidate != "" {
				return candidate
			}
		}
	}

	if strings.Contains(trimmed, "=") {
		prefixes := []string{"sha256", "hmac-sha256", "signature", "sig", "v1"}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) == 2 {
			key := strings.ToLower(strings.TrimSpace(parts[0]))
			value := strings.TrimSpace(parts[1])
			for _, prefix := range prefixes {
				if key == prefix {
					return strings.ToLower(value)
				}
			}
		}
	}

	return strings.ToLower(trimmed)
}

func normalizeGraphPayoutOutcome(eventType, status string) string {
	normalizedEvent := strings.ToUpper(strings.TrimSpace(eventType))
	normalizedStatus := strings.ToUpper(strings.TrimSpace(status))
	combined := normalizedEvent + " " + normalizedStatus

	switch {
	case strings.Contains(combined, "SUCCESS"), strings.Contains(combined, "COMPLETED"), strings.Contains(combined, "PAID"):
		return "completed"
	case strings.Contains(combined, "FAIL"), strings.Contains(combined, "REJECT"), strings.Contains(combined, "CANCEL"), strings.Contains(combined, "REVERSE"):
		return "failed"
	case strings.Contains(combined, "PAYOUT"), strings.Contains(combined, "PENDING"), strings.Contains(combined, "PROCESS"), strings.Contains(combined, "QUEUE"):
		return "pending"
	default:
		return "ignored"
	}
}

func graphWebhookNote(eventType, status, reason string) string {
	parts := make([]string, 0, 3)
	if strings.TrimSpace(eventType) != "" {
		parts = append(parts, strings.TrimSpace(eventType))
	}
	if strings.TrimSpace(status) != "" {
		parts = append(parts, fmt.Sprintf("status=%s", strings.TrimSpace(status)))
	}
	if strings.TrimSpace(reason) != "" {
		parts = append(parts, strings.TrimSpace(reason))
	}
	return strings.Join(parts, "; ")
}
