# syntax=docker/dockerfile:1
FROM golang:1.21-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/pojiagw .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S pojiagw \
    && adduser -S -G pojiagw -h /app pojiagw \
    && mkdir -p /opt/pojiagw /data \
    && chown -R pojiagw:pojiagw /opt/pojiagw /data

COPY --from=build /out/pojiagw /opt/pojiagw/pojiagw
COPY docker/entrypoint.sh /usr/local/bin/pojiagw-entrypoint
RUN chmod 0755 /usr/local/bin/pojiagw-entrypoint /opt/pojiagw/pojiagw

USER pojiagw
WORKDIR /opt/pojiagw
EXPOSE 7878
VOLUME ["/data"]

ENV POJIAGW_CONFIG=/data/config.json \
    POJIAGW_STATE=/data/state.json \
    POJIAGW_BIND=0.0.0.0 \
    POJIAGW_PORT=7878

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:7878/health || exit 1

ENTRYPOINT ["/usr/local/bin/pojiagw-entrypoint"]
