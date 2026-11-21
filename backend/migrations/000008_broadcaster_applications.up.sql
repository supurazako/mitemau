CREATE TABLE broadcaster_applications (
    account_id BINARY(32) NOT NULL PRIMARY KEY,
    requested_at DATETIME(6) NOT NULL,
    approved_at DATETIME(6) NULL,
    approved_by BINARY(32) NULL,
    CONSTRAINT broadcaster_account FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    CONSTRAINT broadcaster_approver FOREIGN KEY (approved_by) REFERENCES accounts(id) ON DELETE SET NULL
) ENGINE=InnoDB;
