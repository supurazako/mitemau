ALTER TABLE email_verifications ADD KEY email_verifications_sent (last_sent_at);

CREATE TABLE verified_registrations (
    token_hash BINARY(32) NOT NULL PRIMARY KEY,
    email VARBINARY(254) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    KEY verified_registrations_expiry (expires_at)
) ENGINE=InnoDB;

INSERT INTO verified_registrations (token_hash,email,expires_at)
SELECT token_hash,email,verified_until FROM email_verifications
WHERE verified_until IS NOT NULL;

CREATE TABLE email_send_limits (
    source_hash BINARY(32) NOT NULL PRIMARY KEY,
    window_start DATETIME(6) NOT NULL,
    sent_count INT UNSIGNED NOT NULL DEFAULT 0,
    KEY email_send_limits_window (window_start)
) ENGINE=InnoDB;
