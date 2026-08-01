# Stage 1: Build the Go binary using Multi-Stage Build
FROM golang:1.26-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder

# Install only the tools required for module download.
RUN apk add --no-cache ca-certificates git

WORKDIR /app

# Copy dependency manifests
COPY go.mod go.sum ./

# Download dependencies with the public checksum database enabled.
ENV GOPROXY=https://proxy.golang.org,direct
ENV GOSUMDB=sum.golang.org
RUN go mod download

# Copy application source code
COPY . .

# Build lightweight static binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/bin/tao-core-go ./cmd/server

# Stage 2: Minimal non-root runtime container
FROM alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce

RUN apk --no-cache add ca-certificates tzdata && \
    addgroup -S tao && adduser -S -G tao tao
ENV TZ=Asia/Taipei

WORKDIR /app

# Copy binary and configuration from builder stage
COPY --from=builder --chown=tao:tao /app/bin/tao-core-go /app/tao-core-go
COPY --from=builder --chown=tao:tao /app/config /app/config

# Create uploads directory
RUN mkdir -p /app/uploads/media && chown -R tao:tao /app/uploads

USER tao

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/health || exit 1

ENTRYPOINT ["/app/tao-core-go"]
