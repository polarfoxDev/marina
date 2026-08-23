# syntax=docker/dockerfile:1.7

# Build cache mounts speed up dependency downloads

ARG GO_VERSION=1.25

FROM golang:${GO_VERSION}-alpine AS base
WORKDIR /src
RUN apk add --no-cache git bash ca-certificates && update-ca-certificates
COPY go.mod ./
# Copy go.sum if present; ignore if missing
# hadolint ignore=DL3059
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download -x || true
COPY . .

# Run unit tests. Build will fail if tests fail.
# Some backend tests exercise a real S3 API (via a throwaway MinIO
# container) and skip themselves when the docker CLI isn't reachable from
# inside this build stage -- which it isn't, so they no-op here and run for
# real in the integration-tests CI job instead.
FROM base AS test
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go test ./...

# Build the manager, api, and marinactl binaries
FROM base AS build
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -ldflags "-X github.com/polarfoxDev/marina/internal/version.Version=${VERSION}" -o /out/marina ./cmd/manager && \
    CGO_ENABLED=0 go build -ldflags "-X github.com/polarfoxDev/marina/internal/version.Version=${VERSION}" -o /out/marina-api ./cmd/api && \
    CGO_ENABLED=0 go build -ldflags "-X github.com/polarfoxDev/marina/internal/version.Version=${VERSION}" -o /out/marinactl ./cmd/marinactl

# Build the React frontend
FROM node:20-alpine AS frontend
RUN corepack enable
WORKDIR /app
COPY web/package.json web/pnpm-lock.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

FROM alpine:3.20 AS runner
WORKDIR /
RUN apk add --no-cache ca-certificates bash curl coreutils tzdata \
    && mkdir -p /backup /var/lib/marina /app/web \
    && update-ca-certificates
COPY --from=build /out/marina /usr/local/bin/marina
COPY --from=build /out/marina-api /usr/local/bin/marina-api
COPY --from=build /out/marinactl /usr/local/bin/marinactl
COPY --from=frontend /app/dist /app/web
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh
ENV PATH="/usr/local/bin:/usr/bin:/bin"
ENV API_PORT=8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
