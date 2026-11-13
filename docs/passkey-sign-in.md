# Passkey sign-in and sessions

Select **Sign in with a Passkey**, choose a discoverable credential, and complete authenticator user verification. No email input or site password is needed. Challenges are browser-bound, single-use, and expire after five minutes. The server verifies the signature, RP ID, origin, user handle, and user-verification flag before creating a session and saving updated credential metadata.

Successful sign-in rotates this browser's opaque session token. **Sign out** invalidates its server-side session and clears its cookies; other browsers remain signed in. Tabs in the same browser refresh their display through BroadcastChannel and on focus. Rejected authentication creates no session. Counter regressions currently reject sign-in with a suggestion to use another Passkey; a regression is a warning, not proof of credential cloning.

## Lifetime and activity

- `SESSION_TTL`: inactivity timeout, initially `24h`.
- `SESSION_ABSOLUTE_TTL`: maximum lifetime from authentication, initially `168h`; renewal never moves this deadline.
- `GET /api/session` reads state without extending either deadline.
- `POST /api/session/renew` extends a still-valid session, capped by the absolute deadline. Expired sessions cannot be renewed.
- The frontend reports visible page visits, pointer/keyboard/scroll interaction, and native media playback. Requests are throttled to once per 30 minutes per tab by default (or half the idle timeout if shorter). Timers only flush recorded activity; timers alone do not count as activity.
- Future MoQ playback/broadcast code must call `reportSessionActivity()` while media is actively flowing. That integration belongs to the streaming implementation; no MoQ player exists yet. Background media activity may count, while merely leaving an idle tab open does not.

Cookies are HttpOnly and SameSite=Strict; HTTPS deployments require Secure cookies. Mutations require JSON and validate the configured origin. [Backup Passkey enrollment](backup-passkeys.md) requires fresh authentication separately; renewal does not provide that authorization.

## Local verification

Stop the backend, run `make migrate-up`, then restart `make backend`; run `make frontend` separately. Migration 5 preserves existing sessions' previous expiry as their absolute deadline. Sign in again to receive the new seven-day maximum lifetime.

MySQL integration tests cover real signed assertions, invalid responses, expired/replayed challenges, concurrent completion, session rotation, logout isolation, renewal deadlines, and cross-origin requests. Run `MYSQL_TEST_DSN=... go test -race ./...` only against a disposable migrated database; also run `go vet ./...`, frontend build and lint.

Automated browser checks use Chrome for Testing 158.0.8127.18 with a virtual CTAP2.1 authenticator. Real-device sign-in and logout remain a manual review step. Recovery is separate work; see [Backup Passkeys](backup-passkeys.md) for additional credential enrollment.
