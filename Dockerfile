# Build stage
FROM golang:1.21-alpine AS builder

# Install build dependencies
RUN apk add --no-cache gcc musl-dev

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN go build -o discord-bot main.go

# Final stage
FROM alpine:latest

# Install runtime dependencies (sqlite3 needs libc)
RUN apk add --no-cache ca-certificates sqlite-libs

# Create non-root user
RUN adduser -D -u 1000 discordbot

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/discord-bot .
COPY --from=builder /app/.env.example ./.env

# Copy SQLite database if exists (for initial setup)
COPY --from=builder /app/schedules.db ./schedules.db 2>/dev/null || :

# Create data directory for persistent storage
RUN mkdir -p /data && chown discordbot:discordbot /data

# Switch to non-root user
USER discordbot

# Expose volume for persistent data
VOLUME /data

# Command to run the bot
CMD ["./discord-bot"]