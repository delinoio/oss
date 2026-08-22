# syntax=docker/dockerfile:1@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32
FROM golang:1.25.7-bookworm@sha256:564e366a28ad1d70f460a2b97d1d299a562f08707eb0ecb24b659e5bd6c108e1 AS build
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

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS api
COPY --from=build /out/devhud-api /usr/local/bin/devhud-api
EXPOSE 46307
ENTRYPOINT ["/usr/local/bin/devhud-api"]
CMD ["serve"]

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS sweeper
COPY --from=build /out/devhud-api-sweeper /usr/local/bin/devhud-api-sweeper
ENTRYPOINT ["/usr/local/bin/devhud-api-sweeper"]
