# syntax=docker/dockerfile:1
FROM golang:1.21-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/gateway-manager .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S gateway-manager \
    && adduser -S -G gateway-manager -h /app gateway-manager \
    && mkdir -p /opt/gateway-manager /data \
    && chown -R gateway-manager:gateway-manager /opt/gateway-manager /data

COPY --from=build /out/gateway-manager /opt/gateway-manager/gateway-manager
COPY docker/entrypoint.sh /usr/local/bin/gateway-manager-entrypoint
RUN chmod 0755 /usr/local/bin/gateway-manager-entrypoint /opt/gateway-manager/gateway-manager

USER gateway-manager
WORKDIR /opt/gateway-manager
EXPOSE 7878
VOLUME ["/data"]

ENV GATEWAY_MANAGER_CONFIG=/data/config.json \
    GATEWAY_MANAGER_STATE=/data/state.json \
    GATEWAY_MANAGER_BIND=0.0.0.0 \
    GATEWAY_MANAGER_PORT=7878

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:7878/health || exit 1

ENTRYPOINT ["/usr/local/bin/gateway-manager-entrypoint"]
