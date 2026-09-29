//go:build unix

package app

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yourchocomate/hampp/internal/config"
	"github.com/yourchocomate/hampp/internal/service"
)

// SitesChanged reports whether the sites discovered now differ from the ones
// the web server config was last rendered with.
func (a *App) SitesChanged() bool {
	c, _ := a.Ctx()
	b, err := os.ReadFile(service.SignatureFile(c))
	if err != nil {
		return true
	}
	return string(b) != service.SitesSignature(c.Data.Sites)
}

// WatchSites is the loop behind `hampp site watch`. It polls every two seconds:
// one directory listing, far below what Android's background CPU limits notice,
// and it also sees folders created by other apps through the storage provider,
// which inotify would not report reliably.
func (a *App) WatchSites(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	var cfgMod time.Time
	for {
		// Pick up `hampp site link` and config edits made by other commands.
		if st, err := os.Stat(a.Paths.ConfigFile()); err == nil && !st.ModTime().Equal(cfgMod) {
			cfgMod = st.ModTime()
			if cfg, err := config.Load(a.Paths.ConfigFile(), a.Env.Home); err == nil {
				a.Cfg = cfg
			}
		}
		c, _ := a.Ctx()
		if a.Web().Status(c).Running && a.SitesChanged() {
			a.printf("%s sites changed; reloading %s\n", time.Now().Format(time.TimeOnly), a.Web().Title())
			if err := a.ReloadService(ctx, "web"); err != nil {
				a.printf("%s reload failed: %v\n", time.Now().Format(time.TimeOnly), err)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}
