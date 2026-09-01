package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// messages
// ---------------------------------------------------------------------------

// snapshotLoadedMsg carries the (possibly failed) state load for all tabs.
type snapshotLoadedMsg struct {
	snap SnapshotData
	err  error
}

// serviceStatusMsg carries one service's actual state.
type serviceStatusMsg struct {
	name   string
	actual ServiceActual
	err    error
}

// siteHealthMsg carries one site's probe result.
type siteHealthMsg struct {
	siteID string
	health SiteHealthData
}

// actionStartedMsg is emitted when a mutation begins; Update answers it
// with the actual run command, so the running state renders before the
// (long) backend call starts — standard bubbletea two-phase idiom. cancel
// aborts the in-flight backend call when the user presses ctrl+c.
type actionStartedMsg struct {
	action pendingAction
	ctx    context.Context
	cancel context.CancelFunc
}

// actionDoneMsg carries the final outcome of a mutation.
type actionDoneMsg struct {
	label     string
	err       error
	cancelled bool
	result    ActionResult
}

// linkDoneMsg carries link-specific output (credentials).
type linkDoneMsg struct {
	label  string
	result LinkResult
	err    error
}

// refreshMsg asks every tab to reload its data.
type refreshMsg struct{}

// batchMsg lets one Cmd deliver several messages at once.
type batchMsg []tea.Msg

// ---------------------------------------------------------------------------
// root model
// ---------------------------------------------------------------------------

type tabID int

const (
	tabStatus tabID = iota
	tabSites
	tabPHP
	tabServices
	tabActivity
	tabCount
)

func (t tabID) title() string {
	switch t {
	case tabStatus:
		return "Status"
	case tabSites:
		return "Sites"
	case tabPHP:
		return "PHP"
	case tabServices:
		return "Services"
	case tabActivity:
		return "Activity"
	}
	return "?"
}

type rootModel struct {
	backend   Backend
	active    tabID
	width     int
	height    int
	snap      SnapshotData
	snapErr   error
	loaded    bool
	status    map[string]ServiceActual // actual systemd state per service
	statusErr map[string]error
	health    map[string]SiteHealthData // site health per site ID
	running   *Action                   // in-flight mutation, nil when idle
	overlay   overlayModel
	log       []logEntry

	// per-tab list cursors (clamped against list length on every render)
	cursorSites    int
	cursorPHP      int
	cursorServices int
}

type logEntry struct {
	time   string
	label  string
	status string // ok | changed | noop | failed | cancelled | info
	detail string
}

func newRootModel(b Backend) rootModel {
	return rootModel{
		backend:   b,
		active:    tabStatus,
		status:    map[string]ServiceActual{},
		statusErr: map[string]error{},
		health:    map[string]SiteHealthData{},
		overlay:   newOverlayModel(),
	}
}

// Init loads the initial snapshot and the per-service actual states.
func (m rootModel) Init() tea.Cmd {
	return tea.Batch(m.loadAll(), m.overlay.Init())
}

func (m rootModel) loadAll() tea.Cmd {
	return tea.Batch(m.loadSnapshot(), m.loadStatuses(), m.loadSiteHealth())
}

func (m rootModel) loadSnapshot() tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		snap, err := b.Snapshot(context.Background())
		return snapshotLoadedMsg{snap: snap, err: err}
	}
}

func (m rootModel) loadStatuses() tea.Cmd {
	b := m.backend
	services := serviceNames(m)
	return func() tea.Msg {
		msgs := make([]tea.Msg, 0, len(services))
		for _, name := range services {
			actual, err := b.ServiceStatus(context.Background(), name)
			msgs = append(msgs, serviceStatusMsg{name: name, actual: actual, err: err})
		}
		if len(msgs) == 0 {
			return nil
		}
		if len(msgs) == 1 {
			return msgs[0]
		}
		return batchMsg(msgs)
	}
}

func (m rootModel) loadSiteHealth() tea.Cmd {
	b := m.backend
	sites := m.snap.Sites
	return func() tea.Msg {
		msgs := []tea.Msg{}
		for _, s := range sites {
			h, err := b.SiteHealth(context.Background(), s.Domain, s.ID, s.Enabled)
			if err != nil {
				continue
			}
			msgs = append(msgs, siteHealthMsg{siteID: s.ID, health: h})
		}
		if len(msgs) == 0 {
			return nil
		}
		if len(msgs) == 1 {
			return msgs[0]
		}
		return batchMsg(msgs)
	}
}

// serviceNames returns the allowlisted services (from the snapshot when
// available; the static allowlist otherwise).
func serviceNames(m rootModel) []string {
	if m.loaded {
		out := make([]string, 0, len(m.snap.Services))
		for _, s := range m.snap.Services {
			out = append(out, s.Name)
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{"nginx", "mariadb", "valkey"}
}

// ---------------------------------------------------------------------------
// update
// ---------------------------------------------------------------------------

func (m rootModel) Update(msg tea.Msg) (rootModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		// While an overlay (confirm/select/form) is open it owns the keys.
		if m.overlay.Active() {
			newOverlay, _ := m.overlay.Update(msg)
			m.overlay = newOverlay
			if finished := m.overlay.Finished(); finished != nil {
				m.overlay = newOverlayModel()
				if c := m.handleOverlayResult(finished); c != nil {
					cmds = append(cmds, c)
				}
			}
			return m, tea.Batch(cmds...)
		}
		// While a mutation is in flight only Ctrl+C (cancel) and quit keys
		// are accepted so tab state cannot race the rebuild.
		if m.running != nil {
			switch msg.String() {
			case "ctrl+c":
				m.running.Cancel() // aborts the backend context; rollback unwinds
				m.running.Cancelled = true
				m.log = appendLog(m.log, logEntry{time: stamp(), label: m.running.Label, status: "cancelled", detail: "cancelling… (transaction rolls back)"})
				return m, nil
			}
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "r":
			return m, m.loadAll()
		case "1":
			m.active = tabStatus
		case "2":
			m.active = tabSites
		case "3":
			m.active = tabPHP
		case "4":
			m.active = tabServices
		case "5":
			m.active = tabActivity
		case "tab", "right":
			m.active = (m.active + 1) % tabCount
		case "shift+tab", "left":
			m.active = (m.active + tabCount - 1) % tabCount
		default:
			var c tea.Cmd
			m, c = m.handleTabKey(msg)
			cmds = append(cmds, c)
		}
		return m, tea.Batch(cmds...)

	case snapshotLoadedMsg:
		m.snap = msg.snap
		m.snapErr = msg.err
		m.loaded = true
		return m, m.loadSiteHealth()

	case serviceStatusMsg:
		if msg.err != nil {
			m.statusErr[msg.name] = msg.err
		} else {
			delete(m.statusErr, msg.name)
			m.status[msg.name] = msg.actual
		}

	case siteHealthMsg:
		m.health[msg.siteID] = msg.health

	case actionStartedMsg:
		m.running = &Action{Label: msg.action.label, Start: now(), cancel: msg.cancel}
		m.log = appendLog(m.log, logEntry{time: stamp(), label: msg.action.label, status: "info", detail: "started"})
		return m, runActionCmd(m.backend, msg.action, msg.ctx, msg.cancel)

	case actionDoneMsg:
		m.running = nil
		status, detail := actionLogStatus(msg.err, msg.cancelled, msg.result)
		m.log = appendLog(m.log, logEntry{time: stamp(), label: msg.label, status: status, detail: detail})
		// every mutation may have changed YAML: reload everything
		return m, m.loadAll()

	case linkDoneMsg:
		m.running = nil
		if msg.err != nil {
			m.log = appendLog(m.log, logEntry{time: stamp(), label: msg.label, status: "failed", detail: msg.err.Error()})
		} else {
			detail := msg.result.Phase
			if msg.result.Database != "" {
				detail = fmt.Sprintf("%s · db: %s (user: %s, password: %s)", msg.result.Phase, msg.result.Database, msg.result.DBUser, msg.result.DBPassword)
			}
			m.log = appendLog(m.log, logEntry{time: stamp(), label: msg.label, status: "changed", detail: detail})
		}
		return m, m.loadAll()

	case refreshMsg:
		return m, m.loadAll()

	case batchMsg:
		for _, sub := range msg {
			var cmd tea.Cmd
			m, cmd = m.Update(sub)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}

	return m, tea.Batch(cmds...)
}

// handleTabKey dispatches per-tab navigation and action keys.
func (m rootModel) handleTabKey(msg tea.KeyMsg) (rootModel, tea.Cmd) {
	// list navigation first: it never conflicts with action keys
	switch msg.String() {
	case "up", "k":
		m = m.moveCursor(-1)
		return m, nil
	case "down", "j":
		m = m.moveCursor(1)
		return m, nil
	}
	switch m.active {
	case tabSites:
		switch msg.String() {
		case "a":
			om := newOverlayModel()
			om.openForm(m.linkFormSpec())
			m.overlay = om
			return m, nil
		case "d":
			if site, ok := m.selectedSite(); ok {
				m.overlay = newOverlayModel()
				m.overlay.openConfirm(
					fmt.Sprintf("Unlink %s (%s)?", site.Domain, site.ID),
					"This removes only the vhost, FPM pool and site manifest.\nProject files, databases and data are kept.", site.Enabled)
				m.overlay.pending = pendingAction{kind: actionUnlink, label: "unlink " + site.Domain, arg: site.Domain}
				return m, nil
			}
		case "enter", "p":
			// re-probe the selected site's health
			if site, ok := m.selectedSite(); ok {
				return m, m.probeSite(site)
			}
		}
	case tabPHP:
		if v, ok := m.selectedPHP(); ok {
			switch msg.String() {
			case "d":
				m.overlay = newOverlayModel()
				m.overlay.openConfirm(
					fmt.Sprintf("Uninstall PHP %s?", v),
					"Versions used by linked sites cannot be removed.", true)
				m.overlay.pending = pendingAction{kind: actionPHPUninstall, label: "php uninstall " + v, arg: v}
				return m, nil
			case "u":
				m.overlay = newOverlayModel()
				m.overlay.openConfirm(
					fmt.Sprintf("Set PHP %s as global default?", v),
					"New shells will activate this version.", true)
				m.overlay.pending = pendingAction{kind: actionPHPUseGlobal, label: "php use --global " + v, arg: v}
				return m, nil
			}
		}
		if msg.String() == "a" {
			if vers := m.availablePHP(); len(vers) > 0 {
				om := newOverlayModel()
				om.openSelect("Install PHP version", vers, pendingAction{kind: actionPHPInstall, label: "php install", argPrefix: true})
				m.overlay = om
			}
			return m, nil
		}
		if msg.String() == "x" {
			if exts := m.availableExts(); len(exts) > 0 {
				om := newOverlayModel()
				om.openSelect("Install PHP extension", exts, pendingAction{kind: actionPHPExt, label: "php ext install", argPrefix: true})
				m.overlay = om
			}
			return m, nil
		}
	case tabServices:
		if svc, ok := m.selectedService(); ok {
			switch msg.String() {
			case "i":
				if !svc.Installed {
					return m, m.startAction(pendingAction{kind: actionService, action: "install", label: fmt.Sprintf("%s install", svc.Name), arg: svc.Name})
				}
			case "s":
				if svc.Installed {
					return m, m.startAction(pendingAction{kind: actionService, action: "start", label: fmt.Sprintf("%s start", svc.Name), arg: svc.Name})
				}
			case "x":
				if svc.Installed {
					m.overlay = newOverlayModel()
					m.overlay.openConfirm(
						fmt.Sprintf("Stop %s?", svc.Name),
						stopWarning(m, svc.Name), true)
					m.overlay.pending = pendingAction{kind: actionService, action: "stop", label: fmt.Sprintf("%s stop", svc.Name), arg: svc.Name}
					return m, nil
				}
			case "R", "enter":
				if svc.Installed && svc.DesiredState == "running" {
					return m, m.startAction(pendingAction{kind: actionService, action: "restart", label: fmt.Sprintf("%s restart", svc.Name), arg: svc.Name})
				}
			}
		}
	}
	return m, nil
}

// stopWarning reproduces the CLI's nginx-with-sites warning in overlay copy.
func stopWarning(m rootModel, name string) string {
	if name == "nginx" && len(m.snap.Sites) > 0 {
		return fmt.Sprintf("Warning: %d enabled site(s) will stop serving traffic.", len(m.snap.Sites))
	}
	return "Service data is kept; starting again is non-destructive."
}

// selected helpers navigate per-tab cursor state (kept here for v1 as the
// first row / first item; per-tab cursors refine this in later iterations).

func (m rootModel) selectedSite() (SiteData, bool) {
	if len(m.snap.Sites) == 0 {
		return SiteData{}, false
	}
	i := minInt(m.cursorSites, len(m.snap.Sites)-1)
	return m.snap.Sites[i], true
}

func (m rootModel) selectedPHP() (string, bool) {
	if len(m.snap.PHPInstalled) == 0 {
		return "", false
	}
	i := minInt(m.cursorPHP, len(m.snap.PHPInstalled)-1)
	return m.snap.PHPInstalled[i], true
}

func (m rootModel) selectedService() (ServiceConfigData, bool) {
	if len(m.snap.Services) == 0 {
		return ServiceConfigData{}, false
	}
	i := minInt(m.cursorServices, len(m.snap.Services)-1)
	return m.snap.Services[i], true
}

func (m rootModel) availablePHP() []string {
	var out []string
	for _, v := range phpCatalogVersions {
		if !containsString(m.snap.PHPInstalled, v) {
			out = append(out, v)
		}
	}
	return out
}

func (m rootModel) availableExts() []string {
	var out []string
	for _, e := range phpCatalogExtensions {
		if !containsString(m.snap.Extensions, e) {
			out = append(out, e)
		}
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// handleOverlayResult dispatches a finished overlay (confirm/select/form).
func (m rootModel) handleOverlayResult(f overlayFinished) tea.Cmd {
	switch f := f.(type) {
	case confirmFinished:
		if !f.confirmed {
			return nil
		}
		pa := f.pending
		pa.label = withArg(f.pending.label, f.pending.arg)
		return m.startAction(pa)
	case selectFinished:
		if f.value == "" {
			return nil
		}
		pa := f.pending
		pa.arg = f.value
		pa.label = f.pending.label + " " + f.value
		return m.startAction(pa)
	case formFinished:
		if f.cancelled {
			return nil
		}
		pa := f.pending
		pa.req = f.req
		pa.label = "link " + f.req.Domain
		return m.startAction(pa)
	}
	return nil
}

func withArg(label, arg string) string {
	if arg == "" {
		return label
	}
	return label + " " + arg
}

// moveCursor shifts the active tab's list cursor, clamped to bounds.
func (m rootModel) moveCursor(delta int) rootModel {
	switch m.active {
	case tabSites:
		m.cursorSites = clamp(m.cursorSites+delta, 0, maxInt(len(m.snap.Sites)-1, 0))
	case tabPHP:
		m.cursorPHP = clamp(m.cursorPHP+delta, 0, maxInt(len(m.snap.PHPInstalled)-1, 0))
	case tabServices:
		m.cursorServices = clamp(m.cursorServices+delta, 0, maxInt(len(m.snap.Services)-1, 0))
	}
	return m
}

// probeSite re-runs the health check for one site and stores the result.
func (m rootModel) probeSite(site SiteData) tea.Cmd {
	b := m.backend
	domain, id, enabled := site.Domain, site.ID, site.Enabled
	return func() tea.Msg {
		h, err := b.SiteHealth(context.Background(), domain, id, enabled)
		if err != nil {
			return nil
		}
		return siteHealthMsg{siteID: id, health: h}
	}
}

// startAction launches a backend mutation: the first message marks the
// action as running (and Update answers with the real run command). The
// cancellable context it creates is stored on the Action so ctrl+c can
// abort the backend call mid-flight.
func (m rootModel) startAction(a pendingAction) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		return actionStartedMsg{action: a, ctx: ctx, cancel: cancel}
	}
}

// runActionCmd performs the backend call and reports the outcome. The
// caller-supplied cancel makes ctrl+c abort the in-flight CLI run; the
// transaction rollback path unwinds any applied rebuild steps, so the
// host never keeps partial state.
func runActionCmd(b Backend, a pendingAction, ctx context.Context, cancel context.CancelFunc) tea.Cmd {
	label := a.label
	return func() tea.Msg {
		defer cancel() // release the context when the call finishes
		var (
			res ActionResult
			lr  LinkResult
			err error
		)
		switch a.kind {
		case actionPHPInstall, actionPHPUninstall, actionPHPUseGlobal, actionPHPExt:
			switch a.kind {
			case actionPHPInstall:
				res, err = b.PHPInstall(ctx, a.arg)
			case actionPHPUninstall:
				res, err = b.PHPUninstall(ctx, a.arg)
			case actionPHPUseGlobal:
				res, err = b.PHPUseGlobal(ctx, a.arg)
			case actionPHPExt:
				res, err = b.PHPExtInstall(ctx, a.arg)
			}
		case actionService:
			res, err = b.ServiceAction(ctx, a.arg, a.action)
		case actionUnlink:
			res, err = b.UnlinkSite(ctx, a.arg)
		case actionLink:
			lr, err = b.LinkSite(ctx, a.req)
			if err == nil {
				return linkDoneMsg{label: label, result: lr}
			}
		}
		if err == nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		return actionDoneMsg{label: label, err: err, cancelled: ctx.Err() == context.Canceled, result: res}
	}
}

func actionLogStatus(err error, cancelled bool, res ActionResult) (status, detail string) {
	switch {
	case cancelled:
		return "cancelled", "cancelled"
	case err != nil:
		return "failed", err.Error()
	case res.Changed:
		return "changed", res.Phase
	default:
		return "noop", res.Phase
	}
}

func appendLog(log []logEntry, e logEntry) []logEntry {
	if len(log) >= 200 {
		log = log[1:]
	}
	return append(log, e)
}

func stamp() string {
	return now().Format("15:04:05")
}

// ---------------------------------------------------------------------------
// view
// ---------------------------------------------------------------------------

func (m rootModel) View() string {
	if m.width == 0 {
		return "loading…"
	}
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n\n")
	b.WriteString(m.body())
	b.WriteString("\n")
	b.WriteString(m.footer())
	return b.String()
}

func (m rootModel) header() string {
	tabs := make([]string, 0, tabCount)
	for i := tabID(0); i < tabCount; i++ {
		style := theme.inactive
		if i == m.active {
			style = theme.active
		}
		tabs = append(tabs, style.Render(fmt.Sprintf("[%d] %s", i+1, i.title())))
	}
	row := theme.brand.Render("NixCP")
	if m.running != nil {
		row += " " + theme.info.Render("⟳ "+m.running.Label+" ("+firstNonEmpty(m.running.Phase, "running")+")")
	}
	if m.snap.Configured {
		right := theme.subtle.Render(fmt.Sprintf("%d sites · php %s", len(m.snap.Sites), phpDefaultLabel(m)))
		row += strings.Repeat(" ", maxInt(1, m.width-lipgloss.Width(row)-lipgloss.Width(right))) + right
	}
	tabsRow := strings.Join(tabs, "  ")
	if m.width < lipgloss.Width(tabsRow) {
		tabsRow = strings.Join(tabs, " ")
	}
	return tabsRow + "\n" + theme.subtle.Render(strings.Repeat("─", minInt(m.width, 80)))
}

func (m rootModel) body() string {
	if !m.loaded {
		return theme.subtle.Render("loading state…")
	}
	if m.snapErr != nil || !m.snap.Configured {
		return m.boxView("Not configured",
			"NixCP is not initialized.",
			"",
			"Run "+theme.title.Render("ncp install")+" from a terminal,",
			"add the printed import line to your NixOS configuration,",
			"then come back here.",
			"",
			theme.subtle.Render("r refresh · q quit"))
	}
	switch m.active {
	case tabStatus:
		return m.viewStatus()
	case tabSites:
		return m.viewSites()
	case tabPHP:
		return m.viewPHP()
	case tabServices:
		return m.viewServices()
	case tabActivity:
		return m.viewActivity()
	}
	return ""
}

func (m rootModel) footer() string {
	var b strings.Builder
	b.WriteString(m.contextBar())
	b.WriteString("\n")
	b.WriteString(theme.helpBar.Render("q quit · 1-5 tabs · r refresh · ctrl+c cancel action"))
	return b.String()
}

func (m rootModel) contextBar() string {
	switch m.active {
	case tabStatus:
		return theme.helpBar.Render("↑↓ inspect · r refresh")
	case tabSites:
		return theme.helpBar.Render("a link · d unlink · enter health probe · r refresh")
	case tabPHP:
		return theme.helpBar.Render("a install version · x install ext · u use global · d uninstall · r refresh")
	case tabServices:
		return theme.helpBar.Render("i install · s start · x stop · enter restart · r refresh")
	case tabActivity:
		return theme.helpBar.Render("r refresh")
	}
	return ""
}

func (m rootModel) boxView(title string, lines ...string) string {
	inner := strings.Join(lines, "\n")
	return theme.box.Render(theme.title.Render(title) + "\n\n" + inner)
}

func phpDefaultLabel(m rootModel) string {
	if m.snap.PHPDefault == "" {
		return "—"
	}
	return m.snap.PHPDefault
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
