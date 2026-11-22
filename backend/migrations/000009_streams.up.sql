CREATE TABLE live_streams (
    id BINARY(16) NOT NULL PRIMARY KEY,
    creator_id BINARY(32) NOT NULL,
    title VARCHAR(200) NOT NULL,
    state ENUM('ready','live','ended') NOT NULL DEFAULT 'ready',
    created_at DATETIME(6) NOT NULL,
    started_at DATETIME(6) NULL,
    ended_at DATETIME(6) NULL,
    CONSTRAINT stream_creator FOREIGN KEY (creator_id) REFERENCES accounts(id),
    KEY creator_streams (creator_id, created_at)
) ENGINE=InnoDB;

CREATE TABLE stream_participants (
    stream_id BINARY(16) NOT NULL,
    account_id BINARY(32) NOT NULL,
    joined_at DATETIME(6) NOT NULL,
    left_at DATETIME(6) NULL,
    PRIMARY KEY (stream_id, account_id),
    CONSTRAINT participant_stream FOREIGN KEY (stream_id) REFERENCES live_streams(id) ON DELETE CASCADE,
    CONSTRAINT participant_account FOREIGN KEY (account_id) REFERENCES accounts(id)
) ENGINE=InnoDB;
