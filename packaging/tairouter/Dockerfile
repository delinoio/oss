# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:24-bookworm-slim AS console
WORKDIR /src
RUN npm install --global pnpm@10.26.2
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/tairouter/package.json ./apps/tairouter/package.json
COPY packages/tairouter-client/package.json ./packages/tairouter-client/package.json
# Dependencies are installed in Linux; Dockerfile.dockerignore excludes host stores.
RUN pnpm install --filter @tairouter/console... --frozen-lockfile --ignore-scripts
COPY apps/tairouter ./apps/tairouter
COPY packages/tairouter-client ./packages/tairouter-client
COPY scripts/localization ./scripts/localization
COPY scripts/tairouter/console-notices.mjs ./scripts/tairouter/console-notices.mjs
RUN pnpm --dir apps/tairouter build && test -s apps/tairouter/dist/index.html
RUN node scripts/tairouter/console-notices.mjs /src/node_modules/.pnpm /console-notices

FROM --platform=$BUILDPLATFORM golang:1.26.10-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download all
RUN apt-get update && apt-get install -y --no-install-recommends python3 && rm -rf /var/lib/apt/lists/*
COPY scripts/tairouter/licenses.py /src/licenses.py
RUN python3 -c 'from pathlib import Path; from licenses import bundle; bundle(Path("/src"), Path("/out-notices"))'
COPY servers/tairouter ./servers/tairouter
COPY packages/tairouter ./packages/tairouter
COPY protos ./protos
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/tairouter-server ./servers/tairouter
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/tairouter-server /usr/local/bin/tairouter-server
COPY LICENSE NOTICE /usr/share/doc/tairouter/
COPY --from=build /out-notices /usr/share/doc/tairouter/THIRD_PARTY_NOTICES
COPY --from=console /src/apps/tairouter/dist /app/console
COPY --from=console /console-notices /usr/share/doc/tairouter/CONSOLE_THIRD_PARTY_NOTICES
ENV TAIROUTER_CONSOLE_DIR=/app/console
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["tairouter-server"]
CMD ["serve"]
