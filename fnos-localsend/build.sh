#!/bin/bash
# ============================================================
# 飞牛 fnOS - LocalSend 应用一键构建脚本
# 生成: native-x86 / native-arm / docker 三个 fpk
# 版本: 1.0.4（WebUI 汉化 + 可改接收目录 + 固定端口 4060）
# 用法: bash build.sh
# 前置: go(GOTOOLCHAIN=auto 可自动拉取) / curl / tar；
#      如无 fnpack 会自动下载
# ============================================================
set -e
cd "$(dirname "$0")"

APP_VERSION="1.0.4"
FNPCK_URL="https://static2.fnnas.com/fnpack/fnpack-1.2.3-linux-amd64"
GO_MIN="go1.21"

# --- 检查 go 工具链（src/go.mod 要求 go 1.26.5，用 GOTOOLCHAIN=auto 自动拉取）
if ! command -v go >/dev/null 2>&1; then
  echo "错误: 未检测到 Go 工具链。请先安装 Go(>=1.21)，或设置 GOTOOLCHAIN=auto。"
  exit 1
fi
export GOTOOLCHAIN=auto

echo "==> [1/5] 准备 fnpack ..."
if [ ! -x ./fnpack ]; then
  curl -sL -o fnpack "$FNPCK_URL" && chmod +x fnpack
fi

echo "==> [2/5] 从 src/ 本地编译 localsend-nas (amd64 + arm64) ..."
mkdir -p .tmp
(cd src && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ../.tmp/localsend-x86 ./cmd/localsend-nas)
(cd src && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o ../.tmp/localsend-arm ./cmd/localsend-nas)

echo "==> [3/5] 构建 原生 x86 版 ..."
cp .tmp/localsend-x86 native/app/localsend-nas
chmod +x native/app/localsend-nas
sed -i 's/^platform=.*/platform=x86/' native/manifest
./fnpack build --directory ./native
mv -f localsend.fpk "localsend-native-x86-${APP_VERSION}.fpk"

echo "==> [4/5] 构建 原生 arm 版 ..."
cp .tmp/localsend-arm native/app/localsend-nas
chmod +x native/app/localsend-nas
sed -i 's/^platform=.*/platform=arm/' native/manifest
./fnpack build --directory ./native
mv -f localsend.fpk "localsend-native-arm-${APP_VERSION}.fpk"
sed -i 's/^platform=.*/platform=x86/' native/manifest

echo "==> [5/5] 构建 docker 版 ..."
./fnpack build --directory ./docker
mv -f localsend.fpk "localsend-docker-${APP_VERSION}.fpk"

echo
echo "全部构建完成:"
ls -la localsend-*.fpk
