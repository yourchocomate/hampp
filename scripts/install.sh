#!/data/data/com.termux/files/usr/bin/sh
# hampp installer for Termux.
#   curl -fsSL https://raw.githubusercontent.com/yourchocomate/hampp/main/scripts/install.sh | sh
# Installs the release .deb with apt (so `apt remove hampp` uninstalls it), or
# the plain binary on pacman-based Termux. Set HAMPP_VERSION=v0.1.0 to pin, or
# HAMPP_DOWNLOAD_URL to a mirror that hosts the release files (with HAMPP_VERSION).
set -eu

REPO="yourchocomate/hampp"
say() { printf '%s\n' "$*"; }
die() { printf 'hampp install: %s\n' "$*" >&2; exit 1; }

[ -n "${PREFIX:-}" ] && [ -d "$PREFIX/bin" ] && case "$PREFIX" in *com.termux*) true ;; *) false ;; esac ||
	die "run this inside Termux (install Termux from F-Droid or GitHub)"
[ "$(id -u)" != 0 ] || die "do not run as root (su/tsu); run as the normal Termux user"

case "$(uname -m)" in
aarch64 | arm64) ARCH=aarch64 ;;
armv7l | armv8l | arm) ARCH=arm ;;
x86_64) ARCH=x86_64 ;;
i686 | i386) ARCH=i686 ;;
*) die "unsupported CPU: $(uname -m)" ;;
esac

command -v curl >/dev/null 2>&1 || pkg install -y curl

TAG="${HAMPP_VERSION:-}"
if [ -z "$TAG" ]; then
	# The API is authoritative and needs no token for public repos.
	TAG=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null |
		sed -n 's/.*"tag_name"[ :]*"\([^"]*\)".*/\1/p' | head -1)
fi
if [ -z "$TAG" ]; then
	# Fallback: /releases/latest redirects to /releases/tag/<tag>.
	TAG=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" | sed 's#.*/tag/##')
fi
case "$TAG" in v*) ;; *) die "could not find the latest release (got '$TAG')" ;; esac
VER=${TAG#v}
BASE="${HAMPP_DOWNLOAD_URL:-https://github.com/$REPO/releases/download/$TAG}"
TMP=$(mktemp -d "${TMPDIR:-$PREFIX/tmp}/hampp.XXXXXX")
trap 'rm -rf "$TMP"' EXIT

say "Downloading hampp $TAG for $ARCH…"
curl -fsSL -o "$TMP/checksums.txt" "$BASE/checksums.txt"

verify() {
	want=$(grep " $1\$" "$TMP/checksums.txt" | cut -d' ' -f1)
	[ -n "$want" ] || die "$1 is not listed in checksums.txt"
	got=$(sha256sum "$TMP/$1" | cut -d' ' -f1)
	[ "$want" = "$got" ] || die "checksum mismatch for $1"
}

# HamppServer v1 installed a Python launcher at the same path; remove it so it
# cannot shadow v2. `hampp doctor --fix` cleans up the rest.
if [ -f "$PREFIX/bin/hampp" ] && grep -q 'HamppServer.py' "$PREFIX/bin/hampp" 2>/dev/null; then
	say "Removing the old HamppServer v1 launcher…"
	rm -f "$PREFIX/bin/hampp"
fi

install_binary() {
	TGZ="hampp_${VER}_${ARCH}.tar.gz"
	curl -fsSL -o "$TMP/$TGZ" "$BASE/$TGZ"
	verify "$TGZ"
	tar -xzf "$TMP/$TGZ" -C "$TMP" hampp
	install -m 755 "$TMP/hampp" "$PREFIX/bin/hampp"
}

if command -v apt >/dev/null 2>&1; then
	DEB="hampp_${VER}_${ARCH}.termux.deb"
	curl -fsSL -o "$TMP/$DEB" "$BASE/$DEB"
	verify "$DEB"
	# Falling back keeps the install working even if this Termux build rejects
	# the package format; only `apt remove hampp` is then unavailable.
	if ! apt install -y "$TMP/$DEB"; then
		say ""
		say "apt could not install the package (see above); installing the plain binary instead."
		install_binary
	fi
else
	install_binary
fi

say ""
say "hampp $(hampp version | cut -d' ' -f2) installed."
# Only hand over to the interactive setup when a terminal can really be opened
# (with `curl | sh`, stdin is the pipe; in CI there is no terminal at all).
if (exec </dev/tty >/dev/tty) 2>/dev/null; then
	say "Starting setup…"
	exec hampp init </dev/tty >/dev/tty 2>&1
fi
say "Next: hampp init"
