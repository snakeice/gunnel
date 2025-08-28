# Build stage
FROM golang:1.22-bookworm AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags="-w -s" -o gunnel .

# Final stage
FROM debian:bookworm-slim

# Install ca-certificates for TLS
RUN apt-get update && \
    apt-get install -y ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Create non-root user
RUN useradd -r -u 1001 -s /bin/false gunnel

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
