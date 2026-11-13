CREATE TABLE accounts (
    id BINARY(32) NOT NULL PRIMARY KEY,
    email VARBINARY(254) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    UNIQUE KEY accounts_email (email)
) ENGINE=InnoDB;

CREATE TABLE passkey_credentials (
    id_hash BINARY(32) NOT NULL PRIMARY KEY,
    account_id BINARY(32) NOT NULL,
    credential JSON NOT NULL,
    created_at DATETIME(6) NOT NULL,
    CONSTRAINT credentials_account FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
) ENGINE=InnoDB;

CREATE TABLE registration_ceremonies (
    registration_hash BINARY(32) NOT NULL PRIMARY KEY,
    user_id BINARY(32) NOT NULL,
    session_data JSON NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    CONSTRAINT ceremonies_registration FOREIGN KEY (registration_hash) REFERENCES verified_registrations(token_hash) ON DELETE CASCADE,
    KEY registration_ceremonies_expiry (expires_at)
) ENGINE=InnoDB;

CREATE TABLE account_sessions (
    token_hash BINARY(32) NOT NULL PRIMARY KEY,
    account_id BINARY(32) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    CONSTRAINT sessions_account FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    KEY account_sessions_expiry (expires_at)
) ENGINE=InnoDB;
