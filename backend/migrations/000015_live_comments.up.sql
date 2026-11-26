ALTER TABLE live_streams ADD COLUMN comment_version BIGINT UNSIGNED NOT NULL DEFAULT 0;
CREATE TABLE stream_comments (
 sequence BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
 comment_id BINARY(16) NOT NULL,
 request_id BINARY(16) NOT NULL,
 stream_id BINARY(16) NOT NULL,
 account_id BINARY(32) NOT NULL,
 posted_at DATETIME(6) NOT NULL,
 UNIQUE KEY comment_identity(comment_id),
 UNIQUE KEY comment_request(account_id,request_id),
 KEY comment_feed(stream_id,sequence),
 CONSTRAINT comment_stream FOREIGN KEY(stream_id) REFERENCES live_streams(id) ON DELETE CASCADE
) ENGINE=InnoDB;
CREATE TABLE account_comment_permissions (
 account_id BINARY(32) NOT NULL PRIMARY KEY,
 posting_allowed BOOLEAN NOT NULL DEFAULT TRUE,
 CONSTRAINT comment_permission_account FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE
) ENGINE=InnoDB;
CREATE TABLE comment_posting_cooldowns (
 account_id BINARY(32) NOT NULL PRIMARY KEY,
 next_post_at DATETIME(6) NOT NULL,
 CONSTRAINT comment_cooldown_account FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE
) ENGINE=InnoDB;
CREATE TABLE stream_comment_publications (
 stream_id BINARY(16) NOT NULL PRIMARY KEY,
 session_id BINARY(16) NOT NULL,
 token_hash BINARY(32) NOT NULL,
 ready BOOLEAN NOT NULL DEFAULT FALSE,
 last_relay_seen_at DATETIME(6) NULL,
 UNIQUE KEY comment_publisher_token(token_hash),
 CONSTRAINT comment_publication_stream FOREIGN KEY(stream_id) REFERENCES live_streams(id) ON DELETE CASCADE
) ENGINE=InnoDB;
