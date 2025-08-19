FROM golang:1.24.1-alpine AS builder

# Install required packages for CGO and SQLite
RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app

# Copy go.mod and go.sum first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Enable CGO and build both applications
ENV CGO_ENABLED=1
RUN go build -o whatsapp-bridge main.go
RUN go build -o daily-summary daily-summary.go

FROM alpine:latest

# Install SQLite, cron and other runtime dependencies
RUN apk add --no-cache sqlite ca-certificates dcron tzdata

WORKDIR /app

# Copy the binaries from builder stage
COPY --from=builder /app/whatsapp-bridge .
COPY --from=builder /app/daily-summary .

# Copy entrypoint script
COPY entrypoint.sh .
RUN chmod +x entrypoint.sh

# Create directories for databases and prompts
RUN mkdir -p store prompts

# Create non-root user for security
RUN adduser -D -s /bin/sh whatsapp
RUN chown -R whatsapp:whatsapp /app
# Don't switch to whatsapp user here - we need root for cron
# The entrypoint script will handle user switching appropriately

# Expose port for HTTP API
EXPOSE 8080

CMD ["./entrypoint.sh"]