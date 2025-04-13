FROM golang:1.20-alpine AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy go.mod and go.sum to download dependencies
COPY go.mod go.sum ./

# Download all dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o pricetelegrambot .

# Create a minimal image for running the application
FROM alpine:latest  

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the binary from builder
COPY --from=builder /app/pricetelegrambot .

# Set the command to run the application
CMD ["./pricetelegrambot"] 