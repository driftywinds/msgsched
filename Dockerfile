# Build stage - Use Debian for better C compatibility
FROM golang:1.21-bookworm AS builder

WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application (CGO enabled for SQLite)
RUN CGO_ENABLED=1 GOOS=linux go build -o discord-bot main.go

# Final stage - Use Debian slim for compatibility
FROM debian:12-slim

# Install runtime dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user
RUN adduser --disabled-password --gecos "" --uid 1000 discordbot

WORKDIR /app

# Copy binary from builder
COPY --from=builder --chown=discordbot:discordbot /app/discord-bot .

# Create data directory for persistent storage
RUN mkdir -p /data && chown discordbot:discordbot /data

# Switch to non-root user
USER discordbot

# Command to run the bot
CMD ["./discord-bot"]