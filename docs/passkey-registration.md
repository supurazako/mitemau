# Passkey registration

Uses `go-webauthn/webauthn` v0.18.2 and native browser WebAuthn APIs. Both discoverable credentials and authenticator user verification are required.

Stop the API, run `make migrate-up`, then restart with `make backend`. Run `make frontend` separately. Use **http://localhost:5173**: the RP ID defaults to `localhost`, not `127.0.0.1`. `APP_ORIGIN` must match the frontend origin; public deployment requires HTTPS and an explicitly configured `WEBAUTHN_RP_ID`. Credentials created for localhost are not credentials for the public domain.

After email verification, select **Register a Passkey**. Successful registration atomically stores the account, full credential record, and a server-side session, consuming the browser's verified email state. The session cookie is opaque and HttpOnly. Reloading restores the signed-in display.

Challenges expire after five minutes (or earlier if email verification expires); sessions expire after 24 hours. Configure these with `PASSKEY_CHALLENGE_TTL` and `SESSION_TTL`. Cancellation leaves email verification usable until its original expiry. A fresh attempt replaces the previous challenge; completed invalid attempts consume the challenge. Account email uniqueness is enforced in MySQL, including concurrent registrations.

Normal sign-in, sign-out and session renewal follow in #17. Backup Passkeys follow in #18; recovery-code issuance and recovery follow in #19. Initial registration creates no recovery codes yet.

## Verification

- `go test -race ./...` and `go vet ./...` in `backend`; set `MYSQL_TEST_DSN` to an explicitly disposable, migrated database for integration tests.
- `npm run build` and `npm run lint` in `frontend`.
- Automated browser check: Chrome for Testing **158.0.8127.18**, headless, with a CDP CTAP2.1 virtual authenticator supporting resident keys and user verification. Confirmed native JSON conversion APIs, email delivery through Mailpit, registration, persisted resident credential, automatic sign-in, reload, and duplicate email rejection.
- Real-device Passkey storage and PIN/biometric prompts remain for human verification. The virtual authenticator result does not establish compatibility with every device or password manager.
