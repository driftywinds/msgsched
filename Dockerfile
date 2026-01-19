# Build stage
FROM golang:1.21-alpine AS builder

# Install build dependencies for SQLite
RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY *.go ./

# Build with proper SQLite flags for Alpine
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o bot .

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates sqlite-libs

WORKDIR /root/

# Copy the binary from builder
COPY --from=builder /app/bot .

# Create directory for database
RUN mkdir -p /root/data

# Run the bot
CMD ["./bot"]