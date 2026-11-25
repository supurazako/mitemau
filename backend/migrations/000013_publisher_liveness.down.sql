ALTER TABLE stream_participants DROP COLUMN publishing_state,DROP COLUMN publisher_seen_at;
ALTER TABLE stream_publishing_grants DROP COLUMN last_relay_seen_at;
