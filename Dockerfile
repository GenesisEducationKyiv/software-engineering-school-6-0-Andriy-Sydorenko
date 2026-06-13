# syntax=docker/dockerfile:1.7
# ---- build stage ----------------------------------------------------------
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Cache module downloads independently of source changes.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Build BOTH binaries from the one module. Static, stripped, reproducible.
# Compose selects which one runs per service via `command:`.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w" -o /out/notifier ./cmd/notifier

# ---- runtime stage --------------------------------------------------------
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S app && adduser -S -G app app

WORKDIR /app
COPY --from=builder /out/server /app/server
COPY --from=builder /out/notifier /app/notifier
COPY --from=builder /src/internal/templates /app/internal/templates

USER app
# Default to the core; compose overrides `command:` for the notifier service.
EXPOSE 8080
CMD ["/app/server"]
