# Moderation records and retention

Only current staff accounts may use investigation APIs and the investigation view. Broadcaster approval alone grants no access. Filters identify an account acting in or being the subject of an event, occurrence time range and optionally a stream; responses contain bounded pages and never expose email addresses, authentication material or media.

## Agreed retention

- A comment becomes publicly unavailable immediately on deletion or whole-stream end. Its investigation-only body expires 30 days after the first such event. Repeated deletion or stream end does not extend this clock.
- Ordinary activity events expire 90 days after occurrence.
- Evidence snapshots expire 180 days after the report's original acceptance. Closing, retrying or leaving the report unresolved does not extend that deadline. Staff may add still-retained evidence of behavior occurring after report acceptance, but that evidence uses the same original deadline.
- Report and staff-action history expires 180 days after each event's occurrence.
- Expiry is enforced during reads, even before periodic physical deletion. Cleanup deletes bounded batches every minute. This worker covers application storage and does not manage external backups.

Comment bodies, events and report evidence are stored separately. A report copies only still-retained source data into an independent, bounded snapshot. Evidence cannot resurrect an expired comment. A still-retained live comment can be snapshotted directly even if its ordinary posting event has already expired. Content remains available for staff while a comment is public; its 30-day body deadline starts on hiding. The ordinary posting event still has its independent 90-day lifetime.

## Recorded scope and integration

Record successful comment posting/deletion, stream start/end and participant join/leave, user reports and staff moderation actions. Keep explicit actor, subject account, occurrence time and applicable stream/comment/report identifiers. Stream changes and their records commit or roll back together. Idempotent retries create no extra event. Whole-stream end records the creator's action and the resulting departures; Relay loss alone creates neither a departure nor stream end. Do not backfill unverifiable history.

Issue #51 records existing stream operations and broadcaster approval. Internal helpers support future comment posting (#52), comment hiding/deletion (#53), report creation/evidence capture (#54), and staff restrictions (#55/#56). Those future flows must call the helpers inside their own successful-operation transactions, using the original report acceptance time under a report-row lock. Report records and staff actions use the 180-day event category. Do not accept arbitrary metadata or store credentials in events.

The public comment API and actual comment/report/moderation product flows are implemented by those subsequent issues. This foundation does not expose public comment history or new public write endpoints. Viewing history, connection heartbeats, camera/audio recordings and general request-body logging are outside scope.

## Investigation

`GET /staff/activity` and `GET /staff/evidence` require a current staff session and `accountId`, `from`, `to` (RFC3339, inclusive start/exclusive end), plus optional `streamId` and `before` cursor. Each returns at most 50 records ordered by descending event ID, with `nextCursor` for older records. Evidence filters use the original event's occurrence time. A comment body omitted by retention is returned as null. Evidence includes its report ID and expiry so staff can distinguish it from the original event.

## User reports

Signed-in accounts may report another account once per minute. A reason and incident time are required; a retained comment and its stream can be attached as context, including hidden comments within their retention deadline. The server validates the comment belongs to the reported account and snapshots its body when accepting the report. Staff can review reports, inspect retained evidence and account activity, remove a comment by the reported user, and record an outcome. Staff deletion increments the live comment version and records the action in the same transaction.

Reports and their evidence expire 180 days after acceptance, including unresolved reports. A recorded outcome and its action event expire 180 days after the action, even when the original report has already expired. The staff activity search exposes the retained outcome. Comment content independently follows the 30-day hidden-content retention policy. The staff queue and report evidence APIs enforce staff authorization and do not return expired reports.
