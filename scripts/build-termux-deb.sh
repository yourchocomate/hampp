#!/usr/bin/env bash
# Build a Termux .deb.
#
# Termux packages use xz for both the control and data members. nFPM always
# writes control.tar.gz, and some Termux installs cannot read that ("could not
# locate member control.tar{.xz,.lzma,}"), so hampp builds its packages here
# with dpkg-deb --uniform-compression -Zxz instead.
#
#   scripts/build-termux-deb.sh <binary> <termux-arch> <version> <output.deb>
#   e.g. scripts/build-termux-deb.sh dist/hampp-aarch64 aarch64 0.1.1 dist/hampp_0.1.1_aarch64.termux.deb
set -euo pipefail

BIN=${1:?binary}
ARCH=${2:?arch: aarch64|arm|i686|x86_64}
VERSION=${3:?version without the leading v}
OUT=${4:?output .deb}

PREFIX_DIR=data/data/com.termux/files/usr
ROOT=$(mktemp -d)
trap 'rm -rf "$ROOT"' EXIT

install -Dm755 "$BIN" "$ROOT/$PREFIX_DIR/bin/hampp"
install -Dm644 LICENSE "$ROOT/$PREFIX_DIR/share/doc/hampp/LICENSE"
install -Dm644 README.md "$ROOT/$PREFIX_DIR/share/doc/hampp/README.md"

mkdir -p "$ROOT/DEBIAN"
cat >"$ROOT/DEBIAN/control" <<EOF
Package: hampp
Version: $VERSION
Architecture: $ARCH
Maintainer: Md Habibur Rahman <yourchocomate@gmail.com>
Installed-Size: $(du -ks "$ROOT" | cut -f1)
Homepage: https://github.com/yourchocomate/hampp
Section: web
Priority: optional
Description: Local PHP web server stack for Termux
 Sets up and runs Apache or nginx, PHP-FPM and MariaDB or SQLite inside Termux,
 with sites on *.localhost, local HTTPS, PHP extension management and a
 phone-sized dashboard.
EOF

mkdir -p "$(dirname "$OUT")"
dpkg-deb --build --root-owner-group --uniform-compression -Zxz "$ROOT" "$OUT" >/dev/null
echo "$OUT"
# Show the members when ar is available (not installed in Termux by default).
command -v ar >/dev/null 2>&1 && ar t "$OUT" || true
