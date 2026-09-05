# syntax=docker/dockerfile:1@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32
FROM --platform=$BUILDPLATFORM node:24-bookworm-slim@sha256:ba849c60be29959425b8734d57b8b4b7d56f98edd9504c9af091d5281095a71e AS administrator-assets
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

FROM --platform=$BUILDPLATFORM golang:1.25.7-bookworm@sha256:564e366a28ad1d70f460a2b97d1d299a562f08707eb0ecb24b659e5bd6c108e1 AS build
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
