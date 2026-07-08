FROM golang:alpine AS builder

WORKDIR /app

# Install dependencies needed for CGO if any, though ideally CGO_ENABLED=0
# We install tzdata and ca-certificates
RUN apk add --no-cache tzdata ca-certificates

# Download go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build server and migrate binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/bin/server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/bin/migrate ./cmd/migrate

# Stage 2: Runtime
FROM alpine:3.19

WORKDIR /app

# Copy timezone data & CA certificates
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy binaries
COPY --from=builder /app/bin/server /app/server
COPY --from=builder /app/bin/migrate /app/migrate

# Copy migrations folder
COPY migrations/ /app/migrations/

# Copy entrypoint script
COPY scripts/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

# Expose port
EXPOSE 8000

# Set entrypoint
ENTRYPOINT ["/app/entrypoint.sh"]
