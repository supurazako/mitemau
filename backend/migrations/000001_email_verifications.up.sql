CREATE TABLE email_verifications (
    email VARBINARY(254) NOT NULL PRIMARY KEY,
    token_hash BINARY(32) NOT NULL,
    code_mac BINARY(32) NULL,
    code_expires_at DATETIME(6) NOT NULL,
    last_sent_at DATETIME(6) NOT NULL,
    failed_attempts SMALLINT UNSIGNED NOT NULL DEFAULT 0,
    verified_until DATETIME(6) NULL,
    UNIQUE KEY email_verifications_token (token_hash)
) ENGINE=InnoDB;
