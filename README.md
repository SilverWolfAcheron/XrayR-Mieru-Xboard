# XrayR Mieru for Xboard

这是一个 XrayR 修改版，用于对接 Xboard UniProxy 的 `mieru` 节点。

仓库结构：

```text
xrayr-mieru-xboard-open/
├── scripts/
│   └── install_xrayr_mieru_from_root.sh
├── xrayr/
│   └── 修改后的 XrayR 源码
├── docs/
├── LICENSE
├── NOTICE.md
└── README.md
```

## 功能

- 支持 Xboard `UniProxy` 接口。
- 支持 `NodeType: Mieru`。
- 从 Xboard 拉取节点配置和用户列表。
- 上报节点状态、在线用户和用户流量。
- Mieru 服务端支持 TCP CONNECT 和 UDP ASSOCIATE。
- 兼容 FlClash/Mihomo 常见 Mieru 客户端，不强制 `user hint`。

## 编译

```bash
cd xrayr
export GOTOOLCHAIN=local
export GOPROXY=https://goproxy.cn,direct
go build ./...
go test -vet=off ./... -run TestNonExistent
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o build/XrayR-mieru-linux-amd64 .
```

编译完成后把二进制上传到服务器：

```bash
/root/XrayR-mieru-linux-amd64
```

## 安装

```bash
bash scripts/install_xrayr_mieru_from_root.sh install
```

脚本不会内置面板地址、节点 ID 或 `server_token`。安装时需要输入：

- Xboard 面板地址
- Xboard `server_token`
- Xboard Mieru 节点 ID
- Mieru 监听端口
- Mieru 传输方式：`TCP` 或 `UDP`
- 可选 `traffic_pattern`

常用命令：

```bash
bash scripts/install_xrayr_mieru_from_root.sh status
bash scripts/install_xrayr_mieru_from_root.sh logs
bash scripts/install_xrayr_mieru_from_root.sh follow
bash scripts/install_xrayr_mieru_from_root.sh restart
bash scripts/install_xrayr_mieru_from_root.sh config
bash scripts/install_xrayr_mieru_from_root.sh uninstall
```

## 配置示例

```yaml
Nodes:
  - PanelType: "NewV2board"
    ApiConfig:
      ApiHost: "https://example.com"
      ApiKey: "server_token"
      NodeID: 2
      NodeType: Mieru
      Timeout: 30
      MieruPort: 25566
      MieruTransport: "TCP"
      MieruTrafficPattern: ""
    ControllerConfig:
      ListenIP: 0.0.0.0
      UpdatePeriodic: 60
      DisableUploadTraffic: false
```

`MieruPort` 和 `MieruTransport` 是兜底配置。当 Xboard 的 Mieru config 接口异常时，XrayR 会使用本地配置启动。

## 排错

```bash
systemctl status XrayR --no-pager -l
journalctl -u XrayR -f
ss -lntup | grep XrayR
```

如果客户端 timeout，但 `tcpdump` 能看到客户端包到达服务端端口，通常不是防火墙问题，而是 Mieru 握手、用户名密码、传输方式或目标出口连接失败。当前版本会在 XrayR 日志里输出连接失败原因。

## 开源发布检查

发布到 GitHub 前建议执行：

```bash
rg -n "server_token|ApiKey|token|https://你的面板|你的密钥|你的服务器IP" .
```

不要提交：

- 真实面板地址
- `server_token`
- 用户 UUID
- 服务器 IP
- 编译产物
- 日志文件

## 许可证

XrayR 原项目使用 Mozilla Public License 2.0。本仓库保留 MPL-2.0 许可证，详见 `LICENSE` 和 `NOTICE.md`。
