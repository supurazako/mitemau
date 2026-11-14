DELETE FROM backup_passkey_ceremonies WHERE stage='code_rotation';
ALTER TABLE backup_passkey_ceremonies MODIFY stage ENUM('verification','registration') NOT NULL;
DROP TABLE recovery_attempt_limits;
DROP TABLE recovery_delivery_limits;
DROP TABLE recovery_requests;
DROP TABLE recovery_codes;
