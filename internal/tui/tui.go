//go:build unix

// Package tui is hampp's interactive dashboard. It is sized for Termux in
// portrait (~44 columns) and only needs keys on Termux's extra-keys row
// (arrows, Tab, Esc, Enter) plus letters.
package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/yourchocomate/hampp/internal/app"
	"github.com/yourchocomate/hampp/internal/config"
	"github.com/yourchocomate/hampp/internal/node"
	"github.com/yourchocomate/hampp/internal/service"
	"github.com/yourchocomate/hampp/internal/site"
	"github.com/yourchocomate/hampp/internal/termux"
)

type Options struct {
	ASCII bool
}

type screen int

const (
	scrDash screen = iota
	scrMenu
	scrNode
	scrSSL
	scrDoctor
	scrLogs
	scrEdit
	scrHelp
	scrPHP
)

type menuItem struct {
	label  string
	action func(m *model) tea.Cmd
}

type model struct {
	ctx  context.Context
	a    *app.App
	g    glyphs
	out  *syncBuffer
	w, h int

	scr      screen
	prev     screen
	focus    int // 0 services, 1 sites
	svcIdx   int
	siteIdx  int
	statuses []service.Status
	sites    []site.Site

	menuTitle string
	menu      []menuItem
	menuIdx   int

	nodeSt      app.NodeState
	remote      []int
	remoteNote  string
	nodeIdx     int
	doctor      []app.Check
	doctorBusy  bool
	sslChecks   []app.Check
	logIdx      int
	phpExts     []app.Ext
	phpVals     map[string][2]string
	phpIdx      int
	phpNote     string
	busy        string
	msg         string
	msgErr      bool
	lastRefresh time.Time
}

type (
	tickMsg   time.Time
	actionMsg struct {
		text string
		err  error
	}
	doctorMsg []app.Check
	sslMsg    []app.Check
	remoteMsg struct {
		majors []int
		err    error
	}
	phpMsg struct {
		exts []app.Ext
		vals map[string][2]string
		err  error
	}
	refreshMsg struct{}
)

// syncBuffer collects app output produced by background commands.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) take() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.b.String()
	s.b.Reset()
	return strings.TrimSpace(t)
}

// Run starts the dashboard, running the setup wizard first if needed.
func Run(ctx context.Context, a *app.App, o Options) error {
	if err := a.RequireTermux(); err != nil {
		return err
	}
	if !a.Initialized {
		opts, ok, err := Wizard(a, o.ASCII)
		if err != nil || !ok {
			return err
		}
		if err := a.Init(ctx, opts); err != nil {
			return err
		}
		fmt.Print("\nPress Enter to open the dashboard…")
		_, _ = fmt.Scanln()
	}
	g := unicodeGlyphs
	if o.ASCII {
		g = asciiGlyphs
	}
	buf := &syncBuffer{}
	a.Out = buf
	m := &model{ctx: ctx, a: a, g: g, out: buf, w: 44, h: 24}
	m.refresh()
	_, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	return err
}

func (m *model) Init() tea.Cmd { return tick() }

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) refresh() {
	m.statuses = m.a.Statuses()
	m.sites, _ = m.a.Sites()
	m.nodeSt = m.a.Node()
	m.lastRefresh = time.Now()
	m.svcIdx = clamp(m.svcIdx, len(m.statuses))
	m.siteIdx = clamp(m.siteIdx, len(m.sites))
}

func clamp(i, n int) int {
	if n == 0 {
		return 0
	}
	return max(0, min(i, n-1))
}

// ---- commands -------------------------------------------------------------

// act runs a quick app action in the background and reports its output.
func (m *model) act(label string, fn func(ctx context.Context) error) tea.Cmd {
	if m.busy != "" {
		return nil
	}
	m.busy, m.msg = label, ""
	ctx := m.ctx
	return func() tea.Msg {
		err := fn(ctx)
		return actionMsg{text: m.out.take(), err: err}
	}
}

// shell suspends the TUI and runs a hampp subcommand in the terminal, for
// long or interactive work (package installs, prompts, log following).
func (m *model) shell(args ...string) tea.Cmd {
	script := `"$0" "$@"; s=$?; printf '\n[Enter] back to hampp '; read _; exit $s`
	c := exec.Command("sh", append([]string{"-c", script, m.a.Self}, args...)...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		// The subcommand may have changed config; reload it.
		if fresh, lerr := app.Load(m.ctx, m.out); lerr == nil {
			fresh.Out = m.out
			*m.a = *fresh
		}
		return actionMsg{err: err}
	})
}

func (m *model) loadDoctor() tea.Cmd {
	m.doctorBusy = true
	return func() tea.Msg { return doctorMsg(m.a.Doctor(m.ctx)) }
}

func (m *model) loadSSL() tea.Cmd {
	return func() tea.Msg { return sslMsg(m.a.SSLStatus(m.ctx)) }
}

func (m *model) loadPHP() tea.Cmd {
	m.phpNote = "Loading…"
	return func() tea.Msg {
		exts, err := m.a.PHPExtList(m.ctx)
		if err != nil {
			return phpMsg{err: err}
		}
		vals, err := m.a.PHPGet(m.ctx, nil)
		return phpMsg{exts: exts, vals: vals, err: err}
	}
}

// phpRows are the extensions shown in the list (built-ins are summarised).
func (m *model) phpRows() []app.Ext {
	var rows []app.Ext
	for _, e := range m.phpExts {
		if e.State != app.ExtBuiltIn {
			rows = append(rows, e)
		}
	}
	return rows
}

func (m *model) loadRemote() tea.Cmd {
	if !m.a.PM.Installed(m.ctx, "tur-repo") {
		m.remoteNote = "Press a to add the Termux User Repository (TUR) and list versions."
		return nil
	}
	m.remoteNote = "Loading versions from TUR…"
	return func() tea.Msg {
		majors, err := m.a.PM.NodeMajors(m.ctx)
		return remoteMsg{majors, err}
	}
}

// ---- update ---------------------------------------------------------------

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tickMsg:
		if m.busy == "" {
			m.refresh()
		}
		return m, tick()
	case actionMsg:
		m.busy = ""
		m.msg, m.msgErr = msg.text, msg.err != nil
		if msg.err != nil {
			m.msg = strings.TrimSpace(m.msg + "\n" + msg.err.Error())
		}
		m.refresh()
		switch m.scr {
		case scrNode:
			return m, m.loadRemote()
		case scrPHP:
			return m, m.loadPHP()
		}
	case doctorMsg:
		m.doctor, m.doctorBusy = msg, false
	case sslMsg:
		m.sslChecks = msg
	case phpMsg:
		if msg.err != nil {
			m.phpNote = "PHP: " + msg.err.Error()
		} else {
			m.phpExts, m.phpVals, m.phpNote = msg.exts, msg.vals, ""
		}
	case remoteMsg:
		if msg.err != nil {
			m.remoteNote = "Could not list TUR versions: " + msg.err.Error()
		} else {
			m.remote, m.remoteNote = msg.majors, ""
		}
	case tea.KeyPressMsg:
		return m, m.key(msg.String())
	}
	return m, nil
}

func (m *model) key(k string) tea.Cmd {
	if k == "ctrl+c" {
		return tea.Quit
	}
	switch m.scr {
	case scrDash:
		return m.dashKey(k)
	case scrMenu:
		switch k {
		case "up", "k":
			m.menuIdx = clamp(m.menuIdx-1, len(m.menu))
		case "down", "j":
			m.menuIdx = clamp(m.menuIdx+1, len(m.menu))
		case "enter":
			item := m.menu[m.menuIdx]
			m.scr = m.prev
			return item.action(m)
		case "esc", "q", "backspace":
			m.scr = m.prev
		}
	case scrNode:
		return m.nodeKey(k)
	case scrSSL:
		switch k {
		case "t":
			return m.shell("ssl", "trust")
		case "i":
			return m.shell("ssl", "init")
		case "o":
			return m.act("Opening Settings", func(ctx context.Context) error { return termux.OpenSecuritySettings(ctx, m.a.R) })
		case "s":
			return m.loadSSL()
		case "esc", "q", "backspace":
			m.scr = scrDash
		}
	case scrDoctor:
		switch k {
		case "f":
			return m.shell("doctor", "--fix")
		case "r":
			return m.loadDoctor()
		case "esc", "q", "backspace":
			m.scr = scrDash
		}
	case scrLogs:
		names := m.logServices()
		switch k {
		case "tab", "right", "l":
			m.logIdx = (m.logIdx + 1) % len(names)
		case "shift+tab", "left", "h":
			m.logIdx = (m.logIdx + len(names) - 1) % len(names)
		case "f":
			return m.shell("logs", "-f", names[m.logIdx])
		case "esc", "q", "backspace":
			m.scr = scrDash
		}
	case scrPHP:
		rows := m.phpRows()
		switch k {
		case "up", "k":
			m.phpIdx = clamp(m.phpIdx-1, len(rows))
		case "down", "j":
			m.phpIdx = clamp(m.phpIdx+1, len(rows))
		case "enter", "space":
			if len(rows) == 0 {
				return nil
			}
			e := rows[m.phpIdx]
			switch {
			case e.State == app.ExtAvailable:
				return m.shell("php", "ext", "install", e.Name)
			case e.State == app.ExtInstalled:
				return m.act("Enabling "+e.Name, func(ctx context.Context) error { return m.a.PHPExtEnable(ctx, e.Name) })
			case e.ByHampp:
				return m.act("Disabling "+e.Name, func(ctx context.Context) error { return m.a.PHPExtDisable(ctx, e.Name) })
			default:
				m.msg, m.msgErr = e.Name+" is enabled by its package; remove it with: hampp php ext remove "+e.Name, false
			}
		case "e":
			return m.shell("php", "edit")
		case "u":
			if rows := m.phpRows(); len(rows) > 0 && rows[m.phpIdx].Package != "" && rows[m.phpIdx].State != app.ExtAvailable {
				return m.shell("php", "ext", "remove", rows[m.phpIdx].Name)
			}
		case "esc", "q", "backspace":
			m.scr = scrDash
		}
	case scrEdit, scrHelp:
		if k == "esc" || k == "q" || k == "enter" || k == "backspace" {
			m.scr = scrDash
		}
		if m.scr == scrEdit && k == "v" {
			return m.shell("code", "start")
		}
	}
	return nil
}

func (m *model) dashKey(k string) tea.Cmd {
	switch k {
	case "q", "esc":
		return tea.Quit
	case "tab", "shift+tab":
		m.focus = 1 - m.focus
	case "up", "k":
		if m.focus == 0 {
			m.svcIdx = clamp(m.svcIdx-1, len(m.statuses))
		} else {
			m.siteIdx = clamp(m.siteIdx-1, len(m.sites))
		}
	case "down", "j":
		if m.focus == 0 {
			m.svcIdx = clamp(m.svcIdx+1, len(m.statuses))
		} else {
			m.siteIdx = clamp(m.siteIdx+1, len(m.sites))
		}
	case "enter":
		m.openMenu()
	case "s", "x", "r":
		return m.lifecycleKey(k, false)
	// Shift always applies to every service, whatever is selected.
	case "S", "X", "R", "shift+s", "shift+x", "shift+r":
		return m.lifecycleKey(strings.ToLower(strings.TrimPrefix(k, "shift+")), true)
	case "o":
		s := m.defaultSite()
		if m.focus == 1 && len(m.sites) > 0 {
			s = m.sites[m.siteIdx]
		}
		return m.openSite(s, false)
	case "w":
		if m.a.Cfg.Web.Share {
			return m.shell("share", "off")
		}
		return m.shell("share", "on")
	case "l":
		m.scr, m.logIdx = scrLogs, 0
	case "n":
		m.scr, m.nodeIdx = scrNode, 0
		return m.loadRemote()
	case "c":
		m.scr = scrSSL
		if m.a.Cfg.Web.HTTPS {
			return m.loadSSL()
		}
	case "d":
		m.scr = scrDoctor
		return m.loadDoctor()
	case "e":
		m.scr = scrEdit
	case "p":
		m.scr, m.phpIdx = scrPHP, 0
		return m.loadPHP()
	case "?":
		m.scr = scrHelp
	}
	return nil
}

// target returns the service s/x/r act on: the selected one while the Services
// panel is focused, or every service (nil) otherwise.
func (m *model) target(all bool) []string {
	if all || m.focus != 0 || len(m.statuses) == 0 {
		return nil
	}
	return []string{m.statuses[m.svcIdx].Name}
}

// targetLabel names that target for the footer.
func (m *model) targetLabel() string {
	if t := m.target(false); t != nil {
		return t[0]
	}
	return "all"
}

// lifecycleKey runs start/stop/reload on the selected service or on all of them.
func (m *model) lifecycleKey(k string, all bool) tea.Cmd {
	names := m.target(all)
	label := "all"
	if names != nil {
		label = names[0]
	}
	switch k {
	case "s":
		return m.act("Starting "+label, func(ctx context.Context) error { return m.a.Start(ctx, names) })
	case "x":
		return m.act("Stopping "+label, func(ctx context.Context) error { return m.a.Stop(ctx, names) })
	}
	return m.act("Reloading "+label, func(ctx context.Context) error {
		if names == nil {
			return m.a.Reload(ctx)
		}
		return m.a.ReloadService(ctx, names[0])
	})
}

func (m *model) defaultSite() site.Site {
	for _, s := range m.sites {
		if s.Default {
			return s
		}
	}
	return site.Site{Host: "localhost", Default: true}
}

func (m *model) openSite(s site.Site, https bool) tea.Cmd {
	port := m.a.Cfg.Web.Port
	if https {
		port = m.a.Cfg.Web.HTTPSPort
	}
	url := s.URL(port, https)
	return m.act("Opening "+url, func(ctx context.Context) error { return termux.OpenURL(ctx, m.a.R, url) })
}

func (m *model) openMenu() {
	m.prev, m.scr, m.menuIdx = scrDash, scrMenu, 0
	if m.focus == 0 && len(m.statuses) > 0 {
		st := m.statuses[m.svcIdx]
		m.menuTitle = st.Name + " · " + st.Title
		name := st.Name
		switch name {
		case "code":
			m.menu = []menuItem{
				{"Start VS Code (installs if needed)", func(m *model) tea.Cmd { return m.shell("code", "start") }},
				{"Stop", func(m *model) tea.Cmd { return m.svcAction("Stopping", "stop", name) }},
				{"Show log", func(m *model) tea.Cmd { return m.showLog(name) }},
			}
		case "mirror":
			m.menu = []menuItem{
				{"Turn mirror on (/sdcard/www → ~/www)", func(m *model) tea.Cmd { return m.shell("mirror", "on") }},
				{"Turn mirror off", func(m *model) tea.Cmd { return m.shell("mirror", "off") }},
				{"Show log", func(m *model) tea.Cmd { return m.showLog(name) }},
			}
		default:
			m.menu = []menuItem{
				{"Start", func(m *model) tea.Cmd { return m.svcAction("Starting", "start", name) }},
				{"Stop", func(m *model) tea.Cmd { return m.svcAction("Stopping", "stop", name) }},
				{"Restart", func(m *model) tea.Cmd { return m.svcAction("Restarting", "restart", name) }},
				{"Show log", func(m *model) tea.Cmd { return m.showLog(name) }},
			}
			if name == "db" && m.a.Cfg.DB.Engine == config.DBMariaDB {
				m.menu = append(m.menu, menuItem{"Connection details", func(m *model) tea.Cmd {
					m.msg, m.msgErr = strings.Join(m.a.DBInfo(), "\n"), false
					return nil
				}})
			}
		}
		return
	}
	if len(m.sites) == 0 {
		m.scr = scrDash
		return
	}
	s := m.sites[m.siteIdx]
	m.menuTitle = s.Host
	m.menu = []menuItem{
		{"Open  " + s.URL(m.a.Cfg.Web.Port, false), func(m *model) tea.Cmd { return m.openSite(s, false) }},
	}
	if m.a.Cfg.Web.HTTPS {
		m.menu = append(m.menu, menuItem{"Open  " + s.URL(m.a.Cfg.Web.HTTPSPort, true), func(m *model) tea.Cmd { return m.openSite(s, true) }})
	}
	m.menu = append(m.menu,
		menuItem{"How to edit these files", func(m *model) tea.Cmd { m.scr = scrEdit; return nil }},
		menuItem{"Show path  " + m.a.Paths.Short(s.DocRoot), func(m *model) tea.Cmd {
			m.msg, m.msgErr = s.DocRoot, false
			return nil
		}},
	)
	if s.Linked {
		name := s.Name
		m.menu = append(m.menu, menuItem{"Unlink", func(m *model) tea.Cmd {
			return m.act("Unlinking", func(ctx context.Context) error { return m.a.SiteUnlink(ctx, name) })
		}})
	}
}

func (m *model) svcAction(label, verb, name string) tea.Cmd {
	return m.act(label+" "+name, func(ctx context.Context) error {
		switch verb {
		case "start":
			return m.a.Start(ctx, []string{name})
		case "stop":
			return m.a.Stop(ctx, []string{name})
		}
		return m.a.Restart(ctx, []string{name})
	})
}

func (m *model) showLog(name string) tea.Cmd {
	for i, n := range m.logServices() {
		if n == name {
			m.logIdx = i
		}
	}
	m.scr = scrLogs
	return nil
}

func (m *model) logServices() []string {
	var names []string
	for _, s := range m.a.All() {
		names = append(names, s.Name())
	}
	return names
}

// nodeRows merges installed and available majors, newest first.
func (m *model) nodeRows() []int {
	seen := map[int]bool{}
	var rows []int
	for _, n := range append(append([]int{}, m.nodeSt.Installed...), m.remote...) {
		if !seen[n] {
			seen[n] = true
			rows = append(rows, n)
		}
	}
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j] > rows[j-1]; j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
	return append(rows, 0) // 0 = system
}

func (m *model) nodeKey(k string) tea.Cmd {
	rows := m.nodeRows()
	switch k {
	case "up", "k":
		m.nodeIdx = clamp(m.nodeIdx-1, len(rows))
	case "down", "j":
		m.nodeIdx = clamp(m.nodeIdx+1, len(rows))
	case "enter":
		major := rows[m.nodeIdx]
		if major != 0 && !contains(m.nodeSt.Installed, major) {
			return m.shell("node", "install", strconv.Itoa(major))
		}
		return m.act("Switching Node.js", func(context.Context) error { return m.a.NodeUse(major) })
	case "i":
		if major := rows[m.nodeIdx]; major != 0 {
			return m.shell("node", "install", strconv.Itoa(major))
		}
	case "u":
		if major := rows[m.nodeIdx]; major != 0 && contains(m.nodeSt.Installed, major) {
			return m.shell("node", "uninstall", strconv.Itoa(major))
		}
	case "a":
		return m.shell("node", "ls-remote")
	case "esc", "q", "backspace":
		m.scr = scrDash
	}
	return nil
}

func contains(ns []int, n int) bool {
	for _, x := range ns {
		if x == n {
			return true
		}
	}
	return false
}

// ---- view -----------------------------------------------------------------

func (m *model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "hampp"
	return v
}

func (m *model) width() int {
	return max(30, min(m.w, 72))
}

func (m *model) render() string {
	ellipsis = m.g.ell
	var body, keys string
	switch m.scr {
	case scrMenu:
		body = m.viewMenu()
		keys = footer(m.width(), m.g.updown+" choose", "enter ok", "esc back")
	case scrNode:
		body = m.viewNode()
		keys = footer(m.width(), "enter use", "i install", "u uninstall", "esc back")
	case scrSSL:
		body = m.viewSSL()
		keys = footer(m.width(), "t trust", "o open Settings", "s re-check", "esc back")
	case scrDoctor:
		body = m.viewDoctor()
		keys = footer(m.width(), "f fix v1 leftovers", "r re-run", "esc back")
	case scrLogs:
		body = m.viewLogs()
		keys = footer(m.width(), "tab next", "f follow", "esc back")
	case scrEdit:
		body = frame(m.g, m.width(), "Edit your files", "", []section{{lines: wrap(strings.Join(m.a.EditGuide(), "\n"), m.width()-4)}})
		keys = footer(m.width(), "v start VS Code", "esc back")
	case scrPHP:
		body = m.viewPHP()
		keys = footer(m.width(), "enter on/off/install", "e edit php.ini", "u remove", "esc back")
	case scrHelp:
		body = m.viewHelp()
		keys = footer(m.width(), "esc back")
	default:
		body = m.viewDash()
		t := m.targetLabel()
		keys = footer(m.width(), "s start "+t, "x stop "+t, "r reload "+t, "S X R all",
			"o open", "p php", "n node", "c ssl", "l logs", "d doctor", "? help", "q quit")
	}
	out := body + "\n" + keys
	if m.busy != "" {
		out += "\n " + yellow.Render(m.g.dot+" "+m.busy+"…")
	} else if m.msg != "" {
		style := faint
		if m.msgErr {
			style = red
		}
		lines := wrap(m.msg, m.width()-2)
		if max := m.h - lipglossHeight(out) - 1; max > 0 && len(lines) > max {
			lines = lines[len(lines)-max:]
		}
		for _, l := range lines {
			out += "\n " + style.Render(l)
		}
	}
	return out
}

func lipglossHeight(s string) int { return strings.Count(s, "\n") + 1 }

func (m *model) viewDash() string {
	w := m.width()
	inner := w - 4
	running := 0
	for _, s := range m.statuses {
		if s.Running {
			running++
		}
	}
	right := green.Render(fmt.Sprintf("%s %d running", m.g.on, running))
	if running == 0 {
		right = faint.Render(m.g.off + " stopped")
	}

	env := m.a.Env
	head := []string{faint.Render(strings.Join(nonEmpty(env.Model, "Android "+termux.AndroidVersion(env.SDK), "Termux "+env.InstallSource()), m.g.sep))}

	var svc []string
	for i, s := range m.statuses {
		cur := " "
		if m.focus == 0 && i == m.svcIdx {
			cur = cyan.Render(m.g.cursor)
		}
		dot, up := faint.Render(m.g.off), faint.Render("off")
		if s.Running {
			dot, up = green.Render(m.g.on), Uptime(time.Since(s.Since))
		}
		detail := s.Detail
		if !s.Running && (s.Name == "code" || s.Name == "mirror") {
			detail = ""
		}
		svc = append(svc, cur+dot+" "+cols(inner-3, []string{s.Name, s.Title, detail, up}, 7, 12, max(12, inner-3-7-12-6)))
	}

	var sites []string
	for i, s := range m.sites {
		cur := " "
		if m.focus == 1 && i == m.siteIdx {
			cur = cyan.Render(m.g.cursor)
		}
		tag := ""
		if s.Linked {
			tag = faint.Render(" link")
		}
		host := s.Host
		sites = append(sites, cur+" "+cols(inner-2, []string{host, m.a.Paths.Short(s.DocRoot) + tag}, min(20, max(12, inner/2))))
	}

	nodeLine := "  system"
	if m.nodeSt.Active > 0 {
		nodeLine = fmt.Sprintf("  v%d  (%s)", m.nodeSt.Active, node.Package(m.nodeSt.Active))
	} else if !m.nodeSt.HasSystem {
		nodeLine = faint.Render("  not installed · press n")
	}

	secs := []section{{lines: head}, {title: "Services", lines: svc}, {title: "Sites", lines: sites}, {title: "Node", lines: []string{nodeLine}}}
	if warn := m.warnings(); len(warn) > 0 {
		secs = append(secs, section{lines: warn})
	}
	return frame(m.g, w, "hampp", right, secs)
}

func (m *model) warnings() []string {
	var out []string
	if m.a.Env.SDK >= 31 {
		out = append(out, yellow.Render(m.g.warn+" Android "+termux.AndroidVersion(m.a.Env.SDK)+" may kill servers"), "  in the background. d "+m.g.arrow+" how to fix")
	}
	if m.a.Cfg.Web.Share {
		out = append(out, yellow.Render(m.g.warn+" Sharing on Wi-Fi is ON (w to turn off)"))
	}
	return out
}

func (m *model) viewMenu() string {
	var lines []string
	for i, it := range m.menu {
		if i == m.menuIdx {
			lines = append(lines, cyan.Render(m.g.cursor)+" "+sel.Render(it.label))
		} else {
			lines = append(lines, "  "+it.label)
		}
	}
	return frame(m.g, m.width(), m.menuTitle, "", []section{{lines: lines}})
}

func (m *model) viewNode() string {
	var lines []string
	for i, major := range m.nodeRows() {
		cur := "  "
		if i == m.nodeIdx {
			cur = cyan.Render(m.g.cursor) + " "
		}
		if major == 0 {
			state := faint.Render("not installed")
			if m.nodeSt.HasSystem {
				state = "installed"
			}
			if m.nodeSt.Active == 0 && m.nodeSt.HasSystem {
				state += " " + green.Render(m.g.on+" active")
			}
			lines = append(lines, cur+cols(m.width()-6, []string{"system", state}, 9))
			continue
		}
		state := faint.Render("available")
		if contains(m.nodeSt.Installed, major) {
			state = "installed"
			if major == m.nodeSt.Active {
				state += " " + green.Render(m.g.on+" active")
			}
		}
		lines = append(lines, cur+cols(m.width()-6, []string{strconv.Itoa(major), state}, 9))
	}
	info := []string{}
	if m.remoteNote != "" {
		info = append(info, wrap(m.remoteNote, m.width()-4)...)
	}
	if wd, err := os.Getwd(); err == nil {
		if sp, file, err := node.FindRC(wd); err == nil {
			want, rerr := sp.Resolve(m.nodeSt.Installed)
			status := green.Render("matches")
			if rerr != nil || want != m.nodeSt.Active {
				status = yellow.Render("run: hampp node use")
			}
			info = append(info, fmt.Sprintf("%s wants %s %s %s", m.a.Paths.Short(file), sp.Raw, m.g.arrow, status))
		}
	}
	info = append(info, wrap(faint.Render("nvm itself can't run on Termux. These are TUR packages (nodejs-NN) that hampp switches between."), m.width()-4)...)
	return frame(m.g, m.width(), "Node versions", "", []section{{lines: lines}, {lines: info}})
}

func (m *model) viewSSL() string {
	w := m.width()
	if !m.a.Cfg.Web.HTTPS {
		return frame(m.g, w, "Local HTTPS", "", []section{{lines: wrap("HTTPS is off. Press i to create a local certificate authority and certificates for every site.", w-4)}})
	}
	var status []string
	for _, c := range m.sslChecks {
		status = append(status, m.symbol(c.Level)+" "+c.Message)
	}
	if len(status) == 0 {
		status = []string{faint.Render("Checking…")}
	}
	steps := []string{"Trust it in Android (once, ~1 min):", "Press t: copies hampp-ca.crt to Downloads"}
	for i, s := range termux.CAInstallSteps(m.a.Env.SDK, "Downloads/hampp-ca.crt") {
		steps = append(steps, fmt.Sprintf("%d %s", i+1, s))
	}
	steps = append(steps, "", "Firefox:")
	steps = append(steps, termux.FirefoxCASteps()...)
	var wrapped []string
	for _, l := range status {
		wrapped = append(wrapped, wrap(l, w-4)...)
	}
	var wsteps []string
	for _, l := range steps {
		wsteps = append(wsteps, wrap(l, w-4)...)
	}
	return frame(m.g, w, "Local HTTPS", "", []section{{lines: wrapped}, {lines: wsteps}})
}

func (m *model) symbol(l app.Level) string {
	switch l {
	case app.OK:
		return green.Render(m.g.ok)
	case app.Warn:
		return yellow.Render(m.g.warn)
	}
	return red.Render(m.g.fail)
}

func (m *model) viewDoctor() string {
	w := m.width()
	var lines []string
	if m.doctorBusy && len(m.doctor) == 0 {
		lines = []string{faint.Render("Running checks…")}
	}
	for _, c := range m.doctor {
		for i, l := range wrap(c.Message, w-6) {
			if i == 0 {
				lines = append(lines, m.symbol(c.Level)+" "+l)
			} else {
				lines = append(lines, "  "+l)
			}
		}
		if c.Level != app.OK && c.Fix != "" {
			for _, l := range wrap(c.Fix, w-8) {
				lines = append(lines, faint.Render("  "+l))
			}
		}
	}
	if max := m.h - 5; max > 3 && len(lines) > max {
		lines = append(lines[:max-1], faint.Render("… run `hampp doctor` for the full list"))
	}
	return frame(m.g, w, "Doctor", "", []section{{lines: lines}})
}

func (m *model) viewLogs() string {
	w := m.width()
	names := m.logServices()
	m.logIdx = clamp(m.logIdx, len(names))
	var tabs []string
	for i, n := range names {
		if i == m.logIdx {
			tabs = append(tabs, sel.Render(" "+n+" "))
		} else {
			tabs = append(tabs, " "+n+" ")
		}
	}
	svcs, _ := m.a.Find([]string{names[m.logIdx]})
	c, _ := m.a.Ctx()
	var lines []string
	n := max(5, m.h-8)
	for _, f := range svcs[0].Logs(c) {
		t := service.Tail(f, n)
		if t == "" {
			continue
		}
		lines = append(lines, faint.Render("── "+m.a.Paths.Short(f)))
		for _, l := range strings.Split(t, "\n") {
			lines = append(lines, wrap(l, w-4)...)
		}
	}
	if len(lines) == 0 {
		lines = []string{faint.Render("No log output yet.")}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return frame(m.g, w, "Logs", "", []section{{lines: []string{strings.Join(tabs, "")}}, {lines: lines}})
}

func (m *model) viewPHP() string {
	w := m.width()
	var settings []string
	for _, k := range app.CommonPHPSettings {
		v, ok := m.phpVals[k]
		if !ok {
			continue
		}
		settings = append(settings, cols(w-4, []string{k, v[0]}, 22))
	}
	if len(settings) == 0 {
		settings = []string{faint.Render("Loading settings…")}
	}
	settings = append(settings, faint.Render("Change: hampp php set <key> <value>"), faint.Render("Websites column; the CLI may differ."))

	var exts []string
	for i, e := range m.phpRows() {
		cur := "  "
		if i == m.phpIdx {
			cur = cyan.Render(m.g.cursor) + " "
		}
		state := faint.Render("install")
		switch e.State {
		case app.ExtEnabled:
			state = green.Render(m.g.on + " on")
		case app.ExtInstalled:
			state = yellow.Render(m.g.off + " off")
		}
		exts = append(exts, cur+cols(w-6, []string{e.Name, state}, 16))
	}
	builtin := 0
	for _, e := range m.phpExts {
		if e.State == app.ExtBuiltIn {
			builtin++
		}
	}
	info := wrap(faint.Render(fmt.Sprintf("%d built-in extensions are always on (list: hampp php ext)", builtin)), w-4)
	if m.phpNote != "" {
		info = append(info, wrap(m.phpNote, w-4)...)
	}
	return frame(m.g, w, "PHP", "", []section{{lines: settings}, {title: "Extensions", lines: exts}, {lines: info}})
}

func (m *model) viewHelp() string {
	lines := []string{
		"Dashboard",
		"  tab     switch Services / Sites",
		"  " + pad(m.g.updown, 6) + "  move    enter  actions",
		"  s / x   start / stop the selected",
		"          service (or all, on Sites)",
		"  r       reload it (new folders, certs)",
		"  S X R   always apply to all services",
		"  o       open site in browser",
		"  w       share on Wi-Fi on/off",
		"  p       PHP settings & extensions",
		"  l       logs      n  Node.js",
		"  c       HTTPS     d  doctor",
		"  e       how to edit your files",
		"",
		"Sites",
		"  Every folder in ~/www becomes",
		"  http://<folder>.localhost:" + strconv.Itoa(m.a.Cfg.Web.Port),
		"",
		"Everything here also works as a command:",
		"  hampp --help",
	}
	return frame(m.g, m.width(), "Help", "", []section{{lines: lines}})
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" && !strings.HasSuffix(p, " unknown") && !strings.HasSuffix(p, " unknown source") {
			out = append(out, p)
		}
	}
	return out
}
