#!/bin/sh
# gateway-manager 容器入口：首次启动自动生成配置，按环境变量修正监听地址，然后前台运行
set -eu

# 允许 docker run <image> version / init / discover 等子命令
if [ "$#" -gt 0 ] && [ "${1#-}" = "$1" ] && [ "$1" != "serve" ]; then
  exec /opt/gateway-manager/gateway-manager "$@"
fi

# 兼容旧变量名（POJIAGW_*）
GATEWAY_MANAGER_CONFIG="${GATEWAY_MANAGER_CONFIG:-${POJIAGW_CONFIG:-}}"
GATEWAY_MANAGER_STATE="${GATEWAY_MANAGER_STATE:-${POJIAGW_STATE:-}}"
GATEWAY_MANAGER_BIND="${GATEWAY_MANAGER_BIND:-${POJIAGW_BIND:-0.0.0.0}}"
GATEWAY_MANAGER_PORT="${GATEWAY_MANAGER_PORT:-${POJIAGW_PORT:-7878}}"
GATEWAY_MANAGER_ACCESS_KEY="${GATEWAY_MANAGER_ACCESS_KEY:-${POJIAGW_ACCESS_KEY:-}}"
GATEWAY_MANAGER_ADMIN_TOKEN="${GATEWAY_MANAGER_ADMIN_TOKEN:-${POJIAGW_ADMIN_TOKEN:-}}"

CONFIG="${GATEWAY_MANAGER_CONFIG:-/data/config.json}"
STATE="${GATEWAY_MANAGER_STATE:-/data/state.json}"
BIND="${GATEWAY_MANAGER_BIND:-0.0.0.0}"
PORT="${GATEWAY_MANAGER_PORT:-7878}"
BIN="/opt/gateway-manager/gateway-manager"

if [ ! -f "$CONFIG" ]; then
  echo "[gateway-manager] 生成默认配置: $CONFIG"
  "$BIN" init -config "$CONFIG" >/dev/null
fi

# 容器里必须监听 0.0.0.0；顺便对齐端口
sed -i "s/\"bind\": \"[^\"]*\"/\"bind\": \"$BIND\"/" "$CONFIG" 2>/dev/null || true
sed -i "s/\"port\": [0-9]\+/\"port\": $PORT/" "$CONFIG" 2>/dev/null || true

if [ -n "${GATEWAY_MANAGER_ACCESS_KEY:-}" ]; then
  sed -i "s/\"access_keys\": \[[^]]*\]/\"access_keys\": [\"$GATEWAY_MANAGER_ACCESS_KEY\"]/" "$CONFIG" 2>/dev/null || true
fi
if [ -n "${GATEWAY_MANAGER_ADMIN_TOKEN:-}" ]; then
  sed -i "s/\"admin_token\": \"[^\"]*\"/\"admin_token\": \"$GATEWAY_MANAGER_ADMIN_TOKEN\"/" "$CONFIG" 2>/dev/null || true
fi

echo "[gateway-manager] 监听 $BIND:$PORT · 控制台 /ui · 指标 /metrics"
exec "$BIN" serve -config "$CONFIG" -state "$STATE" "$@"
