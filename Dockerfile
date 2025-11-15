# syntax=docker/dockerfile:1.7

# Build cache mounts speed up dependency downloads

ARG GO_VERSION=1.25
ARG RESTIC_VERSION=0.17.3

FROM golang:${GO_VERSION}-alpine AS base
WORKDIR /src
RUN apk add --no-cache git bash ca-certificates && update-ca-certificates
COPY go.mod ./
# Copy go.sum if present; ignore if missing
# hadolint ignore=DL3059
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download -x || true
COPY . .

# Fetch restic binary separately (multi-stage) so we can ship it in final image.
FROM alpine:3.20 AS restic
ARG RESTIC_VERSION
ARG TARGETOS
ARG TARGETARCH
RUN apk add --no-cache wget ca-certificates && update-ca-certificates \
    && : "TARGETOS=${TARGETOS:-linux}" "TARGETARCH=${TARGETARCH:-amd64}" \
    && wget -q -O /tmp/restic.bz2 "https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/restic_${RESTIC_VERSION}_${TARGETOS}_${TARGETARCH}.bz2" \
    && bunzip2 /tmp/restic.bz2 \
    && chmod +x /tmp/restic \
    && mv /tmp/restic /usr/local/bin/restic \
    && /usr/local/bin/restic version || true

# Run unit tests. Build will fail if tests fail.
FROM base AS test
COPY --from=restic /usr/local/bin/restic /usr/local/bin/restic
# Ensure module cache
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go test ./...

# Build the manager and api binaries
FROM base AS build
COPY --from=restic /usr/local/bin/restic /usr/local/bin/restic
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /out/marina ./cmd/manager && \
    CGO_ENABLED=0 go build -o /out/marina-api ./cmd/api

FROM alpine:3.20 AS runner
WORKDIR /
RUN apk add --no-cache ca-certificates bash curl coreutils tzdata \
    && mkdir -p /backup /var/lib/marina /app/web \
    && update-ca-certificates
COPY --from=build /out/marina /usr/local/bin/marina
COPY --from=build /out/marina-api /usr/local/bin/marina-api
COPY --from=restic /usr/local/bin/restic /usr/local/bin/restic
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh
ENV PATH="/usr/local/bin:/usr/bin:/bin"
ENV API_PORT=8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
