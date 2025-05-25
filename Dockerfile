# Use multi-stage build to produce a minimal Docker image for dummyload

# Build stage
FROM golang:1.21-alpine AS builder
RUN apk add --no-cache git
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
# Build a statically linked binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o dummyload ./cmd/dummyload

# Final stage: minimal static image
FROM scratch
COPY --from=builder /app/dummyload /dummyload
EXPOSE 8080
ENTRYPOINT ["/dummyload"]
CMD ["-cores", "0", "-mem", "0", "-port", "8080"]