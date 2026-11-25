CREATE TABLE moderation_events (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
 kind VARCHAR(40) NOT NULL,
 actor_id BINARY(32) NOT NULL,
 account_id BINARY(32) NOT NULL,
 stream_id BINARY(16) NULL,
 comment_id BINARY(16) NULL,
 report_id BINARY(16) NULL,
 occurred_at DATETIME(6) NOT NULL,
 expires_at DATETIME(6) NOT NULL,
 KEY activity_lookup (account_id, occurred_at, id),
 KEY activity_actor (actor_id, occurred_at, id),
 KEY activity_expiry (expires_at)
) ENGINE=InnoDB;

-- Content has its own lifecycle: a still-public comment has no hiding deadline.
CREATE TABLE moderation_comment_content (
 comment_id BINARY(16) NOT NULL PRIMARY KEY,
 account_id BINARY(32) NOT NULL,
 stream_id BINARY(16) NOT NULL,
 body TEXT NOT NULL,
 posted_at DATETIME(6) NOT NULL,
 hidden_at DATETIME(6) NULL,
 expires_at DATETIME(6) NULL,
 KEY comment_stream (stream_id),
 KEY comment_expiry (expires_at)
) ENGINE=InnoDB;

-- Deliberately independent of source-row deletion; report snapshots expire separately.
CREATE TABLE moderation_evidence (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
 report_id BINARY(16) NOT NULL,
 event_id BIGINT UNSIGNED NULL,
 source_key VARCHAR(80) NOT NULL,
 kind VARCHAR(40) NOT NULL,
 actor_id BINARY(32) NOT NULL,
 account_id BINARY(32) NOT NULL,
 stream_id BINARY(16) NULL,
 comment_id BINARY(16) NULL,
 occurred_at DATETIME(6) NOT NULL,
 body TEXT NULL,
 accepted_at DATETIME(6) NOT NULL,
 expires_at DATETIME(6) NOT NULL,
 UNIQUE KEY report_source (report_id,source_key),
 KEY evidence_lookup (account_id,occurred_at,id),
 KEY evidence_actor (actor_id,occurred_at,id),
 KEY evidence_expiry (expires_at)
) ENGINE=InnoDB;
