# fnos-localsend

在**飞牛 fnOS** 上运行的 LocalSend 局域网文件互传应用，提供 **原生直装版**（不依赖 Docker）与 **Docker 版** 两种安装包（`.fpk`）。

> LocalSend 让手机 / 电脑 / NAS 在同一局域网内无需公网即可互传文件。
> 本应用基于 [`alexindigo/localsend-nas`](https://github.com/alexindigo/localsend-nas)（LocalSend 协议 v2.2 的 NAS 常驻节点，自带网页管理台），并做了二次开发（汉化 + 可改接收目录 + 可自定义端口）。

---

## ✨ 特性
- **原生直装版**：单个静态 Go 二进制，作为系统服务安装运行，**完全不需要 Docker**。
- **x86 与 ARM 双架构**：`x86_64` / `aarch64` 各提供独立安装包。
- **网页管理台已汉化**：文件 / 设备 / 传输、设置、接收弹窗等全部中文。
- **Web 界面可改接收目录**：设置面板直接填写绝对路径，收到的文件保存到你指定的 NAS 目录，飞牛【文件管理器】直接浏览，**无需重新打包**。
- **支持自定义 Web 端口**：安装向导或应用设置里修改端口（默认 8080），保存后重启应用生效。
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
├── build.sh          # 一键构建三个 fpk（从 src 本地编译）
├── LICENSE           # 许可证
├── src/              # 二次开发后的 localsend-nas Go 源码（汉化+接收目录+端口）
│   ├── go.mod
│   ├── cmd/localsend-nas/
│   ├── internal/     # settings/shares/httpapi/receive/config...
│   └── web/          # index.html + app.js（汉化，含接收目录表单）
├── native/           # 原生直装版工程
│   ├── manifest      # 应用元信息（appname/version/platform/...）
│   ├── cmd/          # 生命周期脚本（main 启停+状态；install/uninstall/upgrade...）
│   ├── wizard/       # 安装向导 + 应用设置配置（Web 端口输入）
│   ├── config/       # privilege（运行用户）+ resource（共享目录声明）
│   ├── app/
│   │   └── ui/       # 桌面入口（点击内嵌打开管理台）
│   └── ICON*.PNG     # 应用图标
└── docker/           # Docker 版工程（app/docker/docker-compose.yaml）
```

---

## 🔨 本地构建
前置：Linux + `go`（build.sh 用 `GOTOOLCHAIN=auto`，可自动拉取匹配版本）+ `curl` + `tar`；`fnpack` 会自动下载。

```bash
bash build.sh
```
产物：
```
localsend-native-x86-2.2.1.fpk
localsend-native-arm-2.2.1.fpk
localsend-docker-2.2.1.fpk
```

---

## 🚀 使用
1. 安装对应 `.fpk`（安装向导可自定义 Web 端口，默认 8080）。
2. 手机 / 电脑装 LocalSend 客户端，同局域网即可发现本 NAS。
3. 网页管理台：`http://NAS地址:端口`。
4. 修改接收目录：打开网页管理台 → 右上角【设置】→ 填写“接收目录”绝对路径 → 自动保存并生效。
5. 修改端口：飞牛应用中心 → LocalSend → 应用设置 → 配置，改端口后重启应用。

---

## ⚠️ 说明
- 原生版按架构区分包（`platform` 只能单一取值），`build.sh` 已自动处理。
- `src/` 为二次开发源码，若直接使用上游请替换回官方代码。

## 🤝 致谢
- [LocalSend](https://localsend.org) —— © Tien Do Nam, Apache-2.0
- [alexindigo/localsend-nas](https://github.com/alexindigo/localsend-nas) —— LGPL-3.0
- 飞牛 fnOS 应用打包文档：[developer.fnnas.com](https://developer.fnnas.com)
