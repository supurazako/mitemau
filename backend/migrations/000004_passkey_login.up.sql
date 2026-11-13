CREATE TABLE login_ceremonies (
    token_hash BINARY(32) NOT NULL PRIMARY KEY,
    session_data JSON NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    KEY login_ceremonies_expiry (expires_at)
) ENGINE=InnoDB;
