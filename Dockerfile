FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY core ./core
COPY cmd/aether-server ./cmd/aether-server
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/aether-server ./cmd/aether-server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/aether-server /usr/local/bin/aether-server
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/aether-server"]
