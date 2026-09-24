# Build and prepare pnport's Linux execution fixtures with the repository's
# pinned Rust toolchain and Node 24. Runtime probes run with --network none.
FROM rust:1.95-bookworm@sha256:6258907abe69656e41cd992e0b705cdcfabcbbe3db374f92ed2d47121282d4a1 AS rust-tools
FROM node:24-bookworm@sha256:64af3819f9275802414d7cdc38c27e9d82bd564dec4d4da87d008255d36c63b4 AS node-tools
FROM ubuntu:22.04@sha256:b8b6ee6aa931ecd9d0d952abc34dc0e5f7c6a30c6bb71b079fe399fde0329c02

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential ca-certificates git golang-go pkg-config python3 && \
    rm -rf /var/lib/apt/lists/*
COPY --from=rust-tools /usr/local/cargo/bin/ /root/.cargo/bin/
COPY --from=node-tools /usr/local/bin/node /usr/local/bin/node
COPY --from=node-tools /usr/local/lib/node_modules/ /usr/local/lib/node_modules/
ENV PATH="/root/.cargo/bin:${PATH}"
RUN ln -s /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm && \
    rustup toolchain install nightly-2026-01-01 --profile minimal && \
    rustup default nightly-2026-01-01
WORKDIR /workspace
