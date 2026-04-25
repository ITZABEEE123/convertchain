DROP INDEX IF EXISTS idx_webhook_events_source_created;
DROP INDEX IF EXISTS idx_webhook_events_source_payload_hash;
DROP INDEX IF EXISTS idx_webhook_events_source_provider_event;

ALTER TABLE webhook_events
  DROP COLUMN IF EXISTS payload_sha256,
  DROP COLUMN IF EXISTS provider_event_id;
