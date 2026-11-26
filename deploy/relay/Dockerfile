# syntax=docker/dockerfile:1.7
FROM rust:1.98-bookworm AS builder
WORKDIR /workspace
ADD --checksum=sha256:a686eb416d5b0f35ed192ad6db2bf1f67bf283db270f3d817622eca1f3a525b0 https://codeload.github.com/moqtail/moqtail/tar.gz/bafde0a3f2af403d3ae3509bf86c8e719d9fe402 /tmp/upstream.tar.gz
RUN tar -xzf /tmp/upstream.tar.gz --strip-components=1 -C /workspace
COPY upstream.patch /tmp/upstream.patch
COPY publishing_auth.rs /tmp/publishing_auth.rs
COPY comment-publisher.rs /tmp/comment-publisher.rs
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/workspace/target \
    git apply --check /tmp/upstream.patch \
    && git apply /tmp/upstream.patch \
    && cp /tmp/publishing_auth.rs apps/relay/src/server/publishing_auth.rs \
    && mkdir -p apps/relay/src/bin \
    && cp /tmp/comment-publisher.rs apps/relay/src/bin/comment-publisher.rs \
    && cargo test --locked -p relay \
    && cargo build --locked -p relay --release --bins \
    && mkdir -p /rootfs/certs /rootfs/tmp \
    && cp target/release/relay /rootfs/relay \
    && cp target/release/comment-publisher /rootfs/comment-publisher

FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=builder /rootfs/relay /usr/local/bin/relay
COPY --from=builder /rootfs/comment-publisher /usr/local/bin/comment-publisher
COPY --from=builder --chown=nonroot:nonroot /rootfs/certs /certs
COPY --from=builder --chown=nonroot:nonroot /rootfs/tmp /tmp
EXPOSE 4433/udp
ENTRYPOINT ["/usr/local/bin/relay"]
