# Database migrations

Use golang-migrate via Docker Compose.
Store paired `.up.sql` and `.down.sql` files in `backend/migrations`.

Start MySQL with `docker compose up -d mysql`, then run `make migrate-up`.

Do not modify successfully applied, shared migrations.
If a migration fails, inspect and repair the database before using `force`;
it updates migration metadata without executing SQL.

The first migration creates email verification state. Apply and reapply it to verify migration tracking.
