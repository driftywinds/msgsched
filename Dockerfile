# Build stage
FROM golang:1.21-alpine AS builder

# Install build dependencies
RUN apk add --no-cache gcc musl-dev

WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build -o discord-bot main.go

# Final stage
FROM alpine:latest

# Install runtime dependencies (sqlite3 needs libc)
RUN apk add --no-cache ca-certificates sqlite-libs tzdata

# Create non-root user
RUN adduser -D -u 1000 discordbot

WORKDIR /app

# Copy binary from builder
COPY --from=builder --chown=discordbot:discordbot /app/discord-bot .

# Create data directory for persistent storage
RUN mkdir -p /data && chown discordbot:discordbot /data

# Switch to non-root user
USER discordbot

# Command to run the bot
CMD ["./discord-bot"]