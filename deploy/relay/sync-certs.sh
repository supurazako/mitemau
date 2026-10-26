#!/bin/sh
set -eu

lineage=/etc/letsencrypt/live/relay-test.supurazako.com
dest=/var/lib/moqtail-relay/certs

install -d -o 65532 -g 65532 -m 0700 "$dest"
tmp_cert=$(mktemp "$dest/.cert.pem.XXXXXX")
tmp_key=$(mktemp "$dest/.key.pem.XXXXXX")
cleanup() {
    rm -f "$tmp_cert" "$tmp_key"
}
trap cleanup EXIT HUP INT TERM

install -o 65532 -g 65532 -m 0400 "$lineage/fullchain.pem" "$tmp_cert"
install -o 65532 -g 65532 -m 0400 "$lineage/privkey.pem" "$tmp_key"
mv -f "$tmp_cert" "$dest/cert.pem"
mv -f "$tmp_key" "$dest/key.pem"
trap - EXIT HUP INT TERM
