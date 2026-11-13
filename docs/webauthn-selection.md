# WebAuthn library selection

Decision for #20, recorded on 2026-11-14: use `github.com/go-webauthn/webauthn` on the Go backend and native WebAuthn APIs in the browser.

## Backend

| Candidate | Assessment |
| --- | --- |
| [go-webauthn/webauthn](https://github.com/go-webauthn/webauthn) | Selected. Provides registration and authentication verification, discoverable login, user-verification requirements, and per-user credential lists. BSD-3-Clause. |
| [pomerium/webauthn](https://github.com/pomerium/webauthn) | Active alternative under Apache-2.0. The selected library's documented registration/login flow and explicit discoverable-login API fit our requirements directly, so no advantage requiring a switch was identified. |
| [duo-labs/webauthn](https://github.com/duo-labs/webauthn) | Archived since 2022; excluded from new adoption. |

The selected repository has recent development and documentation with integration examples. Release `v0.18.2` was reviewed; its Go 1.26 minimum is compatible with this project's Go 1.27.3. The dependency is pinned in #16; review release notes when upgrading the v0 library.

Require discoverable credentials for email-free sign-in and user verification for registration and authentication. Store multiple credentials per account to support backup Passkeys.

## Browser

Use `navigator.credentials.create()` / `get()` with the native JSON conversion APIs. Compared with [@simplewebauthn/browser](https://simplewebauthn.dev/docs/packages/browser/), this avoids an additional dependency for the initial single Chrome/Chromium verification target.

In #16, record the browser/version and verify support for `PublicKeyCredential.parseCreationOptionsFromJSON()`, `parseRequestOptionsFromJSON()`, and credential `toJSON()`. Check interoperability with the selected Go release. Reconsider the helper library if the target browser lacks these APIs. The #16 automated Chromium/virtual-authenticator check is recorded in [passkey-registration.md](passkey-registration.md); real-device verification remains pending.

## Application responsibilities

The library does not replace account authorization, database storage, or login sessions. The application must persist ceremony state, enforce expiry and single use, bind it to the initiating browser/account, and validate and consume the verified email state from #15. Configure RP ID and allowed origins explicitly; do not derive them from untrusted request headers. Use HTTPS in deployment (localhost for local verification).

Authentication implementation and browser/device testing belong to #16–#18. This decision is based on documentation and repository review, not an integration test or a security audit.
