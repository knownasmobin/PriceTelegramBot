# Build stage
FROM golang:1.22.3-alpine AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy go.mod and go.sum to download dependencies
COPY go.mod go.sum ./

# Download all dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the application
RUN go build -o telegram-bot

# Runtime stage
FROM alpine:latest

# Install required dependencies for chromium
RUN apk update && apk add --no-cache \
    ca-certificates \
    chromium \
    fontconfig \
    font-noto \
    ttf-freefont \
    tini \
    && rm -rf /var/cache/apk/*

# Set environment variables
ENV DOCKER_CONTAINER=1
ENV CHROME_BIN=/usr/bin/chromium-browser

# Verify Chrome is installed and accessible
RUN test -f $CHROME_BIN && echo "Chrome installation verified at $CHROME_BIN"

# Create non-root user for security
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
USER appuser

WORKDIR /app

# Copy binary from builder stage
COPY --from=builder /app/telegram-bot /app/

# Use tini as init system
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["/app/telegram-bot"] 