package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

func TestFrameLinesHaveExactWidth(t *testing.T) {
	for _, g := range []glyphs{unicodeGlyphs, asciiGlyphs} {
		for _, w := range []int{30, 44, 60} {
			out := frame(g, w, "hampp 2.0", green.Render("● 3 running"), []section{
				{lines: []string{"Pixel 7 · Android 14"}},
				{title: "Services", lines: []string{"● web apache :8080 :8443 2h14m", strings.Repeat("x", 100)}},
				{title: "Sites", lines: []string{"blog.localhost ~/www/blog/public"}},
			})
			for i, l := range strings.Split(out, "\n") {
				if got := lipgloss.Width(l); got != w {
					t.Errorf("width %d line %d is %d cells: %q", w, i, got, l)
				}
			}
		}
	}
}

func TestASCIIFrameIsPureASCII(t *testing.T) {
	out := frame(asciiGlyphs, 44, "hampp", "", []section{{title: "Services", lines: []string{"* web apache"}}})
	for _, r := range out {
		if r > 127 && r != '…' {
			t.Fatalf("non-ASCII rune %q in ASCII mode", r)
		}
	}
}

func TestUptime(t *testing.T) {
	cases := map[time.Duration]string{
		45 * time.Second:              "45s",
		12 * time.Minute:              "12m",
		2*time.Hour + 14*time.Minute:  "2h14m",
		51*time.Hour + 30*time.Minute: "2d3h",
	}
	for d, want := range cases {
		if got := Uptime(d); got != want {
			t.Errorf("%v: %s want %s", d, got, want)
		}
	}
}

func TestFooterWraps(t *testing.T) {
	f := footer(44, "s start", "x stop", "r reload", "o open", "l logs", "n node", "c ssl", "d doctor", "? help", "q quit")
	for _, l := range strings.Split(f, "\n") {
		if lipgloss.Width(l) > 44 {
			t.Errorf("footer line too wide: %q", l)
		}
	}
}
