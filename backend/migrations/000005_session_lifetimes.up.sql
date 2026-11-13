ALTER TABLE account_sessions ADD COLUMN absolute_expires_at DATETIME(6) NULL;
-- Existing sessions retain their original hard deadline. A fresh sign-in starts
-- the new rolling policy, rather than retroactively extending old sessions.
UPDATE account_sessions SET absolute_expires_at=expires_at;
ALTER TABLE account_sessions MODIFY absolute_expires_at DATETIME(6) NOT NULL;
CREATE INDEX account_sessions_absolute ON account_sessions (absolute_expires_at);
