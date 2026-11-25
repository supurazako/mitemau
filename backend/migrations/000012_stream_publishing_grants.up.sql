CREATE TABLE stream_publishing_grants (
    stream_id BINARY(16) NOT NULL,
    account_id BINARY(32) NOT NULL,
    session_id BINARY(16) NOT NULL,
    token_hash BINARY(32) NOT NULL,
    revoked_at DATETIME(6) NULL,
    PRIMARY KEY (stream_id, account_id, session_id),
    UNIQUE KEY publishing_token (token_hash),
    CONSTRAINT publishing_participant FOREIGN KEY (stream_id, account_id)
        REFERENCES stream_participants(stream_id, account_id) ON DELETE CASCADE
) ENGINE=InnoDB;
