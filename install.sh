#!/usr/bin/env bash
# pojiagw 一体化安装（Linux）：二进制 + 配置 + systemd 服务
# 用法：
#   sudo ./install.sh [二进制路径]                      # 默认 build/pojiagw-linux-amd64
#   sudo PREFIX=/opt/pojiagw PORT=7878 BIND=0.0.0.0 ACCESS_KEY=sk-local ./install.sh
#   sudo ./install.sh --uninstall
set -euo pipefail

PREFIX="${PREFIX:-/opt/pojiagw}"
PORT="${PORT:-7878}"
BIND="${BIND:-127.0.0.1}"
ACCESS_KEY="${ACCESS_KEY:-}"
ADMIN_TOKEN="${ADMIN_TOKEN:-}"
SERVICE_NAME="${SERVICE_NAME:-pojiagw}"
SERVICE_USER="${SERVICE_USER:-pojiagw}"
UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"

log() { printf '\033[36m[pojiagw]\033[0m %s\n' "$*"; }
die() { printf '\033[31m[pojiagw]\033[0m %s\n' "$*" >&2; exit 1; }

if [[ "${1:-}" == "--uninstall" ]]; then
  log "停止并移除服务 $SERVICE_NAME"
  systemctl disable --now "$SERVICE_NAME" 2>/dev/null || true
  rm -f "$UNIT_PATH"
  systemctl daemon-reload || true
  log "保留数据目录 $PREFIX（如需彻底删除： rm -rf $PREFIX）"
  exit 0
fi

BIN_SRC="${1:-build/pojiagw-linux-amd64}"
[[ -f "$BIN_SRC" ]] || die "找不到二进制：$BIN_SRC"
[[ "$(id -u)" -eq 0 ]] || die "请用 root / sudo 运行"

log "安装到 $PREFIX"
install -d -m 0755 "$PREFIX"
install -m 0755 "$BIN_SRC" "$PREFIX/pojiagw"

if [[ ! -f "$PREFIX/config.json" ]]; then
  log "生成默认配置"
  "$PREFIX/pojiagw" init -config "$PREFIX/config.json" >/dev/null
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
Description=pojiagw gateway manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
WorkingDirectory=$PREFIX
ExecStart=$PREFIX/pojiagw serve -config $PREFIX/config.json -state $PREFIX/state.json
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
