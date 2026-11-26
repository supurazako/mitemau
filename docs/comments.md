# Live comments (draft-22)

## Agreed product behavior

Signed-in accounts may post up to 500 Unicode code points after trimming surrounding whitespace. One account may submit one new comment per two seconds across all streams and login sessions. Invalid requests and idempotent retries do not consume another slot. Reading is public while the stream is live, with the latest 50 comments in server sequence order. Stream end rejects posting and makes the history unavailable through both public APIs and new Relay read requests.

The client sends a random `requestId` for retry safety. The server assigns a separate random comment ID; a reused client request ID cannot cause an old moderation event to point to a new comment after public metadata cleanup. Posting permission is checked on every request through `account_comment_permissions`. The staff controls and restriction policy are implemented by #55, not by this issue.

## Delivery path

The Go API authenticates, checks permission and the account-wide cooldown, locks stream state, and commits the comment plus the #51 moderation record in one transaction. The Rust `comment-publisher` process is a separate MOQtail Publisher. It announces a server-owned namespace and responds to the Relay's subscriptions over native QUIC with certificate verification. Browsers subscribe over WebTransport; browsers cannot directly publish into the server-owned namespace.

Namespace: `mitemau/streams/<stream-id>/comments/<sending-session-id>`, Track: `comments`. The server-side process uses its service credential to prepare a fresh session, connects and announces the namespace, then activates it. The namespace is advertised only after successful Relay authorization and announcement. A new session replaces the old credential; stale activations do not replace a newer session. Sending grants expire after 15 seconds without the existing Relay authorization refresh.

Each Object carries a UTF-8 JSON snapshot with `streamId`, string `version`, public `namespace`, and up to 50 comments (`id`, string `sequence`, `accountId`, `body`, `postedAt`). An update uses Group ID = persisted snapshot version, Object ID = 0, and a reliable subgroup stream. Every snapshot is complete and independently useful. The publisher checks for changes each second and sends only a changed version. This deliberately trades repeated bytes for a simple bounded consistency model; it is not a throughput optimization.

The browser subscribes before fetching a second latest-50 snapshot, closing the initial snapshot/subscription gap. Lower versions cannot overwrite higher ones. Repeated snapshots replace the window without duplicating rows. Channel-only polling detects stream end and publisher replacement. If update metadata remains ahead of received Objects, the viewer reconnects and repairs its latest window instead of waiting forever. Ordinary live delivery is MoQT; snapshot/bootstrap and reconnection repair use the API.

## Authorization and end

Publisher preparation/activation and internal feed access require the server service key. The browser never receives this key or the comment publisher credential. The Relay checks comment read authorization at SUBSCRIBE, FETCH, REQUEST_UPDATE and pushed-track subscription entry points. Comment payloads are excluded from the Relay cache, and comment FETCH requests are rejected: the bounded latest-50 API supplies scrollback. This prevents replaying older snapshots to exceed the agreed live scrollback limit. This check is fail-closed during API failures and only permits the active, live namespace. Media namespace behavior is unchanged. Existing subscribers may already hold received bytes; neither stream end nor deletion can erase a client's prior copy.

The publisher closes when the stream disappears from the internal live list or its grant is rejected. The client clears the displayed history when the stream ends, and closes its reader/Relay connection on navigation or replacement. Moderation content starts its existing 30-day non-public retention on stream end. Public comment metadata is reclaimed after the same ended-stream period. Report evidence remains governed independently by #51. Broadcaster deletion increments `live_streams.comment_version` in the same transaction as hiding the comment, so subscribers receive the revised window.

## Broadcaster deletion

Any currently participating, approved broadcaster can delete a comment in the shared live stream, including comments posted by other participants or viewers. Invited users who have not joined, former participants, unrelated broadcasters and staff without participation have no broadcaster deletion permission. Stopping a camera or disconnecting media does not end participation. Leaving or whole-stream end removes this permission; staff investigation actions are handled separately by #54.

`GET /streams/{id}/comments/permissions` returns the signed-in account's current `canDelete` value. `DELETE /streams/{id}/comments/{comment}` rechecks permission on the server and serializes with participation changes and stream end. Deletion hides content, records the actor and comment author, and advances the snapshot version atomically. An authorized retry returns 204 without renewing retention or adding another event/version; an audit failure rolls back the whole operation. The first deletion starts 30-day non-public body retention. Existing report evidence keeps its independent expiry.

All participant and public pages receive the revised latest-50 visible window over MoQT. Removing an item can bring an older, still-visible comment back into that window. The UI does not optimistically hide comments: failure leaves the displayed window intact, and the existing reconnect repair restores the authoritative window after transport failure. Already delivered content cannot be retracted from a viewer's own copies.

## Running

Apply migration `000015` before starting the updated API. Build the image using `deploy/relay/Dockerfile`; it contains both `relay` and `comment-publisher` binaries from the pinned MOQtail revision. The compose file starts both services. The publisher uses `RELAY_AUTH_URL` and `RELAY_AUTH_KEY` for the Go service API, and `COMMENT_RELAY_URL` (default `moqt://relay-test.supurazako.com:4433`) for the Relay. `COMMENT_RELAY_ADDRESS` can override the socket address while retaining the URI host for TLS verification, for isolated verification.

When the API is reached through a host-loopback SSH tunnel, both containers must use the host network (as on the experimental akamai instance). `127.0.0.1` inside a bridge-networked container is not the host's tunnel endpoint. Avoid running competing publishers for one stream: this initial deployment has one managed publisher process, and a newly prepared session deliberately retires the previous one.
