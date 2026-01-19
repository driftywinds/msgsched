# ---------- Build stage ----------
FROM golang:1.22-bookworm AS builder

WORKDIR /app

# Required for go-sqlite3 (CGO)
RUN apt-get update && apt-get install -y \
    gcc \
    libc6-dev \
    && rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO must be enabled for sqlite
ENV CGO_ENABLED=1
ENV GOOS=linux

RUN go build -o discord-bot main.go

# ---------- Runtime stage ----------
FROM debian:bookworm-slim

WORKDIR /app

# sqlite runtime deps
RUN apt-get update && apt-get install -y \
    ca-certificates \
    tzdata \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /app/discord-bot /app/discord-bot

# Where DB + env will live
VOLUME ["/app"]

CMD ["./discord-bot"]
