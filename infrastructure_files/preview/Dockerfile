# Preview image for Autonoma preview environments.
#
# Builds the combined NetBird server (Management + Signal + Relay + embedded Dex
# IdP, all multiplexed on one port) and wraps it in an entrypoint that renders
# /etc/netbird/config.yaml from environment variables, because a preview's public
# URL is only known at deploy time. See README.md in this directory.
#
# Build context is the repository root.

FROM golang:1.25-bookworm AS builder
WORKDIR /app

# CGO is required: the embedded IdP and the activity store use go-sqlite3.
RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG NETBIRD_VERSION=preview
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-s -w -X github.com/netbirdio/netbird/version.version=${NETBIRD_VERSION}" \
    -o netbird-server ./combined

FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /app/netbird-server /go/bin/netbird-server
COPY infrastructure_files/preview/entrypoint.sh /usr/local/bin/netbird-preview-entrypoint

RUN chmod +x /usr/local/bin/netbird-preview-entrypoint \
    && mkdir -p /var/lib/netbird /etc/netbird

ENV NB_PREVIEW_PORT=8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/netbird-preview-entrypoint"]
