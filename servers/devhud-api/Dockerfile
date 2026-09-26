# syntax=docker/dockerfile:1@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32
FROM --platform=$BUILDPLATFORM node:24-bookworm-slim@sha256:0e0ff40c39bc087845bfb27465a0df4ea419520094bc35842ff83dd8cbe6f9b6 AS administrator-assets
WORKDIR /src
RUN corepack enable && corepack prepare pnpm@10.26.2 --activate
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/devhud-admin/package.json ./apps/devhud-admin/package.json
COPY packages/devhud-api-client/package.json ./packages/devhud-api-client/package.json
RUN pnpm install --frozen-lockfile --ignore-scripts --filter devhud-admin...
COPY apps/devhud-admin ./apps/devhud-admin
COPY packages/devhud-api-client ./packages/devhud-api-client
RUN mkdir -p servers/devhud-api/internal/adminassets && \
    pnpm --filter devhud-admin build:embedded

FROM --platform=$BUILDPLATFORM golang:1.26.8-bookworm@sha256:a688600ca24f8a4d3ca77f95b0dd40704a9fc787c826660eb7ba0b641b8b175d AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY protos ./protos
COPY servers/devhud-api ./servers/devhud-api
COPY --from=administrator-assets /src/servers/devhud-api/internal/adminassets/dist ./servers/devhud-api/internal/adminassets/dist
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.1.0-dev
ARG REVISION=unknown
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/devhud-api ./servers/devhud-api/cmd/devhud-api
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/devhud-api-sweeper ./servers/devhud-api/cmd/devhud-api-sweeper

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS api
ARG VERSION=0.1.0-dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="devhud-api" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.source="https://github.com/delinoio/oss" \
      io.delino.devhud.migrations="embedded" \
      io.delino.devhud.administrator-assets="embedded"
COPY --from=build /out/devhud-api /usr/local/bin/devhud-api
EXPOSE 46307
ENTRYPOINT ["/usr/local/bin/devhud-api"]
CMD ["serve"]

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS sweeper
ARG VERSION=0.1.0-dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="devhud-api-sweeper" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.source="https://github.com/delinoio/oss" \
      io.delino.devhud.migrations="embedded" \
      io.delino.devhud.administrator-assets="embedded"
COPY --from=build /out/devhud-api-sweeper /usr/local/bin/devhud-api-sweeper
ENTRYPOINT ["/usr/local/bin/devhud-api-sweeper"]
