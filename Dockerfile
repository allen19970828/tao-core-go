# Stage 1: Build the Go binary using Multi-Stage Build
FROM golang:1.22-alpine AS builder

# Install build tools
RUN apk add --no-cache git gcc musl-dev

WORKDIR /app

# Copy dependency manifests
COPY go.mod go.sum ./

# Download dependencies using Go proxy mirror
ENV GOPROXY=https://goproxy.cn,https://goproxy.io,direct
ENV GOSUMDB=off
RUN go mod download

# Copy application source code
COPY . .

# Build lightweight static binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/bin/tao-core-go ./cmd/server

# Stage 2: Minimal runtime container (~15MB image)
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata
ENV TZ=Asia/Taipei

WORKDIR /app

# Copy binary and configuration from builder stage
COPY --from=builder /app/bin/tao-core-go /app/tao-core-go
COPY --from=builder /app/config /app/config

# Create uploads directory
RUN mkdir -p /app/uploads/media

EXPOSE 8080

ENTRYPOINT ["/app/tao-core-go"]
