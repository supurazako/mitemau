ALTER TABLE account_comment_permissions ADD COLUMN suspension_reason TEXT NULL;
CREATE TABLE comment_permission_actions (
 event_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
 reason TEXT NOT NULL,
 public_reason TEXT NULL,
 expires_at DATETIME(6) NOT NULL,
 KEY comment_action_expiry (expires_at)
) ENGINE=InnoDB;
