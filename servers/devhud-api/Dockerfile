# syntax=docker/dockerfile:1
FROM golang:1.25.7-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY protos ./protos
COPY servers/devhud-api ./servers/devhud-api
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.1.0-dev
ARG REVISION=unknown
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/devhud-api ./servers/devhud-api/cmd/devhud-api
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/devhud-api-sweeper ./servers/devhud-api/cmd/devhud-api-sweeper

FROM gcr.io/distroless/static-debian12:nonroot AS api
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

FROM gcr.io/distroless/static-debian12:nonroot AS sweeper
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
