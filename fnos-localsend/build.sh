#!/bin/bash
# ============================================================
# 飞牛 fnOS - LocalSend 应用一键构建脚本
# 生成: native-x86 / native-arm / docker 三个 fpk
# 用法: bash build.sh
# 前置: curl / tar（飞牛与常见Linux自带）；如无 fnpack 会自动下载
# ============================================================
set -e
cd "$(dirname "$0")"

APP_VERSION="2.1.0"
NAS_REPO="alexindigo/localsend-nas"
NAS_TAG="v0.2.2"
FNPCK_URL="https://static2.fnnas.com/fnpack/fnpack-1.2.3-linux-amd64"

echo "==> [1/5] 准备 fnpack ..."
if [ ! -x ./fnpack ]; then
  curl -sL -o fnpack "$FNPCK_URL" && chmod +x fnpack
fi

echo "==> [2/5] 准备 localsend-nas 二进制 (amd64 + arm64) ..."
mkdir -p .tmp
NAS_VER="${NAS_TAG#v}"
[ -f .tmp/nas-amd64.tar.gz ] || curl -sL -o .tmp/nas-amd64.tar.gz \
  "https://github.com/${NAS_REPO}/releases/download/${NAS_TAG}/localsend-nas_${NAS_VER}_linux_amd64.tar.gz"
[ -f .tmp/nas-arm64.tar.gz ] || curl -sL -o .tmp/nas-arm64.tar.gz \
  "https://github.com/${NAS_REPO}/releases/download/${NAS_TAG}/localsend-nas_${NAS_VER}_linux_arm64.tar.gz"
tar xzf .tmp/nas-amd64.tar.gz -C .tmp
tar xzf .tmp/nas-arm64.tar.gz -C .tmp

echo "==> [3/5] 构建 原生 x86 版 ..."
cp .tmp/localsend-nas_${NAS_VER}_linux_amd64/localsend-nas native/app/localsend-nas
chmod +x native/app/localsend-nas
sed -i 's/^platform=.*/platform=x86/' native/manifest
./fnpack build --directory ./native
mv -f localsend.fpk "localsend-native-x86-${APP_VERSION}.fpk"

echo "==> [4/5] 构建 原生 arm 版 ..."
cp .tmp/localsend-nas_${NAS_VER}_linux_arm64/localsend-nas native/app/localsend-nas
chmod +x native/app/localsend-nas
sed -i 's/^platform=.*/platform=arm/' native/manifest
./fnpack build --directory ./native
mv -f localsend.fpk "localsend-native-arm-${APP_VERSION}.fpk"

echo "==> [5/5] 构建 docker 版 ..."
./fnpack build --directory ./docker
mv -f localsend.fpk "localsend-docker-${APP_VERSION}.fpk"

echo
echo "全部构建完成:"
ls -la localsend-*.fpk
