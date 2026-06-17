# syntax=docker/dockerfile:1.7
# ---- build stage ----------------------------------------------------------
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Cache module downloads independently of source changes.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Static, stripped, reproducible binaries for both services.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/app && \
    go build -trimpath -ldflags="-s -w" -o /out/notifier ./cmd/notifier

# ---- runtime base ---------------------------------------------------------
FROM alpine:3.20 AS runtime-base

RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S app && adduser -S -G app app

WORKDIR /app
USER app

# ---- app image (HTTP API + scanner) ---------------------------------------
# HTML templates are go:embed-ed into the binary — nothing else to copy.
FROM runtime-base AS app
COPY --from=builder /out/app /app/app
EXPOSE 8080
ENTRYPOINT ["/app/app"]

# ---- notifier image (NATS consumer; admin HTTP /metrics) ------------------
FROM runtime-base AS notifier
COPY --from=builder /out/notifier /app/notifier
EXPOSE 9091
ENTRYPOINT ["/app/notifier"]
