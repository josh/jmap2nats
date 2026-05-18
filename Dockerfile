FROM golang:1.25.9-alpine3.23@sha256:5caaf1cca9dc351e13deafbc3879fd4754801acba8653fa9540cea125d01a71f AS builder

RUN apk add --no-cache git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -mod=readonly -ldflags="-s -w" -o jmap2nats .
RUN ./jmap2nats version

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /src/jmap2nats /usr/local/bin/

LABEL org.opencontainers.image.source="https://github.com/josh/jmap2nats"
LABEL org.opencontainers.image.description="Bridge JMAP email push events to NATS JetStream"
LABEL org.opencontainers.image.licenses="MIT"

ENTRYPOINT ["jmap2nats"]
