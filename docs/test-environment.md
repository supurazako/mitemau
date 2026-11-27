# Local browser test environment

Run `make test-environment` from the repository root. It requires local Docker
Compose, Go, Node.js and Python 3. Run `npm ci` in `frontend` first if its
dependencies are missing.

Open [Test accounts](http://mitemau-test.localhost:5176/) and choose a test state.
The picker opens the [test app](http://mitemau-test.localhost:5175/) as a viewer,
pending or approved broadcaster, posting-suspended user, broadcasting-suspended
user, staff member, or signed-out visitor. There's a sample live stream and a
report in the staff queue. Use separate browser profiles or Playwright browser
contexts to keep two accounts open at once.

The picker is a standalone loopback tool. It creates one-hour sessions for
synthetic accounts in the test database. The app uses its usual session and
authorization checks. The test hostname keeps its cookies separate from the
regular app at localhost. The stack uses a dedicated Compose project and
temporary MySQL storage. Stop it with Ctrl+C and start it again for a fresh set
of fixtures. The regular development data, services and `.env` stay separate.

The [test inbox](http://localhost:8125/) receives verification and recovery
messages from the test API. To exercise Passkey registration or sign-in, use
the app's usual forms and a test Passkey or a Chromium virtual authenticator.
Picker sessions skip the Passkey ceremony. For routine UI checks the launcher
disables Relay connections. Use a separate integration run with a test Relay
and matching authorization configuration for actual media and comment delivery.

Services listen on loopback. For a remote workspace, forward ports 5175 and 5176
to the browser's machine and use the exact test hostname above. Forward 8125
as well if you need to read test email.
