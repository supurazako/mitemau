# Backup Passkeys

While signed in, select **Add a Passkey** and verify an existing Passkey. Then select **Register another Passkey** and choose another device or Passkey provider. Both steps require authenticator user verification; the additional credential is discoverable and belongs to the same account. Existing credentials remain usable.

The server binds both steps to the exact authenticated session. Each challenge uses `PASSKEY_CHALLENGE_TTL` (initially five minutes), capped by the session's current expiry. Verification success authorizes one registration challenge for another such interval; it does not renew the login session or authorize other operations. Only one addition attempt is active per browser session, so starting again in another tab replaces the pending attempt.

Invalid, expired, replaced, or reused responses cannot enroll a credential. Completed invalid attempts consume their challenge. Cancellation or failure leaves existing Passkeys intact; the UI restarts with fresh verification. Logging out or signing in again invalidates pending enrollment through its session binding. Registration options exclude existing credentials, and database uniqueness prevents a credential from being overwritten or reassigned.

## Local verification

Stop the backend, apply migration 6 with `make migrate-up`, then run `make backend`. With `make frontend` running, add a Passkey, sign out, and check sign-in using each credential separately. A Passkey synced through the same provider may share the original failure risk; the server cannot establish that the backup uses an independent device or provider.

MySQL integration tests cover fresh verification, account and session binding, invalid signatures/origins/UV, expiry, replay, duplicates, concurrent verification and completion, and sign-in with both credentials. Automated Chrome for Testing 158.0.8127.18 checks use separate virtual CTAP2.1 authenticators; cancellation and duplicate-provider UI errors are simulated. Real-device verification is deferred because the current development environment cannot exercise it; actual device/provider selection remains unverified. Passkey listing, naming, and removal are outside this change.
