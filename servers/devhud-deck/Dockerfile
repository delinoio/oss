# syntax=docker/dockerfile:1.7

ARG GO_BUILD_IMAGE=golang:1.25.7-alpine@sha256:f6751d823c26342f9506c03797d2527668d095b0a15f1862cddb4d927a7a4ced
ARG VERSION=fixture
ARG REVISION=unknown
ARG SOURCE=https://github.com/delinoio/oss

FROM --platform=$BUILDPLATFORM ${GO_BUILD_IMAGE} AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY protos/delibase/gen/go ./protos/delibase/gen/go
COPY protos/devhud-deck/gen/go ./protos/devhud-deck/gen/go
COPY servers/internal ./servers/internal
COPY servers/devhud-deck ./servers/devhud-deck
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -trimpath \
    -buildvcs=false \
    -ldflags="-s -w -buildid=" \
    -o /out/devhud-deck \
    ./servers/devhud-deck/cmd/devhud-deck

FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35

ARG VERSION
ARG REVISION
ARG SOURCE
LABEL org.opencontainers.image.title="devhud-deck" \
      org.opencontainers.image.description="Artifact-only inactive DevHud Deck server fixture" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.source="${SOURCE}" \
      org.opencontainers.image.licenses="Apache-2.0"

COPY --from=build --chown=65532:65532 --chmod=0555 /out/devhud-deck /devhud-deck

USER 65532:65532
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/devhud-deck"]
