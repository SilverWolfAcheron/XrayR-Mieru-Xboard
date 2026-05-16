#!/usr/bin/env bash
# SPDX-License-Identifier: MPL-2.0
set -euo pipefail

# XrayR Mieru for Xboard 安装脚本
# 适合开源发布：不内置面板地址、节点 ID、server_token。
# 使用方式：
#   bash install_xrayr_mieru_from_root.sh install
#   bash install_xrayr_mieru_from_root.sh status
#   bash install_xrayr_mieru_from_root.sh logs
#   bash install_xrayr_mieru_from_root.sh restart
#   bash install_xrayr_mieru_from_root.sh config
#   bash install_xrayr_mieru_from_root.sh uninstall

INSTALL_DIR="/usr/local/XrayR"
CONFIG_DIR="/etc/XrayR"
CONFIG_FILE="${CONFIG_DIR}/config.yml"
SERVICE_FILE="/etc/systemd/system/XrayR.service"
SERVICE_NAME="XrayR"
NODE_TYPE="Mieru"
UPDATE_PERIODIC="${UPDATE_PERIODIC:-60}"
XRAYR_BIN="${XRAYR_BIN:-}"
XRAYR_UPDATE_URL="${XRAYR_UPDATE_URL:-https://github.com/SilverWolfAcheron/XrayR-Mieru-Xboard/releases/latest/download/XrayR-mieru-linux-amd64}"
XRAYR_UPDATE_FALLBACK_URL="${XRAYR_UPDATE_FALLBACK_URL:-https://github.com/SilverWolfAcheron/XrayR-Mieru-Xboard/releases/download/Update/XrayR-mieru-linux-amd64}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
PLAIN='\033[0m'

info() { echo -e "${GREEN}[信息]${PLAIN} $*"; }
warn() { echo -e "${YELLOW}[警告]${PLAIN} $*" >&2; }
fail() { echo -e "${RED}[错误]${PLAIN} $*" >&2; exit 1; }
title() { echo -e "\n${CYAN}========== $* ==========${PLAIN}\n"; }

require_root() {
  if [ "$(id -u)" -ne 0 ]; then
    fail "请使用 root 用户运行。"
  fi
}

require_systemd() {
  command -v systemctl >/dev/null 2>&1 || fail "当前脚本只支持 systemd 系统。"
}

prompt_required() {
  local name="$1"
  local prompt="$2"
  local value="${!name:-}"
  while [ -z "$value" ]; do
    read -r -p "$prompt" value
  done
  printf -v "$name" '%s' "$value"
}

prompt_secret_required() {
  local name="$1"
  local prompt="$2"
  local value="${!name:-}"
  while [ -z "$value" ]; do
    read -r -s -p "$prompt" value
    echo
  done
  printf -v "$name" '%s' "$value"
}

validate_inputs() {
  PANEL_URL="${PANEL_URL%/}"
  case "$PANEL_URL" in
    http://*|https://*) ;;
    *) fail "Xboard 面板地址必须以 http:// 或 https:// 开头。" ;;
  esac

  [[ "$NODE_ID" =~ ^[0-9]+$ ]] || fail "节点 ID 必须是数字。"
  [[ "$MIERU_PORT" =~ ^[0-9]+$ ]] || fail "Mieru 端口必须是数字。"
  if [ "$MIERU_PORT" -lt 1 ] || [ "$MIERU_PORT" -gt 65535 ]; then
    fail "Mieru 端口必须在 1-65535 之间。"
  fi

  MIERU_TRANSPORT="$(printf '%s' "$MIERU_TRANSPORT" | tr '[:lower:]' '[:upper:]')"
  case "$MIERU_TRANSPORT" in
    TCP|UDP) ;;
    *) fail "Mieru 传输方式只能是 TCP 或 UDP。" ;;
  esac
}

detect_binary() {
  if [ -n "$XRAYR_BIN" ]; then
    [ -f "$XRAYR_BIN" ] || fail "找不到 XRAYR_BIN 指定的文件：$XRAYR_BIN"
    echo "$XRAYR_BIN"
    return
  fi

  if [ -f /root/XrayR-mieru-linux-amd64 ]; then
    echo /root/XrayR-mieru-linux-amd64
    return
  fi

  if [ -f /root/XrayR ]; then
    echo /root/XrayR
    return
  fi

  fail "找不到 XrayR 二进制。请把编译好的文件放到 /root/XrayR-mieru-linux-amd64，或用 XRAYR_BIN=/path/to/XrayR 指定。"
}

read_install_inputs() {
  title "XrayR Mieru 对接 Xboard"

  prompt_required PANEL_URL "Xboard 面板地址（必填，例如 https://example.com）: "
  prompt_secret_required API_KEY "Xboard server_token（必填，输入时不显示）: "
  prompt_required NODE_ID "Xboard Mieru 节点 ID（必填）: "
  prompt_required MIERU_PORT "Mieru 兜底监听端口（必填，例如 25566，需与面板节点端口一致）: "
  prompt_required MIERU_TRANSPORT "Mieru 传输方式（TCP/UDP，必填）: "

  MIERU_TRAFFIC_PATTERN="${MIERU_TRAFFIC_PATTERN:-}"
  read -r -p "Mieru traffic_pattern（可选，直接回车留空）: " input_pattern
  MIERU_TRAFFIC_PATTERN="${input_pattern:-$MIERU_TRAFFIC_PATTERN}"

  validate_inputs
}

stop_old_services() {
  info "停止可能冲突的旧服务。"
  systemctl stop "$SERVICE_NAME" 2>/dev/null || true
  systemctl stop mihomo-mieru 2>/dev/null || true
  systemctl stop mihomo-mieru-xboard 2>/dev/null || true
}

install_binary() {
  local src="$1"
  info "安装 XrayR 二进制：$src"
  mkdir -p "$INSTALL_DIR" "$CONFIG_DIR"
  install -m 755 "$src" "${INSTALL_DIR}/XrayR"
}

download_file() {
  local url="$1" dst="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -L --fail --retry 3 --connect-timeout 15 -o "$dst" "$url"
    return
  fi
  if command -v wget >/dev/null 2>&1; then
    wget -O "$dst" "$url"
    return
  fi
  fail "未找到 curl 或 wget，无法下载更新。"
}

download_update_binary() {
  local dst="$1"
  info "下载更新：${XRAYR_UPDATE_URL}"
  if download_file "$XRAYR_UPDATE_URL" "$dst"; then
    return
  fi
  if [ -n "${XRAYR_UPDATE_FALLBACK_URL:-}" ] && [ "$XRAYR_UPDATE_FALLBACK_URL" != "$XRAYR_UPDATE_URL" ]; then
    warn "默认更新地址下载失败，尝试备用地址：${XRAYR_UPDATE_FALLBACK_URL}"
    download_file "$XRAYR_UPDATE_FALLBACK_URL" "$dst"
    return
  fi
  return 1
}

remove_symlink_if_target() {
  local link="$1"
  if [ -L "$link" ] && [ "$(readlink "$link")" = "${INSTALL_DIR}/XrayR" ]; then
    rm -f "$link"
  fi
}

backup_config() {
  if [ -f "$CONFIG_FILE" ]; then
    cp -a "$CONFIG_FILE" "${CONFIG_FILE}.bak.$(date +%Y%m%d%H%M%S)"
    info "已备份旧配置。"
  fi
}

write_config() {
  backup_config
  umask 077
  cat > "$CONFIG_FILE" <<EOF
Log:
  Level: info
  AccessPath:
  ErrorPath:

DnsConfigPath:
RouteConfigPath:
InboundConfigPath:
OutboundConfigPath:

ConnectionConfig:
  Handshake: 4
  ConnIdle: 30
  UplinkOnly: 2
  DownlinkOnly: 4
  BufferSize: 64

Nodes:
  - PanelType: "NewV2board"
    ApiConfig:
      ApiHost: "${PANEL_URL}"
      ApiKey: "${API_KEY}"
      NodeID: ${NODE_ID}
      NodeType: ${NODE_TYPE}
      Timeout: 30
      MieruPort: ${MIERU_PORT}
      MieruTransport: "${MIERU_TRANSPORT}"
      MieruTrafficPattern: "${MIERU_TRAFFIC_PATTERN}"
    ControllerConfig:
      ListenIP: 0.0.0.0
      UpdatePeriodic: ${UPDATE_PERIODIC}
      DisableUploadTraffic: false
EOF
  chmod 600 "$CONFIG_FILE"
}

write_service() {
  cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=XrayR Mieru Service
After=network-online.target nss-lookup.target
Wants=network-online.target

[Service]
User=root
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/XrayR -c ${CONFIG_FILE}
Restart=on-failure
RestartSec=5s
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  if systemctl cat "$SERVICE_NAME" 2>/dev/null | grep -q -- '-config '; then
    fail "systemd 配置里仍然存在错误的 -config 参数，请检查 ${SERVICE_FILE} 或 systemd override。"
  fi
}

test_panel_config() {
  command -v curl >/dev/null 2>&1 || {
    warn "未找到 curl，跳过面板接口测试。"
    return
  }

  info "测试 Xboard UniProxy Mieru 配置接口。"
  local body code
  body="/tmp/xrayr-mieru-panel-config.$$"
  code="$(curl -k -sS -o "$body" -w "%{http_code}" \
    --get "${PANEL_URL}/api/v1/server/UniProxy/config" \
    --data-urlencode "node_type=mieru" \
    --data-urlencode "node_id=${NODE_ID}" \
    --data-urlencode "token=${API_KEY}" || true)"

  if [ "$code" = "200" ]; then
    info "面板 config 接口正常。"
  else
    warn "面板 config 接口返回 HTTP ${code}。XrayR 会尝试使用脚本写入的兜底端口和传输方式启动。"
    sed -n '1,80p' "$body" >&2 || true
  fi
  rm -f "$body"
}

start_service() {
  info "启动 XrayR。"
  systemctl enable --now "$SERVICE_NAME"
  sleep 2
  systemctl status "$SERVICE_NAME" --no-pager -l || true
}

install_or_reconfigure() {
  require_root
  require_systemd
  local binary
  binary="$(detect_binary)"
  read_install_inputs
  stop_old_services
  install_binary "$binary"
  write_config
  write_service
  test_panel_config
  start_service

  title "安装完成"
  echo "配置文件：${CONFIG_FILE}"
  echo "服务名称：${SERVICE_NAME}"
  echo "查看日志：journalctl -u ${SERVICE_NAME} -f"
  echo "查看监听：ss -lntup | grep XrayR"
}

show_status() {
  require_root
  systemctl status "$SERVICE_NAME" --no-pager -l || true
  ss -lntup | grep -E 'XrayR|:' || true
}

show_logs() {
  require_root
  journalctl -u "$SERVICE_NAME" -n 120 --no-pager
}

follow_logs() {
  require_root
  journalctl -u "$SERVICE_NAME" -f
}

show_config() {
  require_root
  [ -f "$CONFIG_FILE" ] || fail "配置文件不存在：${CONFIG_FILE}"
  sed -E 's/(ApiKey: ).*/\1"***"/' "$CONFIG_FILE"
}

restart_service() {
  require_root
  systemctl restart "$SERVICE_NAME"
  systemctl status "$SERVICE_NAME" --no-pager -l || true
}

update_binary() {
  require_root
  require_systemd
  [ -f "$SERVICE_FILE" ] || fail "XrayR systemd 服务不存在，请先 install。"

  title "Update XrayR Mieru"
  local tmp backup
  tmp="$(mktemp /tmp/XrayR-mieru-linux-amd64.XXXXXX)"
  trap 'rm -f "$tmp"' EXIT

  download_update_binary "$tmp"
  chmod 755 "$tmp"

  mkdir -p "$INSTALL_DIR"
  if [ -f "${INSTALL_DIR}/XrayR" ]; then
    backup="${INSTALL_DIR}/XrayR.bak.$(date +%Y%m%d%H%M%S)"
    cp -a "${INSTALL_DIR}/XrayR" "$backup"
    info "已备份旧二进制：${backup}"
  fi

  info "替换 XrayR 二进制并重启服务。"
  systemctl stop "$SERVICE_NAME" 2>/dev/null || true
  install -m 755 "$tmp" "${INSTALL_DIR}/XrayR"
  systemctl daemon-reload
  systemctl start "$SERVICE_NAME"
  sleep 2
  systemctl status "$SERVICE_NAME" --no-pager -l || true
}

uninstall_service() {
  require_root
  require_systemd

  local confirm="" keep_config=0 purge_source=0 arg
  for arg in "$@"; do
    case "$arg" in
      -y|--yes)
        confirm="yes"
        ;;
      --keep-config)
        keep_config=1
        ;;
      --purge-source|--remove-source)
        purge_source=1
        ;;
      *)
        fail "未知卸载参数：${arg}"
        ;;
    esac
  done
  [ "${UNINSTALL_KEEP_CONFIG:-}" = "1" ] && keep_config=1
  [ "${UNINSTALL_PURGE_SOURCE:-}" = "1" ] && purge_source=1

  title "卸载 XrayR Mieru"
  echo "将停止并移除 systemd 服务：${SERVICE_NAME}"
  echo "将删除安装目录：${INSTALL_DIR}"
  if [ "$keep_config" -eq 1 ]; then
    echo "将保留配置目录：${CONFIG_DIR}"
  else
    echo "将删除配置目录：${CONFIG_DIR}"
  fi
  if [ "$purge_source" -eq 1 ]; then
    echo "将额外删除源二进制：/root/XrayR-mieru-linux-amd64 /root/XrayR"
  fi

  if [ "$confirm" != "yes" ]; then
    read -r -p "确认卸载？输入 yes 继续: " confirm
  fi
  [ "$confirm" = "yes" ] || fail "已取消卸载。"

  systemctl stop "$SERVICE_NAME" 2>/dev/null || true
  systemctl disable "$SERVICE_NAME" 2>/dev/null || true
  rm -f "$SERVICE_FILE"
  systemctl daemon-reload
  systemctl reset-failed "$SERVICE_NAME" >/dev/null 2>&1 || true

  remove_symlink_if_target /usr/bin/XrayR
  remove_symlink_if_target /usr/local/bin/XrayR
  rm -rf "$INSTALL_DIR"
  if [ "$keep_config" -eq 1 ]; then
    info "已保留配置目录：${CONFIG_DIR}"
  else
    rm -rf "$CONFIG_DIR"
  fi
  if [ "$purge_source" -eq 1 ]; then
    rm -f /root/XrayR-mieru-linux-amd64 /root/XrayR
  fi
  info "卸载完成。"
}

main_menu() {
  while true; do
    title "XrayR Mieru Tool"
    cat <<EOF
1) Install / Reconfigure
2) Update binary from release
3) Status
4) Recent logs
5) Follow logs
6) Restart service
7) Show config
8) Uninstall
0) Exit
EOF
    local choice
    read -r -p "Select an option [0-8]: " choice
    case "$choice" in
      1) install_or_reconfigure ;;
      2) update_binary ;;
      3) show_status ;;
      4) show_logs ;;
      5) follow_logs ;;
      6) restart_service ;;
      7) show_config ;;
      8) uninstall_service ;;
      0|q|Q|exit) return ;;
      *) warn "Invalid option: ${choice}" ;;
    esac

    echo
    read -r -p "Press Enter to return to menu..." _
  done
}

usage() {
  cat <<EOF
用法：
  bash install_xrayr_mieru_from_root.sh             打开交互菜单
  bash install_xrayr_mieru_from_root.sh menu        打开交互菜单
  bash install_xrayr_mieru_from_root.sh install     安装或重装
  bash install_xrayr_mieru_from_root.sh status      查看状态
  bash install_xrayr_mieru_from_root.sh logs        查看最近日志
  bash install_xrayr_mieru_from_root.sh follow      实时跟踪日志
  bash install_xrayr_mieru_from_root.sh restart     重启服务
  bash install_xrayr_mieru_from_root.sh update      下载最新 XrayR 二进制并重启
  bash install_xrayr_mieru_from_root.sh config      查看配置（隐藏密钥）
  bash install_xrayr_mieru_from_root.sh uninstall   卸载
  bash install_xrayr_mieru_from_root.sh uninstall --keep-config   卸载但保留 /etc/XrayR
  bash install_xrayr_mieru_from_root.sh uninstall -y --purge-source  免确认并额外删除 /root 源二进制

安装前请把兼容 Mieru 的 XrayR 二进制放到：
  /root/XrayR-mieru-linux-amd64

也可以用环境变量指定：
  XRAYR_BIN=/root/XrayR-mieru-linux-amd64 bash install_xrayr_mieru_from_root.sh install
  XRAYR_UPDATE_URL=https://example.com/XrayR bash install_xrayr_mieru_from_root.sh update
  XRAYR_UPDATE_FALLBACK_URL=https://example.com/XrayR.bak bash install_xrayr_mieru_from_root.sh update
EOF
}

case "${1:-menu}" in
  menu) main_menu ;;
  install|reinstall|configure) install_or_reconfigure ;;
  status) show_status ;;
  logs) show_logs ;;
  follow) follow_logs ;;
  restart) restart_service ;;
  update) update_binary ;;
  config) show_config ;;
  uninstall) shift; uninstall_service "$@" ;;
  help|-h|--help) usage ;;
  *) usage; exit 1 ;;
esac
