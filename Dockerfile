# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/pfs ./cmd/pfs

FROM alpine:3.20

RUN addgroup -S pfs && adduser -S -G pfs pfs

WORKDIR /app
COPY --from=builder /out/pfs /usr/local/bin/pfs

RUN mkdir -p /data && chown -R pfs:pfs /data

USER pfs
EXPOSE 8086
VOLUME ["/data"]

ENTRYPOINT ["pfs"]
CMD ["web", "-addr", "0.0.0.0:8086", "-disk", "/data/fms.pfs"]
