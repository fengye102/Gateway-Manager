#!/usr/bin/env bash
# gateway-manager 一体化安装（Linux）：二进制 + 配置 + systemd 服务
# 用法：
#   sudo ./install.sh [二进制路径]                      # 默认 build/gateway-manager-linux-amd64
#   sudo PREFIX=/opt/gateway-manager PORT=7878 BIND=0.0.0.0 ACCESS_KEY=sk-local ./install.sh
#   sudo ./install.sh --uninstall
set -euo pipefail

PREFIX="${PREFIX:-/opt/gateway-manager}"
PORT="${PORT:-7878}"
BIND="${BIND:-127.0.0.1}"
ACCESS_KEY="${ACCESS_KEY:-}"
ADMIN_TOKEN="${ADMIN_TOKEN:-}"
SERVICE_NAME="${SERVICE_NAME:-gateway-manager}"
SERVICE_USER="${SERVICE_USER:-gateway-manager}"
UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"

log() { printf '\033[36m[gateway-manager]\033[0m %s\n' "$*"; }
die() { printf '\033[31m[gateway-manager]\033[0m %s\n' "$*" >&2; exit 1; }

if [[ "${1:-}" == "--uninstall" ]]; then
  log "停止并移除服务 $SERVICE_NAME"
  systemctl disable --now "$SERVICE_NAME" 2>/dev/null || true
  rm -f "$UNIT_PATH"
  systemctl daemon-reload || true
  log "保留数据目录 $PREFIX（如需彻底删除： rm -rf $PREFIX）"
  exit 0
fi

BIN_SRC="${1:-build/gateway-manager-linux-amd64}"
[[ -f "$BIN_SRC" ]] || die "找不到二进制：$BIN_SRC"
[[ "$(id -u)" -eq 0 ]] || die "请用 root / sudo 运行"

log "安装到 $PREFIX"
install -d -m 0755 "$PREFIX"
install -m 0755 "$BIN_SRC" "$PREFIX/gateway-manager"

if [[ ! -f "$PREFIX/config.json" ]]; then
  log "生成默认配置"
  "$PREFIX/gateway-manager" init -config "$PREFIX/config.json" >/dev/null
fi
sed -i "s/\"port\": [0-9]*/\"port\": $PORT/" "$PREFIX/config.json"
sed -i "s/\"bind\": \"[^\"]*\"/\"bind\": \"$BIND\"/" "$PREFIX/config.json"
if [[ -n "$ACCESS_KEY" ]]; then
  sed -i "s/\"access_keys\": \[[^]]*\]/\"access_keys\": [\"$ACCESS_KEY\"]/" "$PREFIX/config.json"
fi
if [[ -n "$ADMIN_TOKEN" ]]; then
  sed -i "s/\"admin_token\": \"[^\"]*\"/\"admin_token\": \"$ADMIN_TOKEN\"/" "$PREFIX/config.json"
fi

if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
  log "创建系统用户 $SERVICE_USER"
  useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER" || true
fi
chown -R "$SERVICE_USER":"$SERVICE_USER" "$PREFIX"
chmod 750 "$PREFIX"
chmod 640 "$PREFIX/config.json"

log "写入 systemd unit"
cat > "$UNIT_PATH" <<UNIT
[Unit]
Description=gateway-manager gateway manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
WorkingDirectory=$PREFIX
ExecStart=$PREFIX/gateway-manager serve -config $PREFIX/config.json -state $PREFIX/state.json
Restart=always
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=$PREFIX
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now "$SERVICE_NAME"
sleep 1
log "服务状态："
systemctl --no-pager --lines=5 status "$SERVICE_NAME" || true
log "控制台： http://$BIND:$PORT/ui"
log "指标：   http://$BIND:$PORT/metrics"
log "完成。卸载： sudo ./install.sh --uninstall"
