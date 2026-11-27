CREATE TABLE account_profiles (
 account_id BINARY(32) NOT NULL PRIMARY KEY,
 display_id VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 display_name VARCHAR(80) CHARACTER SET utf8mb4 NOT NULL,
 UNIQUE KEY unique_display_id (display_id),
 CONSTRAINT profile_account FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
) ENGINE=InnoDB;
