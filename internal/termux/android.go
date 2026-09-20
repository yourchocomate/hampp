package termux

import (
	"context"
	"fmt"
	"strings"

	"github.com/yourchocomate/hampp/internal/sys"
)

// OpenURL opens a URL in the default Android browser. termux-open-url ships in
// termux-tools and uses `am start` directly, so Termux:API is not required.
func OpenURL(ctx context.Context, r sys.Runner, url string) error {
	_, err := r.Output(ctx, "", "termux-open-url", url)
	return err
}

// WakeLock asks Termux to hold a partial wakelock so servers keep running with
// the screen off. It is a no-op outside Termux.
func WakeLock(ctx context.Context, r sys.Runner, on bool) error {
	name := "termux-wake-lock"
	if !on {
		name = "termux-wake-unlock"
	}
	if _, err := r.LookPath(name); err != nil {
		return nil
	}
	_, err := r.Output(ctx, "", name)
	return err
}

// OpenSecuritySettings launches Android's Security settings screen, where the
// user installs a CA certificate. Android blocks background activity starts, so
// this only works while Termux is in the foreground.
func OpenSecuritySettings(ctx context.Context, r sys.Runner) error {
	_, err := r.Output(ctx, "", "am", "start", "-a", "android.settings.SECURITY_SETTINGS")
	return err
}

// PhantomKillerFix returns instructions for disabling the phantom process
// killer on the given API level (see agnostic-apollo/Android-Docs).
func PhantomKillerFix(sdk int) []string {
	switch {
	case sdk >= 34:
		return []string{
			"Settings > System > Developer options >",
			"  Disable child process restrictions  (turn ON)",
			"(Enable Developer options: Settings > About phone > tap Build number 7x)",
		}
	case sdk == 32 || sdk == 33:
		return []string{
			"From a computer with adb:",
			`  adb shell "settings put global settings_enable_monitor_phantom_procs false"`,
		}
	case sdk == 31:
		return []string{
			"From a computer with adb:",
			`  adb shell "/system/bin/device_config set_sync_disabled_for_tests persistent"`,
			`  adb shell "/system/bin/device_config put activity_manager max_phantom_processes 2147483647"`,
		}
	}
	return nil
}

// CAInstallSteps returns the Settings path for installing a user CA certificate.
// Vendors rename these screens, so the steps are phrased to be searchable.
func CAInstallSteps(sdk int, file string) []string {
	var steps []string
	if sdk >= 34 {
		steps = []string{
			"Settings > Security & privacy",
			"More security settings (or: More security & privacy)",
			"Encryption & credentials",
		}
	} else {
		steps = []string{
			"Settings > Security (or: Biometrics and security)",
			"Encryption & credentials (or: Other security settings)",
		}
	}
	steps = append(steps,
		"Install a certificate > CA certificate > Install anyway",
		fmt.Sprintf("Pick %s", file),
	)
	steps = append(steps, "Tip: search Settings for \"CA certificate\" if a step is named differently.")
	return steps
}

// FirefoxCASteps returns the steps for making Firefox for Android trust user CAs.
func FirefoxCASteps() []string {
	return []string{
		"Firefox > Settings > About Firefox > tap the logo 5 times",
		"Back to Settings > Secret settings > Use third party CA certificates (ON)",
	}
}

// IsV1Wrapper reports whether a file is the old HamppServer (v1) launcher script.
func IsV1Wrapper(content string) bool {
	if strings.HasPrefix(content, "\x7fELF") || len(content) > 4096 {
		return false // hampp v2 itself is an ELF binary at the same path
	}
	return strings.Contains(content, "HamppServer") && strings.Contains(content, ".HamppServer.py")
}
