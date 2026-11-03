#!/bin/sh
set -eu

install_dir=${INSTALL_DIR:-/usr/local/bin}
bin="$install_dir/lazylima"
if [ ! -e "$bin" ] && found=$(command -v lazylima 2>/dev/null); then
  bin=$found
fi
prefs_dir="$HOME/Library/Application Support/lazylima"

if [ -e "$bin" ]; then
  if [ -w "$(dirname "$bin")" ]; then
    rm -f "$bin"
  else
    sudo rm -f "$bin"
  fi
  echo "removed $bin."
else
  echo 'lazylima is not installed. nothing to do.'
fi

if [ -d "$prefs_dir" ]; then
  if [ "${PURGE:-0}" = 1 ]; then
    rm -rf "$prefs_dir"
    echo "removed preferences in $prefs_dir."
  else
    echo "kept preferences in $prefs_dir (rerun with PURGE=1 to remove them)."
  fi
fi

echo 'bye for now.'
