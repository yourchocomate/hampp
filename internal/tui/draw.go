package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Box characters. Termux renders Unicode box drawing fine; --ascii is a
// fallback for fonts or terminals that do not.
type glyphs struct {
	tl, tr, bl, br, h, v, lt, rt         string
	on, off, cursor, ok, warn, fail, dot string
	sep, arrow, ell, updown              string
}

var (
	unicodeGlyphs = glyphs{"╭", "╮", "╰", "╯", "─", "│", "├", "┤", "●", "○", ">", "✓", "!", "✗", "·", " · ", "→", "…", "↑ ↓"}
	asciiGlyphs   = glyphs{"+", "+", "+", "+", "-", "|", "+", "+", "*", "o", ">", "+", "!", "x", "-", " - ", "->", "...", "up/dn"}
)

// ellipsis is used when truncating; frame() switches it to "..." in ASCII mode.
var ellipsis = "…"

// ANSI 16-colour palette so the terminal theme (Termux default: dark) decides
// the exact shades.
var (
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	cyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	faint  = lipgloss.NewStyle().Faint(true)
	bold   = lipgloss.NewStyle().Bold(true)
	sel    = lipgloss.NewStyle().Reverse(true)
)

// section is a titled block inside a frame.
type section struct {
	title string
	lines []string
}

// frame draws a box of the given outer width with a title, optional right-hand
// title and titled sections separated by ├─ rules.
func frame(g glyphs, width int, title, right string, secs []section) string {
	ellipsis = g.ell
	if width < 30 {
		width = 30
	}
	inner := width - 2
	var b strings.Builder
	b.WriteString(rule(g, g.tl, g.tr, inner, title, right) + "\n")
	for i, s := range secs {
		if i > 0 || s.title != "" {
			if i > 0 {
				b.WriteString(rule(g, g.lt, g.rt, inner, s.title, "") + "\n")
			}
		}
		for _, l := range s.lines {
			b.WriteString(g.v + " " + pad(l, inner-2) + " " + g.v + "\n")
		}
	}
	b.WriteString(g.bl + strings.Repeat(g.h, inner) + g.br)
	return b.String()
}

func rule(g glyphs, left, rightEdge string, inner int, title, right string) string {
	var mid string
	if title != "" {
		mid = g.h + " " + bold.Render(title) + " "
	}
	tail := ""
	if right != "" {
		tail = " " + right + " " + g.h
	}
	fill := inner - lipgloss.Width(mid) - lipgloss.Width(tail)
	if fill < 1 {
		fill = 1
	}
	return left + mid + strings.Repeat(g.h, fill) + tail + rightEdge
}

// pad truncates or pads s (which may contain ANSI styles) to exactly n cells.
func pad(s string, n int) string {
	if lipgloss.Width(s) > n {
		s = ansi.Truncate(s, n, ellipsis)
	}
	return s + strings.Repeat(" ", max(0, n-lipgloss.Width(s)))
}

// cols lays out fixed-width columns; the last one takes the remaining space.
// Fixed columns always keep one space before the next column.
func cols(width int, parts []string, widths ...int) string {
	var b strings.Builder
	used := 0
	for i, p := range parts {
		if i < len(widths) {
			b.WriteString(pad(pad(p, widths[i]-1), widths[i]))
			used += widths[i]
		} else {
			b.WriteString(pad(p, max(1, width-used)))
		}
	}
	return b.String()
}

// wrap splits text into lines of at most n cells.
func wrap(s string, n int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		if para == "" {
			out = append(out, "")
			continue
		}
		out = append(out, strings.Split(ansi.Wrap(para, n, " /"), "\n")...)
	}
	return out
}

// Uptime formats a duration compactly: 45s, 12m, 2h14m, 3d4h.
func Uptime(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}

func footer(width int, keys ...string) string {
	var lines []string
	line := ""
	for _, k := range keys {
		next := k
		if line != "" {
			next = line + "  " + k
		}
		if lipgloss.Width(next) > width && line != "" {
			lines = append(lines, " "+line)
			line = k
			continue
		}
		line = next
	}
	if line != "" {
		lines = append(lines, " "+line)
	}
	return faint.Render(strings.Join(lines, "\n"))
}
