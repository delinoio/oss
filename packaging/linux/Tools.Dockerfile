FROM node:24-bookworm@sha256:64af3819f9275802414d7cdc38c27e9d82bd564dec4d4da87d008255d36c63b4
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl unzip gnupg rpm binutils cmake make gcc pkg-config \
    libbz2-dev libcurl4-openssl-dev libxml2-dev libssl-dev zlib1g-dev \
    libglib2.0-dev liblzma-dev libsqlite3-dev librpm-dev libzstd-dev \
    && rm -rf /var/lib/apt/lists/*
COPY packaging/linux/pins.json /opt/delino/pins.json
COPY scripts/release/linux-packages/install-tools.mjs /opt/delino/install-tools.mjs
RUN node /opt/delino/install-tools.mjs /opt/delino/pins.json && ldconfig
WORKDIR /workspace
