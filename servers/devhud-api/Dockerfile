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
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/devhud-api ./servers/devhud-api/cmd/devhud-api
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags='-s -w' -o /out/devhud-api-sweeper ./servers/devhud-api/cmd/devhud-api-sweeper

FROM gcr.io/distroless/static-debian12:nonroot AS api
COPY --from=build /out/devhud-api /usr/local/bin/devhud-api
EXPOSE 46307
ENTRYPOINT ["/usr/local/bin/devhud-api", "serve"]

FROM gcr.io/distroless/static-debian12:nonroot AS sweeper
COPY --from=build /out/devhud-api-sweeper /usr/local/bin/devhud-api-sweeper
ENTRYPOINT ["/usr/local/bin/devhud-api-sweeper"]
