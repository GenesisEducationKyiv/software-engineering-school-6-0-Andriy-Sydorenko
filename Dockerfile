# syntax=docker/dockerfile:1.7
# ---- build stage ----------------------------------------------------------
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Cache module downloads independently of source changes.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Static binary, stripped, reproducible.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/server

# ---- runtime stage --------------------------------------------------------
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S app && adduser -S -G app app

WORKDIR /app
COPY --from=builder /out/app /app/app
COPY --from=builder /src/internal/templates /app/internal/templates

USER app
EXPOSE 8080
ENTRYPOINT ["/app/app"]
