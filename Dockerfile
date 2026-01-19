# Build stage
FROM golang:1.21-bookworm AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY *.go ./

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o bot .

# Runtime stage
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/bot .

# Copy .env file if it exists (optional, can use env vars instead)
COPY .env* ./ 2>/dev/null || true

# Run the bot
CMD ["./bot"]