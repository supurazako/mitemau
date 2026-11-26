CREATE TABLE account_broadcast_permissions (
 account_id BINARY(32) NOT NULL PRIMARY KEY,
 broadcasting_allowed BOOLEAN NOT NULL DEFAULT TRUE,
 suspension_reason TEXT NULL,
 CONSTRAINT broadcast_permission_account FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE
) ENGINE=InnoDB;
CREATE TABLE broadcast_permission_actions (
 event_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
 reason TEXT NOT NULL,
 public_reason TEXT NULL,
 expires_at DATETIME(6) NOT NULL,
 KEY broadcast_action_expiry (expires_at)
) ENGINE=InnoDB;
CREATE TABLE stream_termination_notices (
 stream_id BINARY(16) NOT NULL PRIMARY KEY,
 reason TEXT NOT NULL,
 expires_at DATETIME(6) NOT NULL,
 KEY termination_notice_expiry (expires_at),
 CONSTRAINT termination_notice_stream FOREIGN KEY(stream_id) REFERENCES live_streams(id) ON DELETE CASCADE
) ENGINE=InnoDB;
