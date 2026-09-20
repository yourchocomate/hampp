//go:build unix

package cli

import (
	"testing"

	"github.com/yourchocomate/hampp/internal/config"
)

func TestSetKey(t *testing.T) {
	c := config.Default("/h")
	c, err := SetKey(c, "web.port", "8081")
	if err != nil || c.Web.Port != 8081 {
		t.Fatal(c.Web.Port, err)
	}
	if c, err = SetKey(c, "web.server", "nginx"); err != nil || c.Web.Server != "nginx" {
		t.Fatal(err)
	}
	if c, err = SetKey(c, "wake_lock", "false"); err != nil || c.WakeLock {
		t.Fatal(err)
	}
	if _, err := SetKey(c, "web.port", "80"); err == nil {
		t.Fatal("privileged port must be rejected")
	}
	if _, err := SetKey(c, "web.port", "abc"); err == nil {
		t.Fatal("not a number")
	}
	if _, err := SetKey(c, "web.nope", "1"); err == nil {
		t.Fatal("unknown key")
	}
	if _, err := SetKey(c, "web.server", "caddy"); err == nil {
		t.Fatal("validation must run")
	}
}
