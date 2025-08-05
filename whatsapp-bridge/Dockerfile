FROM golang:1.24.1-alpine AS builder

# Install required packages for CGO and SQLite
RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app

# Copy go.mod and go.sum first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Enable CGO and build the application
ENV CGO_ENABLED=1
RUN go build -o whatsapp-bridge main.go

FROM alpine:latest

# Install SQLite and other runtime dependencies
RUN apk add --no-cache sqlite ca-certificates

WORKDIR /app

# Copy the binary from builder stage
COPY --from=builder /app/whatsapp-bridge .

# Create store directory for databases
RUN mkdir -p store

# Create non-root user for security
RUN adduser -D -s /bin/sh whatsapp
RUN chown -R whatsapp:whatsapp /app
USER whatsapp

# Expose port for HTTP API
EXPOSE 8080

CMD ["./whatsapp-bridge"]