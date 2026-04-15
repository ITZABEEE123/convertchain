package workers

import (
	"context"
	"log/slog"
	"time"
)

// QuoteExpiryWorker runs every 30 seconds and sets the status of any
// pending quote whose ValidUntil has passed to expired.
//
// Why do we need this? When the pricing engine creates a quote it locks in
// an exchange rate for 120 seconds. If the user never turns the quote into
// a trade, we need to clean it up so stale quotes don't accumulate.
type QuoteExpiryWorker struct {
	quotes   QuoteRepository
	interval time.Duration
	logger   *slog.Logger
}

// NewQuoteExpiryWorker constructs a QuoteExpiryWorker.
func NewQuoteExpiryWorker(
	quotes QuoteRepository,
	interval time.Duration,
	logger *slog.Logger,
) *QuoteExpiryWorker {
	return &QuoteExpiryWorker{
		quotes:   quotes,
		interval: interval,
		logger:   logger,
	}
}

// Run starts the quote expiry loop. Call this in a goroutine.
func (w *QuoteExpiryWorker) Run(ctx context.Context) {
	w.logger.Info("quote expiry worker starting", "interval", w.interval)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	w.runOnce(ctx)

	for {
		select {
		case <-ticker.C:
			w.runOnce(ctx)
		case <-ctx.Done():
			w.logger.Info("quote expiry worker shutting down")
			return
		}
	}
}

// runOnce expires all quotes that have passed their deadline.
func (w *QuoteExpiryWorker) runOnce(ctx context.Context) {
	now := time.Now().UTC()

	expired, err := w.quotes.GetExpiredPendingQuotes(ctx, now)
	if err != nil {
		w.logger.Error("failed to fetch expired quotes", "error", err)
		return
	}

	if len(expired) == 0 {
		return
	}

	w.logger.Info("expiring stale quotes", "count", len(expired))

	for _, quote := range expired {
		if err := w.quotes.ExpireQuote(ctx, quote.ID.String()); err != nil {
			w.logger.Error("failed to expire quote", "quote_id", quote.ID.String(), "error", err)
		} else {
			w.logger.Info("quote expired", "quote_id", quote.ID.String(), "expired_at", quote.ValidUntil)
		}
	}
}
