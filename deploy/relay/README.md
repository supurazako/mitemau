# MOQtail Relay deployment

The Relay runs on its own Debian VM using Docker Engine and Docker Compose. This deployment is separate from the website VM.

## Runtime configuration

- The custom image builds draft-22 MOQtail Relay 0.18.0 at `bafde0a3f2af403d3ae3509bf86c8e719d9fe402` with `upstream.patch`, `publishing_auth.rs`, and the independent `comment-publisher.rs` binary. The source archive checksum and patched Cargo lockfile are checked during the build.
- Use a draft-22 client (initially `moqtail@0.16.0`); the previous draft-18 client path is incompatible.
- WebTransport and QUIC use UDP port 4433.
- The container runs as UID/GID 65532 with a read-only root filesystem, no Linux capabilities, and `no-new-privileges`.
- Certificates, logs, and temporary files are mounted from `/var/lib/moqtail-relay` on the host.
- `moqtail-relay.service` runs Compose in the foreground under systemd. Certificate renewal syncs the renewed files and restarts that service.

## Install or update

Apply API migration `000012` before starting the new API. Set `RELAY_AUTH_KEY` to the same 32-byte hex secret in the API environment and in `/opt/mitemau/deploy/relay/.env` on the Relay VM. Generate it with `openssl rand -hex 32`. Set `RELAY_AUTH_URL` in the Relay environment to the API's HTTPS `/relay/authorize` endpoint. Loopback HTTP is supported for local verification.

Restrict the environment file to its operator. The key authenticates Relay requests to the API and derives opaque publisher credentials. Keep it out of the frontend and logs. Authorization also verifies the credential against the current key, so rotating the key invalidates earlier credentials even while their database grants remain active. A key rotation requires restarting both services and reconnecting publishers with new sending sessions.

From a checkout at `/opt/mitemau`, build the custom image and install the service and certificate-sync script.

```sh
sudo install -m 0755 deploy/relay/sync-certs.sh /usr/local/sbin/moqtail-relay-sync-certs
sudo install -m 0644 deploy/relay/moqtail-relay.service /etc/systemd/system/moqtail-relay.service
sudo install -d -o 65532 -g 65532 -m 0750 /var/lib/moqtail-relay/logs
sudo install -d -o 65532 -g 65532 -m 0700 /var/lib/moqtail-relay/tmp
sudo /usr/local/sbin/moqtail-relay-sync-certs
sudo docker compose -f deploy/relay/compose.yaml build relay
sudo systemctl daemon-reload
sudo systemctl enable moqtail-relay.service
sudo systemctl restart moqtail-relay.service
```

`compose.yaml` uses `pull_policy: never`. Build the custom image before restarting the service. Keep the existing Certbot deploy hook pointed at `/usr/local/sbin/moqtail-relay-sync-certs` followed by `systemctl try-restart moqtail-relay.service`.

## Verify

```sh
sudo systemctl status moqtail-relay.service
sudo docker compose -f /opt/mitemau/deploy/relay/compose.yaml ps
sudo docker compose -f /opt/mitemau/deploy/relay/compose.yaml logs --tail=100 relay
```

Confirm an external WebTransport client can connect to `relay-test.supurazako.com:4433` and negotiate `moqt-22` and complete MoQ SETUP before considering a runtime migration complete.


## Publishing authorization

The browser prepares a sending session through the authenticated API before connecting to the Relay. The API stores a hash of its credential and returns the credential with the exact namespace. Retrying prepare returns the same credential. Stop, leave, and whole-stream end revoke the corresponding grants in the same transaction as their state changes. A revoked session ID stays revoked after rejoining. An existing browser must reconnect to prepare a new sending session.

The browser carries the credential in a SETUP AuthorizationToken with application token type 0. The Relay checks it through `POST /relay/authorize` using its service key as a Bearer credential. Anonymous SETUP allows subscriptions. Publishing requires a lease for the exact tuple namespace. Both PUBLISH_NAMESPACE and direct PUBLISH are checked, as are upstream SUBSCRIBE_OK and incoming stream/datagram Objects.

The Relay refreshes permission every 5 seconds. A successful check grants 15 seconds from the start of that request, measured with a monotonic clock. Permission denial closes the sending connection immediately. Transport errors, server errors, malformed responses, and request timeouts retain only the existing deadline. The deadline remains active during each HTTP request. Expiry closes the connection and the data path checks the deadline for each incoming Object. API outages therefore stop active publishers when their last lease expires.

A successful leave/end API response confirms the committed database change. Relay enforcement follows at its next permission check or lease expiry. Objects already received or buffered by viewers can still play. Verify revocation with newly generated Object sequence numbers and record both sender termination and the last received Object.

The Relay removes the connection's announcements and publishers through its existing disconnect cleanup. Cached Objects retain the upstream cache policy. Public page discovery and stale API namespace cleanup after unexpected disconnect remain tracked by #36 and #37.

Raw control-message payload logs, SETUP dumps, and token registration logging are removed by the patch. The retained client SETUP also has its AuthorizationToken removed after authentication.

## Maintaining the Relay patch

Keep `upstream.patch`, `publishing_auth.rs`, the Dockerfile archive checksum, image tag, and build identity together in this repository. The Docker build checks patch applicability, applies the patch including Cargo.lock, runs Relay tests, and builds with `--locked`. Update the pinned upstream commit deliberately and repeat direct unauthorized publishing, public subscription, active revocation, and API outage tests before deploying an updated image. The original upstream image permits publishing without these checks and cannot provide this authorization contract.

## Connection loss and reconnect

The API uses the Relay's existing five-second authorization checks as connection liveness. Each successful check records the sending session's timestamp and updates the participant only when that exact session is currently advertised. A session expires fifteen seconds after its last successful check and cannot renew after expiration. Public and private participant reads immediately omit expired namespaces and report `publishingState: disconnected`. A five-second cleanup task revokes expired grants and clears their stored namespaces. Browser background timer throttling does not affect these checks.

A normal camera stop or Disconnect reports `stopped`. Unexpected Relay closure reports `disconnected`; lost stop notifications are covered by lease expiry. Leaving removes participation. Only the creator's explicit whole-stream end ends the stream and revokes everyone's grants. Losing the creator's connection leaves the stream live and other participants keep sending. If everyone disconnects, the live list still contains the stream until the creator ends it.

The initial reconnect flow is manual. The sender chooses Connect and then Start camera, which creates a fresh session and namespace. Advertising the new session revokes that participant's older grants, including prepared sessions. An older Relay connection closes on its next authorization check, with the existing lease bounding a failed check; viewers switch to the new namespace immediately on their next API refresh. Delayed starts, stops and authorization checks for older sessions cannot replace or remove the new namespace. Viewers choose Watch again after a Relay connection loss; when a sender's namespace changes, the old player is disposed and a fresh Watch button is shown. Removed participants, expired namespaces, navigation and whole-stream end dispose the associated subscriptions, decoders and audio contexts.

The fifteen-second interval starts at the last authorization check, not at the physical instant a network fails. A silent network loss first needs the Relay's QUIC connection-loss detection (the pinned Relay defaults to a seven-second idle timeout). Viewer updates also depend on the two-second API polling interval and network availability. An alive connection with stalled media encoding is not detected by this mechanism. API outages expire sending leases rather than keeping stale publishers visible indefinitely.

Migration `000013` clears previously advertised namespaces and revokes pre-migration grants because they have no measured liveness. Existing senders must reconnect after this migration. Apply it before starting the updated API.


## Live comment publisher

The `comment-publisher` compose service sends Go-accepted comment snapshots through the same pinned draft-22 Relay. It is separate from browser media publishers and uses a server-owned namespace and service-authenticated API endpoints. See [live comment architecture and validation](../../docs/comments.md) for the latest-50 bootstrap, reconnect, per-account posting limits and end-of-stream authorization behavior. Build the updated image and apply migration `000015` before starting these services.

When using a host-loopback API tunnel, configure both services with `network_mode: host` and remove the relay's `ports` mapping. On the same machine as the Relay, `COMMENT_RELAY_ADDRESS=127.0.0.1:4433` keeps native QUIC local while the URI hostname is still used for TLS verification. The experimental akamai instance uses this arrangement.
