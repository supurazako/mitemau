# Account recovery

Registration and successful recovery issue ten single-use recovery codes, each containing 128 random bits. Save or download them when displayed: the server stores domain-separated SHA-256 hashes, and the browser keeps plaintext only in memory until dismissal or reload. Codes have no time limit; using recovery or replacing the set invalidates every old code.

Already registered, lost the download, or missed the response? While signed in, select **Replace recovery codes** and verify a Passkey again. Success returns ten new codes and invalidates old codes and pending recoveries. It preserves Passkeys and login sessions. This also works for accounts created before recovery was implemented. A normal session or a backup-Passkey challenge alone cannot authorize replacement.

## Recovery flow

1. Select **Recover account**, then enter the account email and one saved offline code.
2. The server validates both before sending a code to that account's verified email address.
3. Enter the email code. Both factors are required; email access alone is insufficient.
4. Register a new discoverable Passkey with user verification required.
5. In one transaction, replace all old Passkeys, invalidate every old login session, replace the recovery-code set, invalidate all pending recoveries, and create a new authenticated session.

Until step 5 succeeds, old Passkeys, sessions and recovery codes remain unchanged. Invalid registration responses consume their challenge but leave the verified recovery state available for another attempt. Browser cancellation can retry without email verification until that state expires. Restarting or reloading the recovery UI requires entering the saved offline code and requesting email again; plaintext codes are not restored from storage. New recovery codes must be saved after success; a lost success response can be handled by signing in with the new Passkey and replacing codes again.

## Limits

- Recovery email: six digits, `EMAIL_CODE_TTL` (initially 10 minutes), at most `EMAIL_MAX_ATTEMPTS` failed checks (five), and `EMAIL_RESEND_INTERVAL` between sends (60 seconds).
- `EMAIL_MAX_SENDS_PER_SOURCE` / `EMAIL_SEND_WINDOW` (20 per hour) also cap recovery starts, including invalid offline-code attempts. Recovery sends share the existing registration send-source budget and have an additional account-wide send cap using the same settings.
- Email verification grants `REGISTRATION_TTL` (30 minutes) for enrollment; each challenge lasts at most `PASSKEY_CHALLENGE_TTL` (five minutes), capped by the grant's remaining lifetime. These reads and retries do not extend the grant.
- Resending replaces the current browser's flow and email code. Other browsers' verified flows retain their original deadline, but completing recovery or replacing codes invalidates all flows.
- Browser-bound opaque HttpOnly/SameSite=Strict cookies, Secure in HTTPS deployments, link the recovery steps. JSON/origin protections apply to every mutation. Recovery state and email MACs are independent of registration state. Messages and codes are not logged.

## Verification

Stop the backend and apply migration 7 using `make migrate-up`, then restart with `make backend`. Existing accounts receive no plaintext codes through migration; use authenticated replacement to obtain a set.

Disposable MySQL 9.7.4 integration tests with the race detector cover successful recovery, missing/invalid factors, expiry, attempt and send limits, resend, replay, invalid WebAuthn responses, concurrent completion, pending old-key login rejection, fresh verification for replacement, and rollback when credential persistence fails. Frontend build/lint and Go vet pass.

Chrome for Testing 158.0.8127.18 with virtual CTAP2.1 authenticators verifies code display/download, authenticated replacement, recovery, cancellation without revocation, old-session/old-credential rejection, replacement-code display, reload, and new-key login. Cancellation is simulated. Physical authenticators and provider-selection UI remain unverified.
