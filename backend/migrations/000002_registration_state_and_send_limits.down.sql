-- Rolling back loses registrations no longer present in email_verifications.
DROP TABLE email_send_limits;
DROP TABLE verified_registrations;
ALTER TABLE email_verifications DROP KEY email_verifications_sent;
