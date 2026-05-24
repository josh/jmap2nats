FROM golang:1.26.3-alpine3.23@sha256:91eda9776261207ea25fd06b5b7fed8d397dd2c0a283e77f2ab6e91bfa71079d AS builder

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
