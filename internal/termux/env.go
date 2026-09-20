// Package termux detects the Termux environment and wraps Termux/Android helpers.
package termux

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/yourchocomate/hampp/internal/sys"
)

// DefaultPrefix is the prefix of the official com.termux package.
const DefaultPrefix = "/data/data/com.termux/files/usr"

// Env describes where hampp is running.
type Env struct {
	Prefix     string // $PREFIX (or $TERMUX__PREFIX on app >= 0.119)
	Home       string
	IsTermux   bool
	AppVersion string // TERMUX_VERSION, e.g. 0.118.3
	APKRelease string // F_DROID, GITHUB, GOOGLE_PLAY_STORE
	SDK        int    // Android API level, 0 if unknown
	Model      string // ro.product.model
	Arch       string // Go arch of this binary
	TimeZone   string // persist.sys.timezone
}

// Detect inspects environment variables and the filesystem.
// HAMPP_PREFIX overrides detection for development on non-Android hosts.
func Detect(ctx context.Context, r sys.Runner) Env {
	e := Env{Arch: runtime.GOARCH}
	e.Home, _ = os.UserHomeDir()

	e.Prefix, e.IsTermux = detectPrefix(os.Getenv)
	e.AppVersion = firstEnv("TERMUX_VERSION", "TERMUX_APP__APP_VERSION_NAME")
	e.APKRelease = firstEnv("TERMUX_APK_RELEASE", "TERMUX_APP__APK_RELEASE")

	if e.IsTermux {
		e.SDK, _ = strconv.Atoi(getprop(ctx, r, "ro.build.version.sdk"))
		e.Model = getprop(ctx, r, "ro.product.model")
		e.TimeZone = getprop(ctx, r, "persist.sys.timezone")
	}
	return e
}

func detectPrefix(getenv func(string) string) (string, bool) {
	if p := getenv("HAMPP_PREFIX"); p != "" {
		return p, getenv("HAMPP_FAKE_TERMUX") == "1"
	}
	for _, k := range []string{"TERMUX__PREFIX", "PREFIX"} {
		if p := getenv(k); p != "" && isDir(filepath.Join(p, "bin")) && strings.Contains(p, "com.termux") {
			return p, true
		}
	}
	if getenv("TERMUX_VERSION") != "" && isDir(DefaultPrefix) {
		return DefaultPrefix, true
	}
	if isDir(DefaultPrefix) {
		return DefaultPrefix, true
	}
	return "/usr", false
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func getprop(ctx context.Context, r sys.Runner, key string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	bin := "getprop"
	if _, err := r.LookPath(bin); err != nil {
		bin = "/system/bin/getprop"
	}
	out, err := r.Output(ctx, "", bin, key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// AndroidVersion maps an API level to a marketing version for messages.
func AndroidVersion(sdk int) string {
	switch {
	case sdk >= 36:
		return "16+"
	case sdk == 35:
		return "15"
	case sdk == 34:
		return "14"
	case sdk == 33:
		return "13"
	case sdk == 32:
		return "12L"
	case sdk == 31:
		return "12"
	case sdk == 30:
		return "11"
	case sdk == 29:
		return "10"
	case sdk > 0:
		return "≤9"
	}
	return "unknown"
}

// InstallSource returns a human label for TERMUX_APK_RELEASE.
func (e Env) InstallSource() string {
	switch e.APKRelease {
	case "F_DROID":
		return "F-Droid"
	case "GITHUB":
		return "GitHub"
	case "GOOGLE_PLAY_STORE":
		return "Google Play (unofficial build)"
	case "":
		return "unknown source"
	}
	return e.APKRelease
}

// Bin returns the absolute path of a program in $PREFIX/bin.
func (e Env) Bin(name string) string { return filepath.Join(e.Prefix, "bin", name) }
