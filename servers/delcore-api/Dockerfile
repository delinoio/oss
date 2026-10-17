# SPDX-License-Identifier: Apache-2.0
FROM golang:1.26.8-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY protos/gen/go/delcore ./protos/gen/go/delcore
COPY servers/delcore-api ./servers/delcore-api
RUN CGO_ENABLED=0 go build -trimpath -o /out/delcore-api ./servers/delcore-api/cmd/delcore-api
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/delcore-api /delcore-api
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/delcore-api"]
