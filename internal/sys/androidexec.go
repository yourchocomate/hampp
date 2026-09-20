package sys

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
)

// Android's W^X rules stop apps that target SDK 29+ from exec()ing files in
// their own data directory, which is where Termux lives. Termux works around
// it with termux-exec, an LD_PRELOAD library that rewrites exec calls into
// "/system/bin/linker64 <file>". Go does not use libc for exec, so that
// interception never applies to hampp and exec fails with EACCES:
//
//	fork/exec /data/data/com.termux/files/usr/bin/pkg: permission denied
//
// This happens on Termux builds targeting a newer SDK, in practice the
// unofficial Google Play build. hampp re-runs the command through a program on
// the system partition, which is allowed to exec: /system/bin/sh, whose own
// libc exec is then intercepted by termux-exec as usual. If that is unavailable
// it calls the system linker directly, the way termux-exec does.
//
// Everything here is a no-op on the official F-Droid and GitHub builds.

type ExecMode int32

const (
	ModeAuto   ExecMode = iota // try direct, fall back on a permission error
	ModeDirect                 // never rewrite
	ModeShell                  // via /system/bin/sh
	ModeLinker                 // via /system/bin/linker64
	modeFailed                 // nothing left to try
)

const SystemShell = "/system/bin/sh"

var execMode atomic.Int32

func init() {
	switch strings.ToLower(os.Getenv("HAMPP_EXEC_MODE")) {
	case "direct":
		execMode.Store(int32(ModeDirect))
	case "sh", "shell":
		execMode.Store(int32(ModeShell))
	case "linker":
		execMode.Store(int32(ModeLinker))
	default:
		// The Google Play build always needs the workaround, so skip the
		// failing first attempt.
		if os.Getenv("TERMUX_APK_RELEASE") == "GOOGLE_PLAY_STORE" || os.Getenv("TERMUX_APP__APK_RELEASE") == "GOOGLE_PLAY_STORE" {
			execMode.Store(int32(ModeShell))
		}
	}
}

// Mode reports the strategy in use.
func Mode() ExecMode { return ExecMode(execMode.Load()) }

// FallbackName describes an active workaround for `hampp doctor`, or "".
func FallbackName() string {
	switch Mode() {
	case ModeShell:
		return SystemShell
	case ModeLinker:
		return SystemLinker()
	}
	return ""
}

// SystemLinker is Android's dynamic linker for this binary's word size.
func SystemLinker() string {
	if runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64" {
		return "/system/bin/linker64"
	}
	return "/system/bin/linker"
}

// Command applies the active strategy to a command line.
func Command(name string, args []string) (string, []string) {
	switch Mode() {
	case ModeShell:
		// `exec "$0" "$@"` keeps the pid (important for daemons) and needs no
		// quoting: sh assigns $0 and $@ from the arguments.
		return SystemShell, append([]string{"-c", `exec "$0" "$@"`, name}, args...)
	case ModeLinker:
		target, pre := interpreterOf(name)
		return SystemLinker(), append(append([]string{target}, pre...), args...)
	}
	return name, args
}

// interpreterOf returns what the linker should run: for a script, its shebang
// interpreter plus the script path; for anything else, the file itself.
func interpreterOf(name string) (target string, prefixArgs []string) {
	f, err := os.Open(name)
	if err != nil {
		return name, nil
	}
	defer f.Close()
	buf := make([]byte, 256)
	n, _ := f.Read(buf)
	line := string(buf[:n])
	if !strings.HasPrefix(line, "#!") {
		return name, nil
	}
	line, _, _ = strings.Cut(line, "\n")
	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	if len(fields) == 0 {
		return name, nil
	}
	return fields[0], append(fields[1:], name)
}

// Degrade moves to the next strategy after a permission error and reports
// whether the caller should retry.
func Degrade() bool {
	switch Mode() {
	case ModeAuto, ModeDirect:
		if _, err := os.Stat(SystemShell); err == nil {
			execMode.Store(int32(ModeShell))
			return true
		}
		fallthrough
	case ModeShell:
		if _, err := os.Stat(SystemLinker()); err == nil {
			execMode.Store(int32(ModeLinker))
			return true
		}
	}
	execMode.Store(int32(modeFailed))
	return false
}

// IsPermission reports an EACCES/EPERM from starting a program.
func IsPermission(err error) bool {
	return err != nil && (errors.Is(err, fs.ErrPermission) || strings.Contains(err.Error(), "permission denied"))
}

// ExecHint explains a permission failure that no workaround could fix.
func ExecHint(name string) string {
	return "Android would not let hampp run " + name + ".\n" +
		"This happens on Termux builds that target a newer Android SDK (the Google Play build).\n" +
		"Install Termux from F-Droid or github.com/termux/termux-app, or report this with `hampp doctor`."
}
