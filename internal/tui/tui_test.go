package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// fakeBackend is a scriptable Backend for model tests.
type fakeBackend struct {
	snap         SnapshotData
	snapErr      error
	actuals      map[string]ServiceActual
	statusErrs   map[string]error
	healths      map[string]SiteHealthData
	actions      []string // recorded "kind:arg:action" calls
	actionErr    error
	actionResult ActionResult
	linkResult   LinkResult
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		snap: SnapshotData{
			Configured:   true,
			Schema:       2,
			Owner:        "dev",
			StateRoot:    "/home/dev/.nixcp",
			RebuildMode:  "switch",
			PHPInstalled: []string{"8.3", "8.4"},
			PHPDefault:   "8.4",
			Extensions:   []string{"redis"},
			Services: []ServiceConfigData{
				{Name: "nginx", Installed: true, DesiredState: "running"},
				{Name: "mariadb", Installed: false, DesiredState: "stopped"},
				{Name: "valkey", Installed: true, DesiredState: "running"},
			},
			Sites: []SiteData{{
				ID: "abc123", Domain: "app.test", Enabled: true, PHP: "8.4",
				ProjectPath: "/home/dev/app", DocumentRoot: "/home/dev/app/public",
				HandlerType: "template", HandlerName: "laravel", Database: "app",
			}},
		},
		actuals: map[string]ServiceActual{
			"nginx":  {Active: true, Enabled: true, Health: "ok"},
			"valkey": {Active: true, Enabled: true, Health: "ok"},
		},
		statusErrs: map[string]error{},
		healths: map[string]SiteHealthData{
			"abc123": {SocketOK: true, HTTPOK: true, HTTPStatus: 200, Healthy: true, Describe: "app.test: healthy"},
		},
	}
}

func (f *fakeBackend) Snapshot(ctx context.Context) (SnapshotData, error) { return f.snap, f.snapErr }
func (f *fakeBackend) ServiceStatus(ctx context.Context, name string) (ServiceActual, error) {
	if err, ok := f.statusErrs[name]; ok {
		return ServiceActual{}, err
	}
	return f.actuals[name], nil
}
func (f *fakeBackend) SiteHealth(ctx context.Context, domain, siteID string, enabled bool) (SiteHealthData, error) {
	return f.healths[siteID], nil
}
func (f *fakeBackend) record(kind, arg, action string) {
	f.actions = append(f.actions, kind+":"+arg+":"+action)
}
func (f *fakeBackend) PHPInstall(ctx context.Context, v string) (ActionResult, error) {
	f.record("php-install", v, "")
	if f.actionErr != nil {
		return ActionResult{}, f.actionErr
	}
	return f.actionResult, nil
}
func (f *fakeBackend) PHPUninstall(ctx context.Context, v string) (ActionResult, error) {
	f.record("php-uninstall", v, "")
	if f.actionErr != nil {
		return ActionResult{}, f.actionErr
	}
	return f.actionResult, nil
}
func (f *fakeBackend) PHPUseGlobal(ctx context.Context, v string) (ActionResult, error) {
	f.record("php-use-global", v, "")
	if f.actionErr != nil {
		return ActionResult{}, f.actionErr
	}
	return f.actionResult, nil
}
func (f *fakeBackend) PHPExtInstall(ctx context.Context, ext string) (ActionResult, error) {
	f.record("php-ext", ext, "")
	if f.actionErr != nil {
		return ActionResult{}, f.actionErr
	}
	return f.actionResult, nil
}
func (f *fakeBackend) ServiceAction(ctx context.Context, svc, action string) (ActionResult, error) {
	f.record("service", svc, action)
	if f.actionErr != nil {
		return ActionResult{}, f.actionErr
	}
	return f.actionResult, nil
}
func (f *fakeBackend) LinkSite(ctx context.Context, req LinkRequest) (LinkResult, error) {
	f.record("link", req.Domain, "")
	if f.actionErr != nil {
		return LinkResult{}, f.actionErr
	}
	return f.linkResult, nil
}
func (f *fakeBackend) UnlinkSite(ctx context.Context, domain string) (ActionResult, error) {
	f.record("unlink", domain, "")
	if f.actionErr != nil {
		return ActionResult{}, f.actionErr
	}
	return f.actionResult, nil
}

// -----------------------------------------------------------------------------
// model lifecycle
// -----------------------------------------------------------------------------

// loadModel drives a fresh root model through its async load messages until
// the backend data lands, returning the model the TUI would render.
func loadModel(t *testing.T, b *fakeBackend) rootModel {
	t.Helper()
	m := newRootModel(b)
	m.width, m.height = 100, 30
	pump(t, &m, m.Init())
	return m
}

// pump executes a command tree synchronously, feeding every produced
// message back into the model (and running any command the model answers
// with), until nothing is left. It expands bubbletea's internal batch and
// sequence messages so tests observe the full message cascade.
func pump(t *testing.T, m *rootModel, cmd tea.Cmd) {
	t.Helper()
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			return
		}
		switch msg := msg.(type) {
		case tea.BatchMsg:
			for _, sub := range msg {
				pump(t, m, sub)
			}
			return
		default:
			var next tea.Cmd
			*m, next = m.Update(msg)
			cmd = next
		}
	}
}

// collectMsgs executes a Cmd (or batch) synchronously and gathers messages.
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	switch msg := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, sub := range msg {
			out = append(out, collectMsgs(sub)...)
		}
		return out
	default:
		return []tea.Msg{msg}
	}
}

func TestRootModelLoadsSnapshotAndRendersTabs(t *testing.T) {
	b := newFakeBackend()
	m := loadModel(t, b)
	if !m.loaded {
		t.Fatalf("snapshot not loaded")
	}
	if m.snapErr != nil {
		t.Fatalf("unexpected snapshot error: %v", m.snapErr)
	}

	// tab header must mention all five tabs
	m.active = tabStatus
	view := m.View()
	for _, title := range []string{"Status", "Sites", "PHP", "Services", "Activity"} {
		if !strings.Contains(view, title) {
			t.Fatalf("view missing tab %q", title)
		}
	}
	// status tab shows service + site + php info
	if !strings.Contains(view, "nginx") || !strings.Contains(view, "app.test") || !strings.Contains(view, "8.4") {
		t.Fatalf("status view missing expected data:\n%s", view)
	}
}

func TestRootModelTabSwitching(t *testing.T) {
	b := newFakeBackend()
	m := loadModel(t, b)

	for i, key := range []string{"1", "2", "3", "4", "5"} {
		var cmd tea.Cmd
		m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyType(tea.KeyRunes), Runes: []rune(key)})
		_ = collectMsgs(cmd)
		if m.active != tabID(i) {
			t.Fatalf("key %q: active=%d want %d", key, m.active, i)
		}
	}
	// tab cycles forward from last
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyType(tea.KeyTab)})
	_ = collectMsgs(cmd)
	if m.active != tabStatus {
		t.Fatalf("tab wrap: active=%d want 0", m.active)
	}
}

func TestRootModelNotConfiguredShowsInstallHint(t *testing.T) {
	b := newFakeBackend()
	b.snapErr = errors.New("not configured")
	m := loadModel(t, b)
	view := m.View()
	if !strings.Contains(view, "Not configured") {
		t.Fatalf("expected not-configured view, got:\n%s", view)
	}
	if !strings.Contains(view, "ncp install") {
		t.Fatalf("install hint missing")
	}
}

func TestRootModelQuitKeys(t *testing.T) {
	for _, key := range []string{"q", "ctrl+c"} {
		b := newFakeBackend()
		m := loadModel(t, b)
		var cmd tea.Cmd
		m, cmd = m.Update(tea.KeyMsg{Type: keyTypeFor(key), Runes: []rune(key)})
		if cmd == nil {
			t.Fatalf("key %q should produce a quit command", key)
		}
	}
}

// -----------------------------------------------------------------------------
// actions
// -----------------------------------------------------------------------------

func TestConfirmOverlayUnlinkFlow(t *testing.T) {
	b := newFakeBackend()
	b.actionResult = ActionResult{Changed: true, Phase: "committed"}
	m := loadModel(t, b)
	m.active = tabSites

	// open the unlink confirm
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyType(tea.KeyRunes), Runes: []rune("d")})
	_ = collectMsgs(cmd)
	if !m.overlay.Active() {
		t.Fatalf("unlink key did not open confirm overlay")
	}
	if !strings.Contains(m.overlay.View(), "Unlink app.test") {
		t.Fatalf("confirm overlay missing domain:\n%s", m.overlay.View())
	}

	// confirm with y — Update consumes the overlay and returns the action
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyType(tea.KeyRunes), Runes: []rune("y")})
	pump(t, &m, cmd)
	if m.overlay.Active() {
		t.Fatalf("overlay should be closed after confirm")
	}
	if len(b.actions) == 0 || b.actions[0] != "unlink:app.test:" {
		t.Fatalf("backend unlink not recorded: %v", b.actions)
	}
	if m.running != nil {
		t.Fatalf("action should be finished")
	}
	if len(m.log) == 0 || m.log[len(m.log)-1].status != "changed" {
		t.Fatalf("activity log missing changed entry: %+v", m.log)
	}
}

func TestConfirmOverlayCancelKeepsSite(t *testing.T) {
	b := newFakeBackend()
	m := loadModel(t, b)
	m.active = tabSites

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyType(tea.KeyRunes), Runes: []rune("d")})
	_ = collectMsgs(cmd)
	// cancel with esc — Update consumes the overlay and starts nothing
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyType(tea.KeyEscape)})
	pump(t, &m, cmd)
	if m.overlay.Active() {
		t.Fatalf("overlay should be closed after cancel")
	}
	if len(b.actions) != 0 {
		t.Fatalf("backend must not be called: %v", b.actions)
	}
}

func TestSelectOverlayPHPInstall(t *testing.T) {
	b := newFakeBackend()
	b.actionResult = ActionResult{Changed: true, Phase: "committed"}
	m := loadModel(t, b)
	m.active = tabPHP

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyType(tea.KeyRunes), Runes: []rune("a")})
	_ = collectMsgs(cmd)
	if !m.overlay.Active() {
		t.Fatalf("php install key did not open select overlay")
	}
	// fake installs 8.3+8.4 → catalog shows 8.2, 8.5; cursor starts at 8.2
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyType(tea.KeyDown)})
	_ = collectMsgs(cmd)
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyType(tea.KeyEnter)})
	pump(t, &m, cmd)
	if len(b.actions) == 0 || b.actions[0] != "php-install:8.5:" {
		t.Fatalf("backend php install not recorded: %v", b.actions)
	}
}

func TestLinkFormSubmit(t *testing.T) {
	b := newFakeBackend()
	b.linkResult = LinkResult{ID: "x1", Domain: "new.test", Phase: "committed", Database: "newdb", DBUser: "newdb", DBPassword: "secret"}
	m := loadModel(t, b)
	m.active = tabSites

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyType(tea.KeyRunes), Runes: []rune("a")})
	_ = collectMsgs(cmd)
	if !m.overlay.Active() {
		t.Fatalf("link key did not open form overlay")
	}
	// type a domain into the first field
	for _, r := range "new.test" {
		m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyType(tea.KeyRunes), Runes: []rune{r}})
	}
	_ = collectMsgs(cmd)
	if got := m.overlay.fields[0].value; got != "new.test" {
		t.Fatalf("typed domain = %q", got)
	}
	// tab through to the last field then submit — Update consumes the
	// overlay and dispatches the link action itself
	for i := 0; i < len(m.overlay.fields)-1; i++ {
		m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyType(tea.KeyTab)})
		_ = collectMsgs(cmd)
	}
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyType(tea.KeyEnter)})
	pump(t, &m, cmd)
	if m.overlay.Active() {
		t.Fatalf("overlay should be closed after submit")
	}
	if len(b.actions) == 0 || b.actions[0] != "link:new.test:" {
		t.Fatalf("backend link not recorded: %v", b.actions)
	}
	// link results with credentials land in the activity log
	found := false
	for _, e := range m.log {
		if strings.Contains(e.detail, "newdb") && strings.Contains(e.detail, "secret") {
			found = true
		}
	}
	if !found {
		t.Fatalf("link credentials missing from activity log: %+v", m.log)
	}
}

func TestServiceActionRestart(t *testing.T) {
	b := newFakeBackend()
	b.actionResult = ActionResult{Changed: true, Phase: "restarted"}
	m := loadModel(t, b)
	m.active = tabServices

	var cmd tea.Cmd
	// nginx is installed + running + active in actuals → restart allowed
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyType(tea.KeyEnter)})
	pump(t, &m, cmd)
	if len(b.actions) == 0 || b.actions[0] != "service:nginx:restart" {
		t.Fatalf("backend restart not recorded: %v", b.actions)
	}
}

func TestFailedActionLogsError(t *testing.T) {
	b := newFakeBackend()
	b.actionErr = errors.New("transaction failed: rollback")
	m := loadModel(t, b)
	m.active = tabServices

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyType(tea.KeyEnter)})
	pump(t, &m, cmd)
	if len(m.log) == 0 {
		t.Fatalf("failure not logged: log empty")
	}
	last := m.log[len(m.log)-1]
	if last.status != "failed" || !strings.Contains(last.detail, "rollback") {
		t.Fatalf("failure not logged: %+v", m.log)
	}
	if m.running != nil {
		t.Fatalf("running must clear after failure")
	}
}

// -----------------------------------------------------------------------------
// view rendering
// -----------------------------------------------------------------------------

func TestViewsRenderWithoutPanic(t *testing.T) {
	b := newFakeBackend()
	m := loadModel(t, b)
	for tab := tabStatus; tab < tabCount; tab++ {
		m.active = tab
		v := m.View()
		if v == "" {
			t.Fatalf("tab %d rendered empty view", tab)
		}
	}
}

func TestActivityViewShowsLogEntries(t *testing.T) {
	b := newFakeBackend()
	m := loadModel(t, b)
	m.active = tabActivity
	m.log = appendLog(m.log, logEntry{time: "12:00:00", label: "php install 8.4", status: "changed", detail: "committed"})
	v := m.View()
	if !strings.Contains(v, "php install 8.4") || !strings.Contains(v, "changed") {
		t.Fatalf("activity view missing entry:\n%s", v)
	}
}

// keyTypeFor maps common key names to bubbletea types for tests.
func keyTypeFor(key string) tea.KeyType {
	switch key {
	case "ctrl+c":
		return tea.KeyCtrlC
	default:
		return tea.KeyRunes
	}
}
