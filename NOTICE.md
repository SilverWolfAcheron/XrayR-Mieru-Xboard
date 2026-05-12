# NOTICE

本项目基于 XrayR 修改，用于增加 Xboard UniProxy Mieru 节点兼容能力。

上游项目：

- XrayR: https://github.com/XrayR-project/XrayR
- License: Mozilla Public License Version 2.0

主要新增或修改内容：

- 新增 `xrayr/service/mieru/`，实现 Mieru 服务端控制器。
- 修改 XrayR 面板启动逻辑，让 `NodeType: Mieru` 使用独立 Mieru 服务。
- 修改 NewV2board/Xboard API 解析逻辑，支持 Mieru 节点配置、用户拉取、流量上报和在线上报。
- 新增中文安装脚本 `scripts/install_xrayr_mieru_from_root.sh`。

本仓库不应包含真实面板地址、server_token、用户信息或服务器 IP。
