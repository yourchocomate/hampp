# hampp

A local PHP web server for your Android phone, inside [Termux](https://termux.dev).
One command sets up Apache or nginx, PHP (php-fpm) and MariaDB or SQLite, plus Composer.
You get sites at `http://<name>.localhost:8080`, HTTPS with a local certificate, Node.js
version switching and a phone-sized dashboard.

hampp v2 is a rewrite of [HamppServer](https://github.com/yourchocomate/HamppServer) in Go.

```
╭─ hampp ──────────────────── ● 3 running ─╮
│ Pixel 7 · Android 14 · Termux F-Droid    │
├─ Services ───────────────────────────────┤
│ >● db     mariadb     :3306       2h14m  │
│  ● php    php-fpm     socket      2h14m  │
│  ● web    apache      :8080 :8443 2h14m  │
│  ○ code   code-server             off    │
│  ○ mirror mirror                  off    │
├─ Sites ──────────────────────────────────┤
│   localhost           ~/www              │
│   blog.localhost      ~/www/blog/public  │
│   api.localhost       ~/projects/api/pu… │
├─ Node ───────────────────────────────────┤
│   v22  (nodejs-22)                       │
╰──────────────────────────────────────────╯
 s start db  x stop db  r reload db
 S X R all  o open  p php  n node  c ssl
 l logs  d doctor  ? help  q quit
```

In the dashboard, `s`, `x` and `r` act on the **selected** service (use `tab` and
the arrows to pick one); `S`, `X` and `R` act on all of them. `enter` opens a
menu with the same actions plus logs.

## Install

You need Termux from **F-Droid** or **GitHub** (those are the official builds). Then run:

```sh
curl -fsSL https://raw.githubusercontent.com/yourchocomate/hampp/main/scripts/install.sh | sh
```

The installer downloads the release for your CPU (aarch64, arm, x86_64 or i686) and
checks it against `checksums.txt`. It installs the package with apt, so
`apt remove hampp` uninstalls it. Then it starts the setup.

To build from source instead, use Termux's own Go, which is patched for Android DNS and certificates:
`pkg install golang && go install github.com/yourchocomate/hampp/cmd/hampp@latest`.

## Quick start

```sh
hampp            # dashboard (runs the setup wizard the first time)
hampp init       # or: guided setup without the dashboard
hampp open       # opens http://localhost:8080 in your browser
```

Put a folder in `~/www` and it becomes a site:

```sh
cd ~/www && composer create-project laravel/laravel blog
hampp reload     # then open http://blog.localhost:8080
```

hampp uses the `public/` folder automatically (Laravel, Symfony), and `.htaccess`
works with Apache. To serve a project from somewhere else, run `hampp site link api ~/projects/api`.

## Commands

| | |
|---|---|
| `hampp init` | Install packages and set up (safe to run again) |
| `hampp start/stop/restart/reload [web\|php\|db]` | Control services, all of them or one |
| `hampp status`, `hampp logs [svc] -f` | See what's running and why something failed |
| `hampp site ls/link/unlink/open` | Sites on `<name>.localhost` |
| `hampp php`, `hampp php ext install redis`, `hampp php set memory_limit 512M`, `hampp php edit` | PHP settings and extensions |
| `hampp db setup/info/create <name>` | MariaDB user, connection details, new databases |
| `hampp ssl init/trust/status` | Local HTTPS |
| `hampp node install 22`, `hampp node use` | Node.js versions (reads `.nvmrc`) |
| `hampp edit`, `hampp code start` | Edit files with Android apps or VS Code in the browser |
| `hampp share on/off` | Let other devices on your Wi-Fi open your sites |
| `hampp serve` | PHP's built-in server in the current folder |
| `hampp doctor [--fix]` | Check everything and show fixes |
| `hampp config get/set/edit` | Settings (`~/.config/hampp/config.toml`) |

## Things Android does differently (read this)

**Servers stop in the background (Android 12+).** Android kills child processes of apps
once more than 32 run across *all* apps, or when they use a lot of CPU in the background.
hampp keeps its own processes to about 6 and holds a Termux wakelock. To turn the limit off:

- Android 14+: Settings › System › Developer options › **Disable child process restrictions**
- Android 12L/13: `adb shell "settings put global settings_enable_monitor_phantom_procs false"`

`hampp doctor` shows the exact steps for your Android version.

**Ports below 1024 need root.** That's why URLs use `:8080` and `:8443`.

**Your sites live in `~/www`, not `/sdcard`.** Shared storage can't hold symlinks or
executable files, so npm, Composer and `php artisan storage:link` fail there. To edit
`~/www` with a phone app:

- **Acode:** File browser › Add path › pick **Termux** › `www`
- **Material Files:** Add storage › External storage › Termux
- **VS Code in the browser:** `hampp code start`
- If you really need a folder on `/sdcard`, run `hampp mirror on`. It copies `/sdcard/www` into `~/www`.

**Local domains.** Browsers and curl treat every `*.localhost` name as this phone, so no
DNS setup is needed. PHP itself can't resolve those names on Android. For server-side
requests to your own sites, use `http://127.0.0.1:8080` with a `Host` header.

**HTTPS.** Since Android 11, no app can install a certificate for you. `hampp ssl trust`
copies the certificate to Downloads and walks you through
*Settings › Encryption & credentials › Install a certificate › CA certificate*.
For Firefox, turn on *Secret settings › Use third party CA certificates*.
Termux tools (curl, PHP, Composer) are set up to trust it automatically.

**nvm doesn't work on Termux.** It refuses to run when `$PREFIX` is set, and it downloads
builds that don't run on Android. `hampp node` switches between the Termux User Repository
packages `nodejs-NN` instead, and reads `.nvmrc`.

## How it works

- hampp never edits package config files. It writes its own configs to
  `~/.local/share/hampp/conf` and starts each server with them
  (`httpd -f`, `nginx -c`, `php-fpm -y`, `mariadbd --defaults-file`).
- PHP runs in php-fpm behind Apache (worker MPM) or nginx. Sites listen on
  `127.0.0.1` unless you run `hampp share on`. MariaDB always stays on `127.0.0.1`.
- PHP settings are loaded in this order, and later ones win: Termux's `conf.d`,
  then hampp's `php.d`, then your `~/.config/hampp/php.d/custom.ini`. Both websites and
  the CLI use this order (`PHP_INI_SCAN_DIR`).
- `hampp db setup` gives MariaDB's root user a random password and creates a `hampp` user.
  Passwords are stored in `~/.config/hampp/credentials.toml` (mode 0600).
  `mariadb` logs you in through `~/.my.cnf`.
- A marked block in `~/.bashrc` sets PATH (the active Node.js, Composer's global bin),
  `SSL_CERT_FILE` and `PHP_INI_SCAN_DIR`. `hampp uninstall` removes it.

Verification status and platform notes: [docs/platform-notes.md](docs/platform-notes.md).

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md). In short:

```sh
make test          # unit tests
make e2e           # full stack in real Termux packages (Docker, termux/termux-docker)
make shell         # Termux shell with hampp; sites reachable at http://<site>.localhost:8080
make release-snapshot   # release binaries + .deb via the NDK image
```

## License

Apache-2.0
