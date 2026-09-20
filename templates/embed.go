// Package templates embeds the config templates hampp renders. hampp never
// edits package-owned files such as $PREFIX/etc/apache2/httpd.conf; every
// daemon is started with one of these rendered files instead.
package templates

import "embed"

//go:embed *.tmpl
var FS embed.FS
