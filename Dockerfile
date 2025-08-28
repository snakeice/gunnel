# Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags="-w -s" -o gunnel .

# Final stage
FROM alpine:latest

# Install ca-certificates for TLS
RUN apk --no-cache add ca-certificates

# Create non-root user
RUN adduser -D -u 1001 -s /sbin/nologin gunnel

# Copy binary from builder
COPY --from=builder /app/gunnel /usr/local/bin/gunnel

# Copy example configs from builder
COPY --from=builder /app/example/server.yaml /tmp/server.yaml.example
COPY --from=builder /app/example/client.yaml /tmp/client.yaml.example

# Create config directory and move configs with proper ownership
RUN mkdir -p /etc/gunnel && \
    mv /tmp/server.yaml.example /etc/gunnel/server.yaml.example && \
    mv /tmp/client.yaml.example /etc/gunnel/client.yaml.example && \
    chown -R gunnel:gunnel /etc/gunnel

USER gunnel

# Expose ports
# 8081 - QUIC/Server port
# 80 - HTTP
# 443 - HTTPS
EXPOSE 8081 80 443

VOLUME ["/etc/gunnel"]

ENTRYPOINT ["gunnel"]
CMD ["--help"]
