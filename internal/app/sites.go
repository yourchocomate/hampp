//go:build unix

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yourchocomate/hampp/internal/config"
	"github.com/yourchocomate/hampp/internal/netinfo"
	"github.com/yourchocomate/hampp/internal/site"
)

// SiteLink serves a project outside the web root at <name>.localhost.
func (a *App) SiteLink(ctx context.Context, name, path, root string, force bool) error {
	if err := a.RequireInit(); err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return fmt.Errorf("%s is not a folder", abs)
	}
	if name == "" {
		name = site.Sanitize(filepath.Base(abs))
	}
	if !config.ValidSiteName(name) {
		return fmt.Errorf("site name %q: use lowercase letters, digits and dashes", name)
	}
	if site.OnSharedStorage(abs) && !force {
		return fmt.Errorf("%s is on shared storage, where npm/composer symlinks and file permissions fail; move it under ~ or pass --force", abs)
	}
	links := a.Cfg.Sites.Links[:0:0]
	for _, l := range a.Cfg.Sites.Links {
		if l.Name != name {
			links = append(links, l)
		}
	}
	a.Cfg.Sites.Links = append(links, config.Link{Name: name, Path: abs, Root: root})
	if err := a.SaveConfig(); err != nil {
		return err
	}
	a.printf("Linked %s → http://%s.localhost:%d/\n", a.Paths.Short(abs), name, a.Cfg.Web.Port)
	return a.Reload(ctx)
}

// SiteUnlink removes a linked site. Parked sites are removed by moving/renaming
// the folder, so they are not handled here.
func (a *App) SiteUnlink(ctx context.Context, name string) error {
	if err := a.RequireInit(); err != nil {
		return err
	}
	var links []config.Link
	found := false
	for _, l := range a.Cfg.Sites.Links {
		if l.Name == name {
			found = true
			continue
		}
		links = append(links, l)
	}
	if !found {
		return fmt.Errorf("no linked site %q (parked sites disappear when their folder in %s is removed)", name, a.Paths.Short(a.Cfg.Web.Root))
	}
	a.Cfg.Sites.Links = links
	if err := a.SaveConfig(); err != nil {
		return err
	}
	return a.Reload(ctx)
}

// FindSite returns the site with host or name.
func (a *App) FindSite(name string) (site.Site, bool) {
	sites, _ := a.Sites()
	for _, s := range sites {
		if name == "" && s.Default || s.Name == name || s.Host == name {
			return s, true
		}
	}
	return site.Site{}, false
}

// Share switches LAN access on or off and returns the URLs other devices can use.
func (a *App) Share(ctx context.Context, on bool) ([]string, error) {
	if err := a.RequireInit(); err != nil {
		return nil, err
	}
	a.Cfg.Web.Share = on
	if err := a.SaveConfig(); err != nil {
		return nil, err
	}
	// The listen address changes, which needs a restart rather than a reload.
	if err := a.restartWebIfRunning(ctx); err != nil {
		return nil, err
	}
	if !on {
		return nil, nil
	}
	ips := netinfo.LANIPs()
	if len(ips) == 0 {
		return nil, fmt.Errorf("sharing is on, but no Wi-Fi/LAN address was found; connect to Wi-Fi or enable the hotspot")
	}
	var urls []string
	sites, _ := a.Sites()
	for _, ip := range ips {
		urls = append(urls, fmt.Sprintf("http://%s:%d/", ip, a.Cfg.Web.Port))
		for _, s := range sites {
			if !s.Default {
				urls = append(urls, fmt.Sprintf("http://%s.%s.nip.io:%d/  (needs internet for DNS)", s.Name, ip, a.Cfg.Web.Port))
			}
		}
	}
	return urls, nil
}

// OpenURL returns the URL `hampp open` should launch.
func (a *App) OpenURL(name string, https bool) (string, error) {
	s, ok := a.FindSite(strings.TrimSuffix(name, ".localhost"))
	if !ok {
		return "", fmt.Errorf("no site %q (see `hampp site ls`)", name)
	}
	if https && !a.Cfg.Web.HTTPS {
		return "", fmt.Errorf("HTTPS is off; run `hampp ssl init`")
	}
	port := a.Cfg.Web.Port
	if https {
		port = a.Cfg.Web.HTTPSPort
	}
	return s.URL(port, https), nil
}
