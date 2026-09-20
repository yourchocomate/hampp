# Contributing to hampp

Thanks for helping! hampp is used by beginners on phones, so we value work that
makes something *just work* over new features. This guide covers setup, how the
code is organized, the rules the design depends on, and how to test.

## Development setup

You can work in three places, from quickest to most realistic.

### 1. Any machine with Go: unit tests

Requires Go ≥ 1.26 (see `go.mod`).

```sh
make test        # go test -race ./...
make lint        # gofmt + go vet
```

The unit tests run on macOS and Linux. They use fake runners and a fake `$PREFIX`,
so no Termux is needed.

### 2. Docker: the real Termux stack

The `Dockerfile` builds on [termux/termux-docker](https://github.com/termux/termux-docker),
which contains the real Termux userland and the real Termux packages
(apache2, php-fpm, mariadb, nginx, composer). Inside, hampp is built with Termux's
own Go, which is patched for Android DNS and CA lookups, so it's the same binary
a user gets.

```sh
make docker-dev      # docker build --target dev -t hampp-dev .
make e2e             # run test/e2e/smoke.sh against the whole stack
make shell           # interactive Termux shell, repo mounted at /src
```

Inside `make shell`:

```sh
bash /src/scripts/dev-build.sh   # rebuild hampp from your working tree (a few seconds)
hampp init --yes                 # or just: hampp   (wizard + dashboard)
hampp share on                   # make sites reachable from the host
```

Ports 8080 and 8443 are published. After `hampp share on`, open
`http://localhost:8080` or `http://<site>.localhost:8080` in your **desktop browser**.
Desktop browsers resolve `*.localhost` to your machine, and Docker forwards the port.

Notes:
- `termux/termux-docker:latest` is multi-arch, so the image runs natively on
  Apple Silicon and on x86_64.
- The container has no Android framework: `getprop`, `am`, `termux-open-url`,
  the storage provider and browsers are missing. Features that use them need a phone (below).
- Termux runs as a single non-root user. `pkg` refuses to run as root, and so does hampp.
  With `docker exec`, pass `-u 1000`.
- The ARM images sometimes need `--privileged` or `--security-opt seccomp:unconfined`
  (see the termux-docker README).

### 3. A phone: the real target

Install Termux from F-Droid or GitHub (not Google Play), then either:

```sh
pkg install golang git
git clone https://github.com/yourchocomate/hampp && cd hampp
bash scripts/dev-build.sh .      # builds with Termux's Go into $PREFIX/bin/hampp
bash test/e2e/smoke.sh
```

or copy a cross-compiled binary over with `adb push`. The checks that only a device
can do are listed in [docs/platform-notes.md](docs/platform-notes.md). If you tick one
off, update that file with the date, device, Android version and Termux source.

### Release builds (cross-compiling)

Binaries users download are cross-compiled with the Android NDK (`GOOS=android`,
cgo, clang for API 24). A static `GOOS=linux` build does **not** work on Android:
it has no DNS config or CA roots there.

```sh
make release-snapshot   # builds the `cross` image (NDK r29 + goreleaser), writes dist/
```

With a local NDK instead:
`ANDROID_NDK_BIN=$NDK/toolchains/llvm/prebuilt/<host>/bin goreleaser release --snapshot --clean`.
To check a `.deb`, run `apt install ./hampp_<ver>_<arch>.termux.deb` inside `make shell`.

## Project layout

```
cmd/hampp/            main
internal/cli/         cobra commands — thin wrappers over app
internal/tui/         bubbletea v2 dashboard + huh v2 setup wizard
internal/app/         the core: init, start/stop, sites, ssl, db, php, node, doctor
internal/service/     one file per daemon (apache/nginx, php-fpm, mariadb, code-server, mirror)
internal/render/      turns templates/ into config files
templates/            every config hampp writes (httpd.conf, nginx.conf, php.ini, my.cnf, …)
internal/termux/      environment detection and Android helpers (open-url, wakelock, Settings)
internal/pkgmgr/      apt (via pkg) / pacman
internal/pki/         local CA and site certificates
internal/site/        park + link site discovery on *.localhost
internal/node/        nvm-style switching between TUR nodejs-NN packages
internal/proc/        detached spawn, pidfiles, health checks
internal/config/      config.toml and credentials.toml
internal/sys/         command runner + fake for tests
test/e2e/smoke.sh     full-stack test, run inside Termux
docs/platform-notes.md  what has been verified, where, and what is still open
```

The CLI and the TUI call the same `app.App` methods. Put logic in `internal/app` or below,
never in `cli` or `tui`.

## Rules the design depends on

These come from what Termux and Android actually do. Please keep them:

1. **Never edit package-owned files.** Don't touch `$PREFIX/etc/apache2/httpd.conf`,
   `$PREFIX/etc/php/…` and the like. Render hampp's own file from `templates/` and point
   the daemon at it with a flag (`-f`, `-c`, `-y`, `--defaults-file`) or `PHP_INI_SCAN_DIR`.
2. **No `User`/`Group` directives.** Termux has a single user, and Android's seccomp blocks `setuid`.
3. **Mind the phantom-process budget.** Android 12+ allows 32 child processes across *all*
   apps. Keep daemons to one worker or on-demand.
4. **Bind to `127.0.0.1` by default.** Only `hampp share on` opens the web server. The
   database never listens beyond loopback.
5. **Keep state out of `$TMPDIR`.** Termux wipes it when the app restarts. Use `paths.Paths`
   (`~/.local/state/hampp`, …).
6. **Use `$PREFIX`-derived paths**, never a hard-coded `/data/data/com.termux/…`
   (the tests use a fake prefix).
7. **Every failure a user can hit ends with a fix.** Start commands wait for health checks,
   and on failure they show the log tail and the next step (`hampp doctor` style).
8. **Base Termux/Android facts on sources.** Link the termux-packages `build.sh`, the AOSP
   source or the issue in a comment or in `docs/platform-notes.md`. Mark anything
   unverified and add it to the device checklist.

## Testing

- **Unit tests.** Put them next to the code. Use `sys.Fake` to script external commands,
  and `newTestApp` in `internal/app` for a fake Termux prefix and home.
- **Templates.** `internal/render/render_test.go` checks each rendered config for required
  and forbidden directives. If you add a directive, add an assertion.
- **TUI.** `internal/tui/testdata/*.txt` are plain-text snapshots of each screen at 44 columns,
  in both Unicode and `--ascii` mode. After a deliberate UI change, run `make snapshots`
  and check the diff. Lines must fit in 44 cells, which is Termux in portrait.
- **End to end.** Run `make e2e` before sending anything that touches services, templates,
  sites, SSL, the database or PHP. CI runs the same script in `termux/termux-docker:x86_64`.

## Adding things

- **A service:** implement `service.Service` (and `Tester`/`Reloader` where the daemon
  supports it) in `internal/service`. Add its template in `templates/`, register it in
  `app.Core()` or `app.Extras()`, and add a step to `test/e2e/smoke.sh`.
- **A command:** add a thin cobra command in `internal/cli` that calls an `app` method.
  If it's a common action, give it a TUI key and add it to the help screen.
- **A doctor check:** add it in `internal/app/doctor.go`. Always fill in `Fix`.

## Pull requests

- Keep each PR focused. Run `make lint test` and, where relevant, `make e2e`.
- Say what you verified and where: unit, termux-docker, or a device (model, Android version, Termux source).
- Use plain commit messages that describe the change, e.g. `php: roll back extensions that fail to load`.
- By contributing you agree that your work is licensed under Apache-2.0.

## Releasing (maintainers)

1. Update `docs/platform-notes.md` if anything was re-verified.
2. Tag: `git tag v0.x.y && git push origin v0.x.y`.
3. The `release` workflow runs goreleaser with NDK r29. It publishes a **draft** release with
   four `.tar.gz` files, four `.termux.deb` files and `checksums.txt`. Review it, then publish.
4. `scripts/install.sh` always picks the latest published release.
