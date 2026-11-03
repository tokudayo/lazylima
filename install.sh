#!/bin/sh
set -eu

[ "$(uname -s)" = Darwin ] || { echo 'lazylima needs macOS.' >&2; exit 1; }
case "$(uname -m)" in
  arm64) arch=arm64 ;;
  x86_64) arch=amd64 ;;
  *) echo 'unsupported Mac architecture.' >&2; exit 1 ;;
esac

version=${VERSION:-latest}
install_dir=${INSTALL_DIR:-/usr/local/bin}
asset="lazylima-darwin-$arch"
base=https://github.com/tokudayo/lazylima/releases
if [ "$version" = latest ]; then
  url="$base/latest/download"
else
  url="$base/download/$version"
fi

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' 0
trap 'exit 1' HUP INT TERM

echo "grabbing lazylima ($version, $arch)…"
curl -fsSL "$url/$asset" -o "$tmp_dir/$asset"
curl -fsSL "$url/checksums.txt" -o "$tmp_dir/checksums.txt"
awk -v asset="$asset" '$2 == asset' "$tmp_dir/checksums.txt" > "$tmp_dir/checksum"
[ -s "$tmp_dir/checksum" ] || { echo 'missing checksum.' >&2; exit 1; }
(cd "$tmp_dir" && shasum -a 256 -c checksum)

if mkdir -p "$install_dir" 2>/dev/null && [ -w "$install_dir" ]; then
  install -m 755 "$tmp_dir/$asset" "$install_dir/lazylima"
else
  sudo mkdir -p "$install_dir"
  sudo install -m 755 "$tmp_dir/$asset" "$install_dir/lazylima"
fi

echo "installed to $install_dir/lazylima. time to sit back."
