# Platform notes and verification status

These results come from running hampp against the real Termux userland, and they
feed back into the design. Check them again when Termux or its packages change a lot.

## Verified in `termux/termux-docker:aarch64` (2026-09-20)

Environment: Termux packages from `packages-cf.termux.dev` (apache2 2.4.68, php 8.5.1, nginx 1.31,
mariadb 13.0, composer 2.10.3), run as the non-root Termux user (uid 1000). The hampp
binary was cross-compiled on macOS with NDK r29 (`GOOS=android`, cgo, API 24).

| Check | Result |
|---|---|
| NDK-built android binary runs; DNS and HTTPS from Go (Adminer download) | ✅ once Termux's `$PREFIX/etc/tls/cert.pem` is added to the Go cert pool (upstream Go only reads `/system/etc/security/cacerts`) |
| `httpd -f` with mpm_worker, proxy_fcgi to the php-fpm socket, mod_ssl, mod_http2 | ✅ |
| php-fpm without `user`/`group`, `-D`, and `-t` using only hampp's pool | ✅ |
| MariaDB postinst creates a passwordless root; `db setup` secures it | ✅ root now needs a password; hostname-based root accounts are dropped (under `skip-name-resolve` they are ignored and `ALTER USER` fails on them) |
| PHP↔MariaDB socket mismatch (`$PREFIX/tmp` vs `$PREFIX/var/run`) | ✅ fixed for php-fpm and the CLI; PDO to `localhost` works |
| Parked sites, `.htaccess` rewrites, dotfiles denied, front controller (nginx `try_files`) | ✅ |
| HTTPS with the local CA; `curl --cacert bundle.pem` | ✅ |
| nginx server_names_hash | ✅ needs `server_names_hash_bucket_size 128` (Termux's build uses 32-byte buckets) |
| Adminer 6.1.0: MariaDB login; SQLite login through `Adminer\Password` | ✅ correct password opens the DB; wrong or empty passwords are rejected |
| `share on` binds the web server to 0.0.0.0; MariaDB stays on 127.0.0.1 | ✅ |
| `stop` leaves no processes; re-running `init` is safe; `uninstall --purge` keeps `~/www` | ✅ |
| TUR `nodejs-22` via `hampp node install 22`; `node`/`npm` through the `current` symlink in a new shell | ✅ v22.22.1 / npm 10.9.4 |
| `PHP_INI_SCAN_DIR` with a leading `:` (Termux conf.d, then hampp, then the user) for CLI and FPM | ✅ |
| TUI (bubbletea v2) in a real pty at 44×30: renders, keys, exits cleanly | ✅ |
| PHP resolves `*.localhost` (bionic getaddrinfo) | ❌ as the research predicted; hampp's welcome page and doctor say to use `127.0.0.1` + `Host` |
| Composer downloads | ⚠ failed in the container (`curl error 6`) while the `curl` CLI and PHP single requests resolved fine. It looks like a DNS quirk of the container, not hampp: it also happens without hampp's shell block. Check on a device. |

### Upstream Termux package problems found (2026-09)

- **php-redis 6.3.0RC1** was built against PHP 8.4's module API (20240924), but PHP is 8.5 (20250925), so the extension won't load.
- **php-apcu** is missing the `libandroid-shmem` dependency. hampp installs it, but the module then fails with `cannot locate symbol "empty_fcall_info"`, another build against an older PHP.

hampp handles both. It installs missing libraries it can identify, switches the broken module
off again so PHP stays clean, and explains why (`hampp php ext enable <name>` shows the reason).
Retry after `pkg upgrade` once Termux rebuilds the packages.

### Package format (reported from a device, 2026-09-20)

`apt` on a real phone refused the v0.1.0 package with *"could not locate member
control.tar{.xz,.lzma,}"*, while the same file installed fine in
termux-docker (apt 2.8.1, dpkg 1.22.6, both built with zlib). nFPM always writes
**control.tar.gz**, but Termux's own packages use **xz for both members**, and at
least some Termux builds only accept that.

Since v0.1.1 hampp builds its packages with
`dpkg-deb --uniform-compression -Zxz` (`scripts/build-termux-deb.sh`), so they
match Termux convention, and CI installs the real `.deb` before running the
smoke test. `scripts/install.sh` also falls back to the plain binary from the
tarball if `apt` refuses the package for any reason.

## Still to verify on a real device (Phase 0)

The container has no Android framework (`getprop`, `am`, the storage provider or browsers),
so these need a phone with Termux from F-Droid or GitHub:

- [ ] (e) `net.InterfaceAddrs()` in official Termux (targetSdk 28) and in the Play build; the UDP-dial fallback otherwise
- [ ] (i) Acode › Add path › Termux › `www`: edit and save, and the change is served
- [ ] (j) `hampp code start`: code-server from TUR starts with hampp's flags
- [ ] (k) `hampp mirror on` sees a `/sdcard/www` edit from another app within about 3 s
- [ ] (l) Chrome, Firefox and Samsung Internet open `http://blog.localhost:8080`
- [ ] (m) After installing the CA in Settings, Chrome shows a padlock for `https://blog.localhost:8443`, and Firefox does after the Secret-settings toggle
- [ ] (n) `am start -a android.settings.SECURITY_SETTINGS` opens Settings from Termux
- [ ] phantom-process warning and wakelock behaviour on Android 14
- [ ] Composer `create-project laravel/laravel` end to end
- [ ] Mouse/tap input in the TUI (optional)

Run `bash test/e2e/smoke.sh` on the device first; it covers everything in the first table.
