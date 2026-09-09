FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /oraclenode ./cmd/oraclenode

FROM gcr.io/distroless/static-debian12:nonroot
# Sources are reached over TLS, so the image needs a trust store.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /oraclenode /oraclenode
USER nonroot:nonroot
ENTRYPOINT ["/oraclenode"]
