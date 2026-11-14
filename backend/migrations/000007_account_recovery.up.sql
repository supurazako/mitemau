CREATE TABLE recovery_codes (
    code_hash BINARY(32) NOT NULL PRIMARY KEY,
    account_id BINARY(32) NOT NULL,
    CONSTRAINT recovery_codes_account FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE recovery_requests (
    token_hash BINARY(32) NOT NULL PRIMARY KEY,
    account_id BINARY(32) NOT NULL,
    recovery_hash BINARY(32) NOT NULL,
    email_mac BINARY(32) NULL,
    expires_at DATETIME(6) NOT NULL,
    failed_attempts SMALLINT UNSIGNED NOT NULL DEFAULT 0,
    verified_until DATETIME(6) NULL,
    session_data JSON NULL,
    challenge_expires_at DATETIME(6) NULL,
    CONSTRAINT recovery_request_account FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT recovery_request_code FOREIGN KEY (recovery_hash) REFERENCES recovery_codes(code_hash) ON DELETE CASCADE,
    KEY recovery_requests_expiry (expires_at)
) ENGINE=InnoDB;

CREATE TABLE recovery_delivery_limits (
    account_id BINARY(32) NOT NULL PRIMARY KEY,
    last_sent_at DATETIME(6) NOT NULL,
    window_start DATETIME(6) NOT NULL,
    sent_count INT UNSIGNED NOT NULL,
    CONSTRAINT recovery_delivery_account FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE recovery_attempt_limits (
    source_hash BINARY(32) NOT NULL PRIMARY KEY,
    window_start DATETIME(6) NOT NULL,
    attempt_count INT UNSIGNED NOT NULL,
    KEY recovery_attempt_window (window_start)
) ENGINE=InnoDB;

ALTER TABLE backup_passkey_ceremonies MODIFY stage ENUM('verification','registration','code_rotation') NOT NULL;
