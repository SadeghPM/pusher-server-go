FROM golang:1.24-alpine AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /pusher-clone .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /pusher-clone /usr/local/bin/pusher-clone
WORKDIR /app
EXPOSE 6001 5174 9601
ENTRYPOINT ["/usr/local/bin/pusher-clone"]
CMD ["-config", "/app/config.yaml"]
