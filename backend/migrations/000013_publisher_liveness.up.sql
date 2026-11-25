ALTER TABLE stream_publishing_grants ADD COLUMN last_relay_seen_at DATETIME(6) NULL;
ALTER TABLE stream_participants
 ADD COLUMN publisher_seen_at DATETIME(6) NULL,
 ADD COLUMN publishing_state ENUM('stopped','publishing','disconnected') NOT NULL DEFAULT 'stopped';
-- Existing connections have no measured lease. Require a fresh sending session.
UPDATE stream_participants SET publishing_state='disconnected',publisher_namespace=NULL WHERE publisher_namespace IS NOT NULL;
UPDATE stream_publishing_grants SET revoked_at=UTC_TIMESTAMP(6) WHERE revoked_at IS NULL;
