FROM golang:1.27-alpine3.23@sha256:3747dcba41c8b0db3211fda4db61638b980e17ac5bb3c94460a975a9cfe19395 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -mod=readonly -ldflags="-s -w" -o jmap2nats .

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /src/jmap2nats /usr/local/bin/

LABEL org.opencontainers.image.source="https://github.com/josh/jmap2nats"
LABEL org.opencontainers.image.description="Bridge JMAP email push events to NATS JetStream"
LABEL org.opencontainers.image.licenses="MIT"

ENTRYPOINT ["jmap2nats"]
