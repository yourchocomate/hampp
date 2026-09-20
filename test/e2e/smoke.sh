#!/data/data/com.termux/files/usr/bin/bash
# End-to-end smoke test, run inside Termux (a device or termux/termux-docker)
# as the normal Termux user:  bash test/e2e/smoke.sh /path/to/hampp
set -euo pipefail
BIN=${1:-hampp}
fail() { echo "FAIL: $*" >&2; exit 1; }
step() { printf '\n== %s\n' "$*"; }
if [ "$BIN" != hampp ]; then install -m 755 "$BIN" "$PREFIX/bin/hampp"; fi

step "init (apache, mariadb, https)"
hampp init --yes --https >"$TMPDIR/init.log" 2>&1 || { tail -30 "$TMPDIR/init.log"; fail init; }
hampp status

step "welcome page through php-fpm"
curl -fsS http://localhost:8080/ | grep -q 'hampp is running' || fail "welcome page"

step "parked site + .htaccess + dotfiles"
mkdir -p ~/www/blog/public
printf '%s' '<?php echo "ok ", $_SERVER["HTTP_HOST"];' >~/www/blog/public/index.php
printf 'RewriteEngine On\nRewriteRule ^hello$ index.php [L]\n' >~/www/blog/public/.htaccess
hampp reload
[ "$(curl -fsS http://blog.localhost:8080/)" = "ok blog.localhost:8080" ] || fail "parked site"
[ "$(curl -fsS http://blog.localhost:8080/hello)" = "ok blog.localhost:8080" ] || fail ".htaccess rewrite"
[ "$(curl -s -o /dev/null -w '%{http_code}' http://blog.localhost:8080/.htaccess)" = 403 ] || fail "dotfile not denied"

step "https with the local CA"
[ "$(curl -fsS --cacert ~/.local/share/hampp/ca/bundle.pem https://blog.localhost:8443/)" = "ok blog.localhost:8443" ] || fail https
hampp ssl status

step "database"
mariadb -e 'SELECT CURRENT_USER()' | grep -q hampp@localhost || fail "~/.my.cnf login"
if mariadb --no-defaults -S "$PREFIX/var/run/mysqld.sock" -u root -e 'select 1' 2>/dev/null; then fail "root still passwordless"; fi
hampp db create smoke
printf '%s' '<?php $i=parse_ini_file(getenv("HOME")."/.my.cnf",true); new PDO("mysql:host=localhost;dbname=smoke",$i["client"]["user"],$i["client"]["password"]); echo "pdo ok";' >~/www/pdo.php
[ "$(curl -fsS http://localhost:8080/pdo.php)" = "pdo ok" ] || fail "PDO over the socket from php-fpm"
[ "$(curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/adminer/)" = 200 ] || fail adminer

step "php settings"
hampp php set max_execution_time 300
printf '%s' '<?php echo ini_get("max_execution_time");' >~/www/ini.php
[ "$(curl -fsS http://localhost:8080/ini.php)" = 300 ] || fail "php set not applied to websites"

step "share keeps the database local"
hampp share on >/dev/null
netstat -ltn 2>/dev/null | grep -q '0.0.0.0:8080' || fail "web not shared"
netstat -ltn 2>/dev/null | grep -q '127.0.0.1:3306' || fail "database must stay on loopback"
hampp share off >/dev/null

step "doctor"
hampp doctor

step "stop leaves nothing running"
hampp stop
sleep 1
if ps -e -o comm= | grep -E -q '^(httpd|php-fpm|mariadbd)$'; then fail "processes left"; fi

step "re-running init is safe"
hampp init --yes --no-start >/dev/null
echo
echo "SMOKE TEST PASSED"
