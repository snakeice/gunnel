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

# Create config directory
RUN mkdir -p /etc/gunnel && \
    chown -R gunnel:gunnel /etc/gunnel

# Copy example configs
COPY --chown=gunnel:gunnel example/server.yaml /etc/gunnel/server.yaml.example
COPY --chown=gunnel:gunnel example/client.yaml /etc/gunnel/client.yaml.example

USER gunnel

# Expose ports
# 8081 - QUIC/Server port
# 80 - HTTP
# 443 - HTTPS
EXPOSE 8081 80 443

VOLUME ["/etc/gunnel"]

ENTRYPOINT ["gunnel"]
CMD ["--help"]
