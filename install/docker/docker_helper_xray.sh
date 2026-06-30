#!/bin/sh
set -ex
# Versions are injected as environment variables by the Dockerfile (build args).
: "${V2RAYA_VERSION:?V2RAYA_VERSION is required}"
: "${XRAY_VERSION:?XRAY_VERSION is required}"
current_dir=$(pwd)
case "$(arch)" in
    x86_64)
        v2raya_arch="x64"
        xray_arch="64"
        ;;
    armv7l)
        v2raya_arch="armv7"
        xray_arch="arm32-v7a"
        ;;
    aarch64)
        v2raya_arch="arm64"
        xray_arch="arm64-v8a"
        ;;
    riscv64)
        v2raya_arch="riscv64"
        xray_arch="riscv64"
        ;;
    *)
        echo "unsupported architecture: $(arch)" >&2
        exit 1
        ;;
esac
apk add --no-cache unzip
mkdir -p build && cd build || exit
# v2rayA service + merged v2raya_core (default, fully-featured backend)
wget https://github.com/v2rayA/v2rayA/releases/download/v"$V2RAYA_VERSION"/v2raya_linux_"$v2raya_arch"_"$V2RAYA_VERSION"
wget https://github.com/v2rayA/v2rayA/releases/download/v"$V2RAYA_VERSION"/v2raya_core_linux_"$v2raya_arch"_"$V2RAYA_VERSION"
install ./v2raya_linux_"$v2raya_arch"_"$V2RAYA_VERSION" /usr/bin/v2raya
install ./v2raya_core_linux_"$v2raya_arch"_"$V2RAYA_VERSION" /usr/bin/v2raya_core
# Official Xray-core binary (optional backend, opt-in via V2RAYA_V2RAY_BIN=/usr/bin/xray)
wget -O xray.zip https://github.com/XTLS/Xray-core/releases/download/v"$XRAY_VERSION"/Xray-linux-"$xray_arch".zip
mkdir -p xray && unzip -o xray.zip -d xray
install ./xray/xray /usr/bin/xray
mkdir /usr/share/v2raya
wget -O /usr/share/v2raya/LoyalsoldierSite.dat https://raw.githubusercontent.com/mzz2017/dist-v2ray-rules-dat/master/geosite.dat
wget -O /usr/share/v2raya/geosite.dat https://raw.githubusercontent.com/mzz2017/dist-v2ray-rules-dat/master/geosite.dat
wget -O /usr/share/v2raya/geoip.dat https://raw.githubusercontent.com/mzz2017/dist-v2ray-rules-dat/master/geoip.dat
cd "$current_dir" || exit
rm -rf build
apk add --no-cache iptables iptables-legacy nftables tzdata
install ./iptables.sh /usr/local/bin/iptables
install ./ip6tables.sh /usr/local/bin/ip6tables
install ./iptables.sh /usr/local/bin/iptables-nft
install ./ip6tables.sh /usr/local/bin/ip6tables-nft
install ./iptables.sh /usr/local/bin/iptables-legacy
install ./ip6tables.sh /usr/local/bin/ip6tables-legacy
