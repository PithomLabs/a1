# =============================================================================
# WorkforceOps Lab — single image carrying the one workforceops binary.
# Pure-Go build (no CGO): CGO_ENABLED=0 throughout.
# =============================================================================

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/workforceops ./cmd/workforceops

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=build /out/workforceops ./
EXPOSE 8081
CMD ["./workforceops", "serve", "--port", "8081"]
