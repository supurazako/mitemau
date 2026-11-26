CREATE TABLE user_reports (
 id BINARY(16) NOT NULL PRIMARY KEY,
 reporter_id BINARY(32) NOT NULL,
 target_id BINARY(32) NOT NULL,
 reason TEXT NOT NULL,
 incident_at DATETIME(6) NOT NULL,
 stream_id BINARY(16) NULL,
 comment_id BINARY(16) NULL,
 state ENUM('open','resolved') NOT NULL DEFAULT 'open',
 outcome TEXT NULL,
 resolved_by BINARY(32) NULL,
 resolved_at DATETIME(6) NULL,
 created_at DATETIME(6) NOT NULL,
 expires_at DATETIME(6) NOT NULL,
 KEY report_queue (state,created_at,id),
 KEY report_expiry (expires_at)
) ENGINE=InnoDB;
CREATE TABLE report_cooldowns (
 account_id BINARY(32) NOT NULL PRIMARY KEY,
 next_report_at DATETIME(6) NOT NULL,
 CONSTRAINT report_cooldown_account FOREIGN KEY(account_id) REFERENCES accounts(id) ON DELETE CASCADE
) ENGINE=InnoDB;
CREATE TABLE report_outcomes (
 report_id BINARY(16) NOT NULL PRIMARY KEY,
 target_id BINARY(32) NOT NULL,
 actor_id BINARY(32) NOT NULL,
 outcome TEXT NOT NULL,
 occurred_at DATETIME(6) NOT NULL,
 expires_at DATETIME(6) NOT NULL,
 KEY report_outcome_target (target_id,occurred_at),
 KEY report_outcome_expiry (expires_at)
) ENGINE=InnoDB;
