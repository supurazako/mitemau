# Account access flows

## Registration

1. The user enters their email address.
2. If the address is already registered, the site informs the user and directs them to sign in.
3. Otherwise, the site sends a verification code to the email address.
4. The user enters the code. Successful verification creates a temporary registration state with an expiration time.
5. The user registers a Passkey with user verification required. The credential must support sign-in without entering an email address.
6. After the server validates the registration response, the site creates the account and an authenticated session.
7. The site displays offline recovery codes and allows the user to copy or save them.

- If Passkey enrollment is canceled or fails, the user can retry within the registration state's lifetime without repeating email verification.
- Each enrollment attempt uses a new WebAuthn challenge.
- If the registration state expires, the user must verify their email address again.
- Incomplete registrations must not reserve an email address permanently.
- Email verification uses six-digit codes valid for 10 minutes, with five attempts per code and a 60-second resend interval. Resending invalidates the previous code.
- Successful email verification authorizes Passkey enrollment for 30 minutes in the same browser. These limits are configurable.

## Sign-in and sign-out

1. The user selects "Sign in with a Passkey" without entering their email address.
2. The user selects an available Passkey and completes authenticator user verification.
3. The server validates the authentication response and creates an authenticated session.
4. Signing out invalidates the current browser session. Sessions on other browsers remain active.

- Failed authentication does not create a session.
- Invalid, expired, or reused authentication challenges are rejected. Challenges last five minutes by default.
- Sessions expire after 24 hours of inactivity or seven days after sign-in, whichever comes first. Interaction and ongoing viewing/broadcasting count as activity; passive session status polling does not. See [session behavior](passkey-sign-in.md).

## Adding a backup Passkey

1. A signed-in user selects "Add a Passkey".
2. The user authenticates again using an existing Passkey with user verification required.
3. After successful reauthentication, the user registers an additional Passkey with user verification required.
4. The server validates the registration response and associates the new credential with the same account.
5. Both the existing and additional Passkeys remain usable for sign-in.

- Failed or canceled reauthentication does not authorize enrollment.
- Failed or canceled enrollment leaves the existing Passkeys unchanged.
- Invalid, expired, or reused challenges are rejected.
- Each step expires after five minutes by default and requires the original session to remain valid. Cancellation and failure restart verification in the UI; see [backup Passkeys](backup-passkeys.md).

## Account recovery

1. The user selects "Recover account" and enters their email address.
2. The user submits an offline recovery code saved previously.
3. After validating the offline recovery code, the site sends a verification code to the account's verified email address.
4. The user enters the email verification code.
5. After both checks succeed, the user registers a new Passkey with user verification required.
6. When recovery completes, the site invalidates all previous Passkeys and sessions, consumes the used offline recovery code, and creates a new authenticated session.
7. The site issues replacement offline recovery codes and displays them for copying or saving.

- Email access alone cannot authorize recovery.
- Ten 128-bit offline codes are shown once after setup and recovery; only hashes are stored. All old codes are invalidated when a replacement set is issued.
- Signed-in users can obtain replacement codes after fresh Passkey verification. See [account recovery](account-recovery.md) for limits and retry behavior.
- Missing or invalid recovery codes are rejected.
- Expired or reused email verification codes are rejected.
- Existing Passkeys and sessions are invalidated only after recovery succeeds.

## Failure cases and unresolved decisions

- Recovery email uses the configured 10-minute/five-attempt/60-second defaults. Verified recovery lasts 30 minutes, with five-minute enrollment challenges capped by the grant.
- Sessions use opaque HttpOnly, SameSite=Strict cookies (Secure in HTTPS deployments). Mutations require JSON and validate the configured origin; session status reads never renew sessions.
- Registration resumes using an opaque HttpOnly cookie linked to server-side verification state; Passkey enrollment must validate and consume that state.
- Failed/canceled Passkey enrollment can retry within verified recovery; offline codes are consumed only when recovery succeeds.
- Email resends replace pending codes but do not revoke already verified registration states in other browsers.
- Account uniqueness is enforced by the database; account locks serialize recovery with login and credential changes. Successful recovery invalidates competing recovery grants.
- Production email delivery remains to be selected; local SMTP uses Mailpit.
