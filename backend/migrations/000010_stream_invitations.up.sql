CREATE TABLE stream_invitations (
    stream_id BINARY(16) NOT NULL,
    account_id BINARY(32) NOT NULL,
    invited_at DATETIME(6) NOT NULL,
    PRIMARY KEY (stream_id, account_id),
    CONSTRAINT invitation_stream FOREIGN KEY (stream_id) REFERENCES live_streams(id) ON DELETE CASCADE,
    CONSTRAINT invitation_account FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
) ENGINE=InnoDB;
