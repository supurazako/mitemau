# Email verification

Use SMTP with Mailpit for local development: Docker-based mail capture, a web inbox, and an API for testing without external delivery.

Copy the new settings from `.env.example` into `.env`. Generate `EMAIL_CODE_SECRET` with `openssl rand -hex 32` and keep it stable across API restarts.

    docker compose up -d mysql mailpit
    make migrate-up
    make backend

Run `make frontend` in another terminal. Open http://localhost:5173 and read mail at http://localhost:8025.

Codes expire after 10 minutes, allow five attempts, and can be resent after 60 seconds. Resending invalidates the previous pending code and browser token. Already verified registrations are stored separately and remain valid for 30 minutes, including when another browser requests a code. Timings and attempt limits are configurable.

Send requests also have a configurable source-IP limit of 20 accepted messages per hour, across recipients. Source IPs are stored as keyed hashes. `TRUST_LOOPBACK_PROXY=true` uses the rightmost forwarded IP only when the immediate peer is loopback; the single trusted proxy must append the actual client IP. Vite is configured to do so. Review this setting when deploying behind Caddy.

Expired state is deleted at API startup and hourly, in batches of up to 1,000 rows per table. Expiry is enforced on reads even before deletion. The cleanup interval is configurable.

The HttpOnly cookie identifies registration state, not a login session. #16 validates and consumes the `verified_registrations` state and handles existing accounts and concurrent registration. The initial email policy supports ASCII addresses, preserving local-part case and lowercasing the domain.

For public deployment, configure `APP_ORIGIN`, HTTPS cookies, and authenticated STARTTLS delivery. Public provider selection and delivery validation remain pending for M5. Distributed abuse and provider quotas still need review before public launch.

Tests: `go test ./...` in `backend`. Set `MYSQL_TEST_DSN` explicitly to a disposable MySQL database with migrations applied to run DB integration tests. `API_ADDR` and Vite's `API_PROXY_TARGET` can override local ports when needed.
