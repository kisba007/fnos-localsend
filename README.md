# fnos-localsend
LocalSend 局域网文件互传，飞牛fnOS原生应用，免Docker

在飞牛NAS上运行 LocalSend 服务，手机、电脑、NAS 在同一局域网内，无需公网即可互相传输文件。

## ✨ 特性
- ✅ 原生直装版本，静态二进制，**不依赖Docker**
- ✅ 飞牛桌面内嵌窗口打开，可拖动、缩放、最小化，不再新开网页
- ✅ 自动创建并授权接收目录，文件管理器直接浏览收到的文件
- ✅ 局域网UDP组播设备发现，离线可用，端口53317
- ✅ 同时支持 x86_64 和 aarch64 双架构
- ✅ 发布者链接指向本GitHub仓库

## 📦 安装
1. 前往 [Releases](https://github.com/kisba007/fnos-localsend/releases) 下载对应架构 `.fpk` 安装包
2. 打开飞牛【应用中心】→【手动安装】，上传fpk文件完成安装
3. 桌面点击LocalSend图标，弹出内嵌窗口使用

> 访问地址：`http://NASIP:8080`

## 🔨 本地构建
```bash
bash build.sh
