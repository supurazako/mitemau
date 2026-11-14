# MOQtail Relay deployment

The Relay runs on its own Debian VM using Docker Engine and Docker Compose. This deployment is separate from the website VM.

## Runtime configuration

- MOQtail Relay 0.18.0 implements MoQ draft-22; its image is pinned by digest in `compose.yaml`.
- Use a draft-22 client (initially `moqtail@0.16.0`); the previous draft-18 client path is incompatible.
- WebTransport and QUIC use UDP port 4433.
- The container runs as UID/GID 65532 with a read-only root filesystem, no Linux capabilities, and `no-new-privileges`.
- Certificates, logs, and temporary files are mounted from `/var/lib/moqtail-relay` on the host.
- `moqtail-relay.service` runs Compose in the foreground under systemd. Certificate renewal syncs the renewed files and restarts that service.

## Install or update

From a checkout at `/opt/mitemau`, install the service and certificate-sync script:

```sh
sudo install -m 0755 deploy/relay/sync-certs.sh /usr/local/sbin/moqtail-relay-sync-certs
sudo install -m 0644 deploy/relay/moqtail-relay.service /etc/systemd/system/moqtail-relay.service
sudo install -d -o 65532 -g 65532 -m 0750 /var/lib/moqtail-relay/logs
sudo install -d -o 65532 -g 65532 -m 0700 /var/lib/moqtail-relay/tmp
sudo /usr/local/sbin/moqtail-relay-sync-certs
sudo docker pull ghcr.io/moqtail/relay@sha256:c68aa7f28ad51014b8f081386cc0d5bb0816f8ca8be2c98656d9a80d8ee93d21
sudo systemctl daemon-reload
sudo systemctl enable moqtail-relay.service
sudo systemctl restart moqtail-relay.service
```

`compose.yaml` uses `pull_policy: never`; pull the pinned digest before starting a new version. Keep the existing Certbot deploy hook pointed at `/usr/local/sbin/moqtail-relay-sync-certs` followed by `systemctl try-restart moqtail-relay.service`.

## Verify

```sh
sudo systemctl status moqtail-relay.service
sudo docker compose -f /opt/mitemau/deploy/relay/compose.yaml ps
sudo docker compose -f /opt/mitemau/deploy/relay/compose.yaml logs --tail=100 relay
```

Confirm an external WebTransport client can connect to `relay-test.supurazako.com:4433` and negotiate `moqt-22` and complete MoQ SETUP before considering a runtime migration complete.
