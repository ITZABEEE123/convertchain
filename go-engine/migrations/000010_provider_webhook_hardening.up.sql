ALTER TABLE webhook_events
  ADD COLUMN IF NOT EXISTS provider_event_id VARCHAR(160),
  ADD COLUMN IF NOT EXISTS payload_sha256 CHAR(64);

UPDATE webhook_events
SET payload_sha256 = encode(digest(payload::text, 'sha256'), 'hex')
WHERE payload_sha256 IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_events_source_provider_event
  ON webhook_events(source, provider_event_id)
  WHERE provider_event_id IS NOT NULL AND provider_event_id <> '';

CREATE INDEX IF NOT EXISTS idx_webhook_events_source_payload_hash
  ON webhook_events(source, payload_sha256)
  WHERE payload_sha256 IS NOT NULL AND payload_sha256 <> '';

CREATE INDEX IF NOT EXISTS idx_webhook_events_source_created
  ON webhook_events(source, created_at DESC);
