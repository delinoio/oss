# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.25.7
ARG VERSION=dev
ARG REVISION=unknown
ARG SOURCE=https://github.com/delinoio/oss

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY protos/delibase/gen/go ./protos/delibase/gen/go
COPY servers/internal ./servers/internal
COPY servers/delibase ./servers/delibase
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -trimpath \
    -buildvcs=false \
    -ldflags="-s -w -buildid=" \
    -o /out/delibase \
    ./servers/delibase/cmd/delibase

FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION
ARG REVISION
ARG SOURCE
LABEL org.opencontainers.image.title="delibase" \
      org.opencontainers.image.description="Artifact-only delibase organization and usage-billing runtime" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.source="${SOURCE}" \
      org.opencontainers.image.licenses="MIT"

COPY --from=build --chown=65532:65532 --chmod=0555 /out/delibase /delibase
COPY --chown=65532:65532 --chmod=0444 servers/delibase/catalog.json /etc/delibase/catalog.json

USER 65532:65532
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/delibase"]
