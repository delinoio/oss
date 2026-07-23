FROM golang:1.25.7-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY protos/delibase/gen/go ./protos/delibase/gen/go
COPY servers ./servers
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/delibase \
    ./servers/delibase/cmd/delibase

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/delibase /delibase
COPY --chown=65532:65532 servers/delibase/catalog.json /etc/delibase/catalog.json

USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/delibase"]
