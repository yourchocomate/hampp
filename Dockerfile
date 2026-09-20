# syntax=docker/dockerfile:1
# check=skip=FromPlatformFlagConstDisallowed
#
# hampp development images.
#
#   dev    (default) Termux userland (termux/termux-docker) with the real Termux
#          packages hampp manages. hampp is built with Termux's own Go, which
#          is patched for Android DNS/CA lookups — the same toolchain users get
#          with `pkg install golang`. No Android NDK needed.
#
#   cross  Linux + Android NDK r29 + goreleaser, for building the release
#          binaries and .deb packages exactly like CI does.
#
# Quick start (see CONTRIBUTING.md):
#   docker build -t hampp-dev .
#   docker run -it --rm -p 8080:8080 -p 8443:8443 hampp-dev
#   docker run --rm hampp-dev bash test/e2e/smoke.sh

# ---------------------------------------------------------------------------
# dev: Termux userland. `latest` is multi-arch (arm64, arm/v7, 386, amd64), so
# this runs natively on Apple Silicon and x86_64 hosts.
FROM termux/termux-docker:latest AS dev

# The image's entrypoint drops root to Termux's single "system" user. RUN steps
# bypass the entrypoint, and pkg refuses to run as root, so route them through it.
SHELL ["/entrypoint.sh", "bash", "-c"]

# golang + git to build; the rest is what `hampp init` would install, baked in
# so containers start fast. termux-tools/procps give termux-open-url/ps/netstat
# equivalents used by hampp and the smoke test.
RUN pkg install -y golang git \
        apache2 nginx php php-fpm mariadb composer openssl-tool rsync curl \
        procps net-tools \
    && apt clean

WORKDIR /data/data/com.termux/files/home/hampp

# Module downloads get their own layer so source edits rebuild quickly.
COPY --chown=1000:1000 go.mod go.sum ./
RUN go mod download

COPY --chown=1000:1000 . .
RUN go build -trimpath -buildvcs=false \
        -ldflags "-X github.com/yourchocomate/hampp/internal/cli.Version=dev" \
        -o "$PREFIX/bin/hampp" ./cmd/hampp \
    && hampp version

# `hampp share on` inside the container makes these reachable from the host,
# e.g. http://blog.localhost:8080 in your desktop browser.
EXPOSE 8080 8443

# The base image's ENTRYPOINT (/entrypoint.sh) and CMD (login) are kept: you get
# an interactive Termux login shell as the non-root user.

# ---------------------------------------------------------------------------
# cross: release builds. Google ships the NDK for x86_64 Linux hosts only, so
# this stage is pinned to linux/amd64 (Docker Desktop emulates it on Apple Silicon).
FROM --platform=linux/amd64 golang:1.27-bookworm AS cross

ARG NDK_VERSION=r29
# SHA-1 from https://dl.google.com/android/repository/repository2-3.xml
ARG NDK_SHA1=87e2bb7e9be5d6a1c6cdf5ec40dd4e0c6d07c30b
RUN apt-get update && apt-get install -y --no-install-recommends unzip file \
    && rm -rf /var/lib/apt/lists/* \
    && curl -fsSLo /tmp/ndk.zip "https://dl.google.com/android/repository/android-ndk-${NDK_VERSION}-linux.zip" \
    && echo "${NDK_SHA1}  /tmp/ndk.zip" | sha1sum -c - \
    && unzip -q /tmp/ndk.zip -d /opt \
    && rm /tmp/ndk.zip
ENV ANDROID_NDK_BIN=/opt/android-ndk-${NDK_VERSION}/toolchains/llvm/prebuilt/linux-x86_64/bin

# Pinned for reproducible releases; goreleaser v2.18 needs Go >= 1.27.1.
ARG GORELEASER_VERSION=v2.18.2
RUN go install github.com/goreleaser/goreleaser/v2@${GORELEASER_VERSION}

WORKDIR /src
# Mount the repository at /src, then e.g.:
#   goreleaser release --snapshot --clean
CMD ["bash"]
