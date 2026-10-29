#!/bin/sh
# pojiagw 容器入口：首次启动自动生成配置，按环境变量修正监听地址，然后前台运行
set -eu

# 允许 docker run <image> version / init / discover 等子命令
if [ "$#" -gt 0 ] && [ "${1#-}" = "$1" ] && [ "$1" != "serve" ]; then
  exec /opt/pojiagw/pojiagw "$@"
fi

CONFIG="${POJIAGW_CONFIG:-/data/config.json}"
STATE="${POJIAGW_STATE:-/data/state.json}"
BIND="${POJIAGW_BIND:-0.0.0.0}"
PORT="${POJIAGW_PORT:-7878}"
BIN="/opt/pojiagw/pojiagw"

if [ ! -f "$CONFIG" ]; then
  echo "[pojiagw] 生成默认配置: $CONFIG"
  "$BIN" init -config "$CONFIG" >/dev/null
fi

# 容器里必须监听 0.0.0.0；顺便对齐端口
sed -i "s/\"bind\": \"[^\"]*\"/\"bind\": \"$BIND\"/" "$CONFIG" 2>/dev/null || true
sed -i "s/\"port\": [0-9]\+/\"port\": $PORT/" "$CONFIG" 2>/dev/null || true

if [ -n "${POJIAGW_ACCESS_KEY:-}" ]; then
  sed -i "s/\"access_keys\": \[[^]]*\]/\"access_keys\": [\"$POJIAGW_ACCESS_KEY\"]/" "$CONFIG" 2>/dev/null || true
fi
if [ -n "${POJIAGW_ADMIN_TOKEN:-}" ]; then
  sed -i "s/\"admin_token\": \"[^\"]*\"/\"admin_token\": \"$POJIAGW_ADMIN_TOKEN\"/" "$CONFIG" 2>/dev/null || true
fi

echo "[pojiagw] 监听 $BIND:$PORT · 控制台 /ui · 指标 /metrics"
exec "$BIN" serve -config "$CONFIG" -state "$STATE" "$@"
