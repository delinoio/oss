# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:24-bookworm-slim AS console
WORKDIR /src
RUN npm install --global pnpm@10.26.2
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/aiprouter/package.json ./apps/aiprouter/package.json
COPY packages/aiprouter-client/package.json ./packages/aiprouter-client/package.json
# Dependencies are installed in Linux; Dockerfile.dockerignore excludes host stores.
RUN pnpm install --filter @aiprouter/console... --frozen-lockfile --ignore-scripts
COPY apps/aiprouter ./apps/aiprouter
COPY packages/aiprouter-client ./packages/aiprouter-client
COPY scripts/localization ./scripts/localization
COPY scripts/aiprouter/console-notices.mjs ./scripts/aiprouter/console-notices.mjs
RUN pnpm --dir apps/aiprouter build && test -s apps/aiprouter/dist/index.html
RUN node scripts/aiprouter/console-notices.mjs /src/node_modules/.pnpm /console-notices

FROM --platform=$BUILDPLATFORM golang:1.26.10-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download all
RUN apt-get update && apt-get install -y --no-install-recommends python3 && rm -rf /var/lib/apt/lists/*
COPY scripts/aiprouter/licenses.py /src/licenses.py
RUN python3 -c 'from pathlib import Path; from licenses import bundle; bundle(Path("/src"), Path("/out-notices"))'
COPY servers/aiprouter ./servers/aiprouter
COPY packages/aiprouter ./packages/aiprouter
COPY protos ./protos
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/aiprouter-server ./servers/aiprouter
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/aiprouter-server /usr/local/bin/aiprouter-server
COPY LICENSE NOTICE /usr/share/doc/aiprouter/
COPY --from=build /out-notices /usr/share/doc/aiprouter/THIRD_PARTY_NOTICES
COPY --from=console /src/apps/aiprouter/dist /app/console
COPY --from=console /console-notices /usr/share/doc/aiprouter/CONSOLE_THIRD_PARTY_NOTICES
ENV AIPROUTER_CONSOLE_DIR=/app/console
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["aiprouter-server"]
CMD ["serve"]
