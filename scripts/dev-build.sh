#!/data/data/com.termux/files/usr/bin/bash
# Rebuild hampp from a bind-mounted checkout inside the dev container (or on a
# phone with `pkg install golang`) and install it as $PREFIX/bin/hampp.
#   docker run -it --rm -v "$PWD:/src" hampp-dev
#   bash /src/scripts/dev-build.sh
set -euo pipefail
SRC=${1:-/src}
cd "$SRC"
go build -trimpath -buildvcs=false \
	-ldflags "-X github.com/yourchocomate/hampp/internal/cli.Version=dev-$(date +%H%M%S)" \
	-o "$PREFIX/bin/hampp" ./cmd/hampp
hampp version
