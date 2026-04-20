# Build stage
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o caw ./cmd/wrapper

# Final image — alpine gives us bash/sh for the server-side agent executor
FROM alpine:3.19
RUN apk add --no-cache bash python3 py3-pip
COPY --from=builder /app/caw /caw
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
EXPOSE 8080
ENTRYPOINT ["/caw"]
