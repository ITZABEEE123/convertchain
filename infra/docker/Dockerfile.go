ARG GO_VERSION=1.26

FROM golang:${GO_VERSION} AS builder

WORKDIR /src/go-engine

COPY go-engine/go.mod go-engine/go.sum ./
RUN go mod download

COPY go-engine/ ./
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/engine ./cmd/server

FROM alpine:3.22

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /out/engine /engine

EXPOSE 9000
USER 65534:65534
ENTRYPOINT ["/engine"]
