# Local Testing Guide - Gunnel

## Quick Start (Local Testing)

When running Gunnel locally with self-signed certificates, you need to enable insecure mode:

### Server
```bash
./gunnel server -c ./example/server.yaml
```

### Client
```bash
export GUNNEL_TOKEN="YOUR_SHARED_TOKEN"
export GUNNEL_INSECURE=true
./gunnel client -c ./example/client.yaml
```

## Understanding the TLS Certificate

### Self-Signed Certificates

Gunnel generates self-signed TLS certificates for development/testing:
- **Location**: Generated in-memory at runtime
- **Validity**: Localhost, 127.0.0.1, *.localhost
- **Duration**: 365 days
- **Certificate Authority**: Self-signed (not in system CA store)

### Certificate Verification Modes

**Production Mode** (Default - `GUNNEL_INSECURE=false`):
- Certificate signature verified against system CA store
- Self-signed certs are **rejected** ✓ (correct security behavior)
- Hostname validation enforced
- **Use certmagic for production** - handles real Let's Encrypt certs

**Development/Testing Mode** (`GUNNEL_INSECURE=true`):
- Certificate signature verification **skipped**
- Self-signed certs are **accepted**
- Allows quick local testing
- **Never use in production**

## Why You Need GUNNEL_INSECURE=true for Local Testing

When you run Gunnel locally:

1. Server generates a **self-signed certificate** (not signed by a trusted CA)
2. Client by default verifies certificates are signed by a **trusted authority**
3. Self-signed certs from localhost are **not** in the system CA store
4. Verification **fails** unless you set `GUNNEL_INSECURE=true`

```
Error without GUNNEL_INSECURE:
x509: certificate signed by unknown authority
```

## Production Deployment

For production, use proper certificates:

```yaml
# server.yaml
domain: example.com
cert:
  enabled: true
  email: admin@example.com
  production: true  # Use Let's Encrypt production
```

Gunnel will:
- Automatically obtain certificates from Let's Encrypt
- Handle certificate renewal
- Use proper certificate authority
- No need for `GUNNEL_INSECURE=true`

## Security Best Practices

1. **Never set GUNNEL_INSECURE=true in production**
2. **Use certificates from a trusted authority** (Let's Encrypt, etc.)
3. **Local testing only** - use proper certs for staging/prod
4. **Rotate certificates regularly** (Let's Encrypt handles this)

## Testing with Load Script

```bash
# Start with insecure mode for testing
export GUNNEL_INSECURE=true

# Run load test with 100 concurrent clients
./scripts/load-test.sh 100 30
```

## Docker Testing

```dockerfile
# Dockerfile.test
FROM golang:1.24

WORKDIR /app
COPY . .

ENV GUNNEL_INSECURE=true
ENV GUNNEL_TOKEN=test_token

RUN go build -o gunnel .

# Test: start server + client
CMD ["./gunnel", "server", "-c", "./example/server.yaml"]
```

## Troubleshooting

### Error: "certificate signed by unknown authority"
**Solution**: Set `export GUNNEL_INSECURE=true`

### Error: "certificate is not valid for any names"
**Solution**: Ensure you're connecting to `localhost`, `127.0.0.1`, or `*.localhost`

### Error: "CRYPTO_ERROR 0x12a"
**Solution**: Verify both server and client are using the same TLS settings
