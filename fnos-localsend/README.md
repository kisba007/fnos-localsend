# fnos-localsend

在**飞牛 fnOS** 上运行的 LocalSend 局域网文件互传应用，提供 **原生直装版**（不依赖 Docker）与 **Docker 版** 两种安装包（`.fpk`）。

> LocalSend 让手机 / 电脑 / NAS 在同一局域网内无需公网即可互传文件。
> 本应用基于 [`alexindigo/localsend-nas`](https://github.com/alexindigo/localsend-nas)（LocalSend 协议 v2.2 的 NAS 常驻节点，自带网页管理台）。

---

## ✨ 特性
- **原生直装版**：单个静态 Go 二进制，作为系统服务安装运行，**完全不需要 Docker**。
- **x86 与 ARM 双架构**：`x86_64` / `aarch64` 各提供独立安装包。
- **网页管理台**：浏览器打开 `http://NAS地址:8080`，可浏览文件、打包发送、接收确认、自动入盒（dropbox）。
- **共享接收目录**：安装时飞牛自动创建并授权（Windows ACL），在【文件管理器】中可直接浏览收到的文件。
- **组播发现**：采用 host 网络 / UDP 53317，局域网设备可自动发现本 NAS。

---

## 📦 安装包（Build / Release 产物）
在应用中心 → 手动安装应用 → 上传对应 `.fpk` 即可。

| 文件 | 适用 | 架构 |
|---|---|---|
| `localsend-native-x86-<ver>.fpk` | Intel/AMD 设备（如绿联 DXP4800） | x86_64 |
| `localsend-native-arm-<ver>.fpk` | ARM 设备（飞腾/瑞芯微等） | aarch64 |
| `localsend-docker-<ver>.fpk` | 偏好容器运行的设备 | amd64+arm64 |

---

## 🗂 仓库结构
```
fnos-localsend/
├── build.sh          # 一键构建三个 fpk
├── LICENSE           # 许可证（打包源码 MIT；运行组件 LGPL-3.0）
├── native/           # 原生直装版工程
│   ├── manifest      # 应用元信息（appname/version/platform/...）
│   ├── cmd/          # 生命周期脚本（main 启停+状态；install/uninstall/upgrade...）
│   ├── config/       # privilege（运行用户）+ resource（共享目录声明）
│   ├── wizard/       # 安装向导
│   ├── app/
│   │   ├── localsend-nas   # 静态二进制（由 build.sh 拉取）
│   │   └── ui/            # 桌面入口（点击打开 8080 管理台）
│   └── ICON*.PNG     # 应用图标
└── docker/           # Docker 版工程（app/docker/docker-compose.yaml）
```

### 原生版 manifest 要点
- `platform`：`x86` / `arm`（构建脚本自动切换）
- `service_port=8080` + `desktop_uidir=ui` → 桌面图标直接打开网页管理台
- `config/resource` 声明 `data-share: localsend` → 自动创建可浏览、已授权的接收目录
- `cmd/main` 以 `$TRIM_DATA_SHARE_PATHS` 定位接收目录

---

## 🔨 本地构建
前置：Linux + `curl` + `tar`（build.sh 会自动下载官方 `fnpack` 与二进制）。

```bash
bash build.sh
```
产物：
```
localsend-native-x86-2.1.0.fpk
localsend-native-arm-2.1.0.fpk
localsend-docker-2.1.0.fpk
```

> 构建需要官方 `fnpack`（飞牛应用开放平台 CLI）。版本以 `developer.fnnas.com/docs/cli/fnpack` 为准。

---

## 🚀 使用
1. 安装对应 `.fpk` 后，手机 / 电脑装 LocalSend 客户端。
2. 同局域网即可在客户端里看到本 NAS（别名 `fnOS-NAS`）。
3. 网页管理台：`http://NAS地址:8080`。
4. 收到的文件在飞牛【文件管理器】的应用共享目录中（`data-share: localsend`）。

---

## ⚠️ 说明
- 原生版按架构区分包（`platform` 只能单一取值），`build.sh` 已自动处理。
- 若需把接收目录固定到自定义路径，可在应用设置-编排/脚本中调整（需考虑 ACL 权限）。
- 本应用由社区打包，如遇问题请优先查看 `cmd/main` 与 `TRIM_TEMP_LOGFILE` 日志；勿向飞牛官方反馈第三方应用问题。

## 🤝 致谢
- [LocalSend](https://localsend.org) —— © Tien Do Nam, Apache-2.0
- [alexindigo/localsend-nas](https://github.com/alexindigo/localsend-nas) —— LGPL-3.0
- 飞牛 fnOS 应用打包文档：[developer.fnnas.com](https://developer.fnnas.com)
