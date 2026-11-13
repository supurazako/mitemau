-- Keep any already renewed idle deadline when returning to the old policy.
ALTER TABLE account_sessions DROP KEY account_sessions_absolute;
ALTER TABLE account_sessions DROP COLUMN absolute_expires_at;
