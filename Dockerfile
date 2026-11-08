# =============================================================================
# WorkforceOps Lab — single image carrying all four binaries.
# Pure-Go build (no CGO): CGO_ENABLED=0 throughout.
# Fly.io selects the process via [processes] cmd overrides (see fly.toml).
# =============================================================================

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/shifthub-api ./cmd/shifthub-api && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/payloop-api ./cmd/payloop-api && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/sync-worker ./cmd/sync-worker && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/workforceops ./cmd/workforceops

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=build /out/* ./
EXPOSE 8081 8082
# Default: ShiftHub API, which co-hosts the console in v0.1.
CMD ["./shifthub-api"]
