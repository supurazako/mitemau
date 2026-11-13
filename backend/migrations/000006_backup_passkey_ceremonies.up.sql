CREATE TABLE backup_passkey_ceremonies (
    session_hash BINARY(32) NOT NULL PRIMARY KEY,
    stage ENUM('verification','registration') NOT NULL,
    session_data JSON NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    CONSTRAINT backup_ceremonies_session FOREIGN KEY (session_hash) REFERENCES account_sessions(token_hash) ON DELETE CASCADE,
    KEY backup_ceremonies_expiry (expires_at)
) ENGINE=InnoDB;
